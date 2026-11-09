#!/usr/bin/env bash
# Prove Voptrack Research from an empty directory: the researcher designs a
# study's tracks and opens it in the Console, the public reads it, consent is
# versioned, people join under pseudonyms and record, the researcher's
# dashboards add up across them, and participants are suspended, withdraw and
# leave. Removes everything it creates, volumes included.
set -euo pipefail
# This scenario qualifies the explicit consent-based fallback.
export VOPTRACK_WEBSITE_SIGN_IN=off
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_GATE_PROJECT:-voptrack-research-gate}"
port="${VOPTRACK_RESEARCH_HTTPS_PORT:-7843}"
cp "$root/release/compose.yaml" "$work/"
dc() { ( cd "$work" && docker compose -p "$project" "$@" ); }
cleanup() { ci_phase cleanup dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc logs --tail 20 console >&2 2>/dev/null || true; exit 1; }
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_RESEARCH_HTTP_PORT:-7880}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_VARIANT=research
echo "Research: studies"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "the research deployment did not become healthy"
dc run --rm -T init password > "$work/owner" 2>/dev/null; chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
ci_phase verification python3 "$root/release/research/drive.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" || fail "the study checks failed"

# Consent records are immutable, even to the Console's own service.
dc exec -T console cat /var/lib/voptrack-console/console-state.secret > "$work/state.secret"
dc exec -T console cat /var/lib/voptrack-console/console-state.dataset > "$work/state.dataset"
chmod 600 "$work/state.secret"
python3 "$root/release/research/immutable.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/state.secret" "$work/state.dataset" \
  || fail "consent records can be changed"

# The Console keeps studies and consent records in the server, not its volume:
# none of those documents are files. The private recovery-material journal is
# deployment state, validated separately below. Then the volume is lost -- everything but what init wrote
# once at install (the services' secrets, the Console store's identifier) and
# the OAuth registration. The study comes back as the Console starts; joining
# comes back at the researcher's sign-in, which replaces the lost invitation
# secret.
docs=$(dc exec -T console sh -c 'find /var/lib/voptrack-console -name "*.json" ! -path "/var/lib/voptrack-console/.recovery-material.json" ! -path "/var/lib/voptrack-console/backup-live.json" | grep -v "/oauth-client" | wc -l' | tr -d ' \r')
[ "$docs" = 0 ] || fail "the Console still keeps $docs documents as files"
# The exact live-settings control journal is not a Console document. Validate
# its public-only shape independently rather than hiding arbitrary JSON files.
dc exec -T console cat /var/lib/voptrack-console/backup-live.json > "$work/live-settings.json"
python3 - "$work/live-settings.json" <<'PYLIVE'
import json,re,sys
v=json.load(open(sys.argv[1]))
assert set(v)=={'commands','observation','destination'} and v['commands'] in (None,[]) and v['destination']=='local'
o=v['observation']
assert set(o)=={'instance_id','observed_at','history_scope','configured','available','reason','enabled','settings','settings_scope','pending_request_id','last_scheduled_attempt','active_scheduled_operation','next_attempt_at','next_reason','retention','settings_export'}
assert o['history_scope']=='process_instance' and o['configured'] and o['available']
assert re.fullmatch('[0-9a-f]{32}\.[0-9a-f]{32}\.[0-9a-f]{32}',o['settings']['revision'])
assert set(o['settings_export'])=={'version','deployment','every_hours','keep','revision','exported_at'}
assert o['settings_export']['revision']==o['settings']['revision']
PYLIVE
dc exec -T console cat /var/lib/voptrack-console/.recovery-material.json > "$work/material-state.json"
python3 - "$work/material-state.json" <<'PYMATERIAL'
import json,re,sys
state=json.load(open(sys.argv[1]))
assert set(state)=={'revision','pending'} and re.fullmatch('[a-f0-9]{48}',state['revision']) and state['pending']=={},'unexpected or unreconciled private material journal'
PYMATERIAL
dc exec -T console sh -c 'cd /var/lib/voptrack-console && rm -rf studies dashboards home-sources.json' \
  || fail "could not empty the Console's volume"
dc exec -T console sh -c 'ls /var/lib/voptrack-console/studies 2>/dev/null' | grep -q . && fail "the Console's volume still holds study files"
dc restart console >/dev/null 2>&1 && dc up -d --no-deps --wait console >/dev/null 2>&1 || fail "the Console did not restart"
ci_phase verification python3 "$root/release/research/reopen.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" before || fail "the study did not come back from the server"
ci_phase verification python3 "$root/release/research/reopen.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" after || fail "the studies did not come back"
# Reproduce a crash after the issuer secret/document are saved but before the
# material journal is cleared. Keep the existing secret; approved sign-in must
# reconcile the pending operation rather than treating the file as completion.
dc exec -T console test -s /var/lib/voptrack-console/studies/sleep-mood.issuer-secret
dc exec -T console cat /var/lib/voptrack-console/.recovery-material.json > "$work/material-state.json"
python3 - "$work/material-state.json" <<'PYPENDING'
import json,secrets,sys
path=sys.argv[1];state=json.load(open(path));state['revision']=secrets.token_hex(24);state['pending']['issuer:sleep-mood']=True
with open(path,'w') as out:json.dump(state,out)
PYPENDING
dc exec -T console sh -c 'cat > /var/lib/voptrack-console/.recovery-material.json' < "$work/material-state.json"
ci_phase verification python3 "$root/release/research/reopen.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" after || fail "pending issuer recovery failed"
dc exec -T console cat /var/lib/voptrack-console/.recovery-material.json > "$work/material-state.json"
python3 - "$work/material-state.json" <<'PYRECONCILED'
import json,sys
assert json.load(open(sys.argv[1]))['pending']=={},'completed issuer write remains pending after approved sign-in'
print('  completed issuer write recovery               pending cleared; joining still works')
PYRECONCILED
# Exercise the existing recovery commands with study dashboard documents in
# the archive, on fresh disposable volumes. No recovery behavior is changed.
dc restart tesserad >/dev/null 2>&1 && dc up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || fail "backup restart failed"
dc stop console tesserad >/dev/null 2>&1 || fail "could not quiesce deployment for export"
mkdir -m 700 "$work/restore"
dc run --rm -T init recovery-kit > "$work/restore/kit.tar" 2>/dev/null || fail "Research recovery kit failed"
dc run --rm -T init take-backup > "$work/restore/backup.tar" 2>/dev/null || fail "Research archive export failed"
dc down -v --remove-orphans >/dev/null 2>&1 || fail "could not retire disposable Research volumes"
dc run --rm -T init restore-kit < "$work/restore/kit.tar" >/dev/null 2>&1 || fail "Research restore-kit failed"
dc run --rm -T init restore < "$work/restore/backup.tar" >/dev/null 2>&1 || fail "Research archive restore failed"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "restored Research deployment did not become healthy"
# Compose waits for Caddy to run, not for its HTTPS listener to be ready.
curl --retry 15 --retry-connrefused --retry-delay 1 --max-time 3 -fsS --cacert "$work/ca.crt" "$VOPTRACK_PUBLIC_URL/.well-known/tessera" -o /dev/null 2>/dev/null || fail "restored public HTTPS listener did not become ready"
ci_phase verification python3 "$root/release/research/reopen.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" before || fail "restored studies did not load"
ci_phase verification python3 "$root/release/research/reopen.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" after archive || fail "study collection state did not survive archive restoration"
echo "  Research archive on fresh volumes             study, policy and draft state reopened"
echo
echo "ok — a researcher publishes a study, the public reads it, and consent is versioned"
