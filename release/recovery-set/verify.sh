#!/usr/bin/env bash
# Local focused gate; independent-host replay is a separately recorded operation.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${VOPTRACK_IMAGE:?build and select an immutable image}"
work="$(mktemp -d)"
retained="${VOPTRACK_RECOVERY_SET_DIR:-$work/retained}"
mkdir -m700 -p "$retained"
project="${VOPTRACK_RECOVERY_SET_PROJECT:-voptrack-recovery-set}"
# Refuse a colliding project before installing a cleanup trap.
for resource in container network volume; do
 case "$resource" in container) existing=$(docker ps -aq --filter "label=com.docker.compose.project=$project");; network) existing=$(docker network ls -q --filter "label=com.docker.compose.project=$project");; volume) existing=$(docker volume ls -q --filter "label=com.docker.compose.project=$project");; esac
 if [ -n "$existing" ]; then rm -rf "$work"; echo 'Disposable project name already in use' >&2; exit 2; fi
done
cp "$root/release/compose.yaml" "$work/"
dc() { (cd "$work" && docker compose -p "$project" "$@"); }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
export VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_BACKUP_KEEP=8 VOPTRACK_TIMEZONE=UTC
export VOPTRACK_HTTPS_PORT="${VOPTRACK_RECOVERY_SET_PORT:-30743}" VOPTRACK_HTTP_PORT="${VOPTRACK_RECOVERY_SET_HTTP_PORT:-30780}"
export VOPTRACK_CONSOLE_PORT="${VOPTRACK_RECOVERY_SET_CONSOLE_PORT:-30744}" VOPTRACK_PROXY_SUBNET="${VOPTRACK_RECOVERY_SET_SUBNET:-10.219.91.0/24}"
export VOPTRACK_PROXY_ADDRESS="${VOPTRACK_RECOVERY_SET_PROXY_ADDRESS:-10.219.91.2}" VOPTRACK_PROXY_POOL="${VOPTRACK_RECOVERY_SET_PROXY_POOL:-10.219.91.128/25}"
export VOPTRACK_PUBLIC_URL="https://localhost:$VOPTRACK_HTTPS_PORT"
for variant in household research; do
 export VOPTRACK_VARIANT="$variant"
 if ! dc up -d --wait > "$work/startup.log" 2>&1; then
  cp "$work/startup.log" "$retained/startup-failure.log"
  dc logs --no-color > "$retained/startup-container-failure.log" 2>&1 || true
  echo 'Disposable source startup failed; private diagnostics retained' >&2
  exit 1
 fi
 dc run --rm -T init password > "$work/owner" 2>/dev/null
 dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
 chmod 600 "$work/owner"
 python3 "$root/release/recovery-set/create.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" "$work" "$project" "$retained/$variant"
 python3 "$root/release/recovery-set/drill.py" "$retained/$variant" "$VOPTRACK_IMAGE" "$project-drill" "${VOPTRACK_RECOVERY_SET_RESTORE_PORT:-30843}"
done
printf 'ok — isolated Home/Research recovery sets; same-host gate makes no independent-host custody claim\n'
