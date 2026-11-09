#!/usr/bin/env bash
# Prove the Console can be kept off the internet: Home with
# VOPTRACK_CONSOLE_PRIVATE=yes, from an empty directory. The public address
# serves Tessera (what the app talks to) and not the Console; the Console
# answers on its own port, published on 127.0.0.1 only; and the owner signs in
# across the two. Removes everything it creates, volumes included.
set -euo pipefail
# This scenario qualifies the explicit consent-based fallback.
export VOPTRACK_WEBSITE_SIGN_IN=off
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_GATE_PROJECT:-voptrack-private-console-gate}"
port="${VOPTRACK_PRIVATE_HTTPS_PORT:-7643}" cport="${VOPTRACK_PRIVATE_CONSOLE_PORT:-7644}"
cp "$root/release/compose.yaml" "$work/"
dc() { ( cd "$work" && docker compose -p "$project" "$@" ); }
cleanup() { ci_phase cleanup dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc logs --tail 20 console caddy >&2 2>/dev/null || true; exit 1; }
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_PRIVATE_HTTP_PORT:-7680}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_VARIANT=household
export VOPTRACK_CONSOLE_PRIVATE=yes VOPTRACK_CONSOLE_PORT="$cport"
echo "Home with the Console kept on the home network"
if ! ci_phase bootstrap dc up -d --wait >"$work/startup.log" 2>&1; then
  tail -n 40 "$work/startup.log" >&2
  fail "the deployment did not become healthy"
fi
dc run --rm -T init password > "$work/owner" 2>/dev/null; chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
bound="$(dc port caddy 9443)"
[ "$bound" = "127.0.0.1:$cport" ] || fail "the Console's port is published on $bound, want 127.0.0.1:$cport only"
printf '  %-46s %s\n' "the Console's port" "published on 127.0.0.1 only"
ci_phase verification python3 "$root/release/private-console/drive.py" "https://localhost:$port" "https://localhost:$cport" "$work/ca.crt" "$work/owner" \
  || fail "the private Console checks failed"
echo
echo "ok — the internet reaches Tessera, and only the household reaches the Console"
