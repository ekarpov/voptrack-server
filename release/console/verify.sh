#!/usr/bin/env bash
# Prove Voptrack Home's Console end to end, from an empty directory, the way a
# household uses it: nothing is set up through /admin or on the host. The owner
# invites from the Console; members join, record from their own app sign-in,
# and choose on the Sharing page what Home sees; drive.py checks every number
# exactly. Removes everything it creates, volumes included.
set -euo pipefail
# This scenario qualifies the explicit consent-based fallback.
export VOPTRACK_WEBSITE_SIGN_IN=off

root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_CONSOLE_PROJECT:-voptrack-console-gate}"
port="${VOPTRACK_CONSOLE_HTTPS_PORT:-7743}"
cp "$root/release/compose.yaml" "$work/"

dc() { ( cd "$work" && docker compose -p "$project" "$@" ); }
# KEEP=1 leaves the deployment running after a failure, for inspection.
cleanup() {
  if [ "${KEEP:-}" = 1 ]; then echo "kept: $work (project $project)" >&2; return; fi
  dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"
}
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
say()  { printf '  %-46s %s\n' "$1" "$2"; }
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc logs --tail 20 console tesserad >&2 2>/dev/null || true; exit 1; }

export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_CONSOLE_HTTP_PORT:-7780}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes
export VOPTRACK_VARIANT=household VOPTRACK_TIMEZONE=UTC
# The extended pin/rotation oracle requires exactly two native retained pairs.
# Set this fixture explicitly rather than inheriting a developer's environment.
if [ "${VOPTRACK_ONLINE_PROGRESS:-}" = 1 ]; then export VOPTRACK_BACKUP_KEEP=2; fi

echo "Voptrack Home, through the Console"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "the deployment with the Console did not become healthy"
dc run --rm -T init password > "$work/owner" 2>/dev/null; chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
say "Tessera, Console and Caddy" "healthy, one hostname"

VOPTRACK_GATE_WORK="$work" VOPTRACK_GATE_PROJECT="$project" python3 "$root/release/console/drive.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" \
  || fail "the Console's checks failed"

echo
dc stop console tesserad >/dev/null 2>&1 || fail "could not quiesce for settings application"
if VOPTRACK_BACKUP_REQUEST_REVISION=stale dc run --rm -T init >/dev/null 2>&1; then fail "stale host application was accepted"; fi
dc run --rm -T init >/dev/null 2>&1 || fail "could not apply saved backup request"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "settings restart failed"
dc exec -T console cat /var/lib/voptrack-public/backup-settings.json > "$work/backup-settings.json"
python3 - "$work/backup-settings.json" <<'PYSETTINGS'
import json,sys
v=json.load(open(sys.argv[1]))
assert (v['destination'],v['every_hours'],v['keep'])==('reserve',12,1), 'generated settings did not apply'
PYSETTINGS
# New pair artifacts are opaque: inspect only the supported authenticated
# catalog, never walk Tessera's private acquisition layout.
python3 "$root/release/console/catalog_check.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/gate-administrator"
dc restart tesserad >/dev/null 2>&1
dc up -d --no-deps --wait tesserad console caddy collector >/dev/null 2>&1 || fail "retention restart failed"
python3 "$root/release/console/catalog_check.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/gate-administrator"
say "host applies reviewed backup settings" "reserve/12h/1 generated; startup archive in new slot"

echo "ok — Home adds up exactly what members chose to share, and they can change their minds"
