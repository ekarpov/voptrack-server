#!/usr/bin/env bash
# Focused actual-image kit/runtime gate, entirely disposable synthetic Home.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_KIT_PROJECT:-voptrack-kit-gate}"
cp "$root/release/compose.yaml" "$work/"
dc() { (cd "$work" && docker compose -p "$project" "$@"); }
cleanup() { ci_phase cleanup dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
export VOPTRACK_HTTPS_PORT="${VOPTRACK_KIT_HTTPS_PORT:-22743}" VOPTRACK_HTTP_PORT="${VOPTRACK_KIT_HTTP_PORT:-22780}"
export VOPTRACK_CONSOLE_PORT="${VOPTRACK_KIT_CONSOLE_PORT:-22744}" VOPTRACK_VARIANT=household VOPTRACK_STORAGE_ENCRYPTED=yes
export VOPTRACK_PUBLIC_URL="https://localhost:$VOPTRACK_HTTPS_PORT"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1
dc run --rm -T init password > "$work/owner" 2>/dev/null
chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
VOPTRACK_GATE_WORK="$work" VOPTRACK_GATE_PROJECT="$project" python3 "$root/release/kit/drive.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner"
# Abrupt wrapper/container death must not leave a permanently live observation.
id="$(dc ps -q tesserad)"
docker update --restart=no "$id" >/dev/null
docker kill "$id" >/dev/null
sleep 16
dc exec -T console cat /var/lib/voptrack-runtime/server.json > "$work/dead.json"
python3 - "$work/dead.json" <<'PY'
import datetime,json,sys
r=json.load(open(sys.argv[1]));observed=datetime.datetime.strptime(r['observed_at'][:19],'%Y-%m-%dT%H:%M:%S').replace(tzinfo=datetime.timezone.utc)
assert (datetime.datetime.now(datetime.timezone.utc)-observed).total_seconds()>15,'dead process observation did not age out'
PY
dc up -d --no-deps --wait tesserad >/dev/null 2>&1
dc exec -T tesserad cat /var/lib/voptrack-runtime/server.json > "$work/restarted.json"
python3 - "$work/dead.json" "$work/restarted.json" <<'PY'
import json,sys
old,new=(json.load(open(p)) for p in sys.argv[1:])
assert old['instance']!=new['instance'] and new['state']=='running','restart reused dead process observation'
PY
printf 'ok — constrained kit export and exact process observations; abrupt death/stale horizon and restart checked\n'
