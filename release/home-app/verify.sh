#!/usr/bin/env bash
# Prove Voptrack Home without its web interface (VOPTRACK_CONSOLE=off) from an
# empty directory: nothing serves a Console, no credential on the server reads
# across members, the owner invites from the host, the owner may stop a
# member's share without reading it, and the owner's suspension
# reaches a member's reading. Removes everything it creates, volumes included.
set -euo pipefail
umask 077
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_GATE_PROJECT:-voptrack-home-app-gate}"
port="${VOPTRACK_HOMEAPP_HTTPS_PORT:-7543}" cport="${VOPTRACK_HOMEAPP_CONSOLE_PORT:-7544}"
cp "$root/release/compose.yaml" "$work/"
dc() { ( cd "$work" && docker compose -p "$project" "$@" ); }
cleanup() { ci_phase cleanup dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc logs --tail 20 init console caddy >&2 2>/dev/null || true; exit 1; }
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_HOMEAPP_HTTP_PORT:-7580}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_VARIANT=household
export VOPTRACK_CONSOLE=off VOPTRACK_CONSOLE_PORT="$cport" VOPTRACK_TIMEZONE=Europe/Helsinki VOPTRACK_STORE_LIMIT_MB=64
echo "Home without its web interface"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "the deployment did not become healthy"
dc run --rm -T init password > "$work/owner" 2>/dev/null; chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
secrets="$(dc exec -T console sh -c 'find /var/lib/voptrack-console -name "*.secret" | wc -l' | tr -d ' \r')"
[ "$secrets" = 0 ] || fail "the Console's volume holds $secrets secrets, want none"
printf '  %-46s %s\n' "the Console's volume" "holds no secret"
# What the app reads before signing in: the product, the household's clock,
# and that there is no web interface -- served with the Console off.
# A local Home announces its CA pin: the SHA-256 of ca.crt's public key,
# computed here independently, so the key the app pins is the one Caddy chains to.
pin="sha256/$(openssl x509 -in "$work/ca.crt" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256 -binary | base64)"
got="$(curl -s --cacert "$work/ca.crt" "https://localhost:$port/.well-known/voptrack")"
want="{\"product\": \"home\", \"timezone\": \"Europe/Helsinki\", \"week_start\": \"monday\", \"web_interface\": false, \"tls_pin\": \"$pin\"}"
[ "$got" = "$want" ] || fail "/.well-known/voptrack answered '$got'; want '$want'"
dc stop console tesserad >/dev/null 2>&1 || fail "could not quiesce for time zone validation"
bad="$(VOPTRACK_TIMEZONE=Mars/Olympus_Mons dc run --rm -T --no-deps init 2>&1)" && fail "init accepted a time zone that does not exist"
case "$bad" in *"no time zone called Mars/Olympus_Mons"*) ;; *) fail "init refused a bad time zone without saying why: $bad" ;; esac
dc run --rm -T --no-deps init >/dev/null 2>&1 || fail "could not restore valid time zone configuration"
dc up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || fail "could not restart after time zone validation"
printf '  %-46s %s\n' "/.well-known/voptrack" "home, Europe/Helsinki, monday, no web interface; a bad zone refused"
link="$(dc run --rm -T init invite 2>/dev/null)"
case "$link" in https://localhost:"$port"/oauth/invitation*) ;; *) fail "init invite printed no invitation link" ;; esac
python3 -c 'import sys, urllib.parse as u; q = dict(u.parse_qsl(u.urlparse(sys.argv[1]).query)); sys.exit(0 if q.get("pin") == sys.argv[2] and q.get("code") else 1)' "$link" "$pin" \
  || fail "init invite's link does not carry this Home's pin"
curl -s --cacert "$work/ca.crt" "https://localhost:$port/.well-known/voptrack/ca.pem" > "$work/served.pem"
served="sha256/$(openssl x509 -in "$work/served.pem" -pubkey -noout 2>/dev/null | openssl pkey -pubin -outform DER | openssl dgst -sha256 -binary | base64)"
[ "$served" = "$pin" ] || fail "/.well-known/voptrack/ca.pem is not the root the pin names"
printf '  %-46s %s\n' "the local CA pin" "announced, matches ca.crt, on the invitation; the root served"
ci_phase verification python3 "$root/release/home-app/drive.py" "https://localhost:$port" "https://localhost:$cport" "$work/ca.crt" "$work/owner" "$link" \
  || fail "the app-only Home checks failed"

# Console-off still runs discovery/health and holds the shared guard. Reproduce
# the old stop-only-Tessera workflow: it must fail without issuing a device link.
dc stop tesserad >/dev/null 2>&1 || fail "could not stop Tessera for the old-workflow probe"
console_id="$(dc ps -q console)"
[ -n "$console_id" ] && [ "$(docker inspect -f '{{.State.Running}}' "$console_id")" = true ] \
  || fail "Console-off was not alive for the maintenance regression probe"
if dc run --rm -T --no-deps init owner-device > "$work/refused-link" 2> "$work/refused-error"; then
  fail "stop-only-Tessera owner-device unexpectedly succeeded"
fi
[ ! -s "$work/refused-link" ] && grep -q 'maintenance unavailable: stop both console and tesserad' "$work/refused-error" \
  || fail "old owner-device workflow failed for the wrong reason or emitted a link"
printf '  %-46s %s\n' "old stop-only-Tessera owner-device" "refused while Console-off remains alive; no link emitted"

# First phone and lost-phone replacement use the same guarded host workflow.
# Fail before restarting if either quiescence or owner-device fails. Service-only
# up must not rerun init after it has started either lock holder.
owner_device() {
  dc stop console tesserad >/dev/null 2>&1 || fail "could not stop both producers for owner-device"
  local running; running="$(dc ps --status running -q console tesserad)" || fail "could not check producer state"
  [ -z "$running" ] || fail "a producer is still running"
  local l; l="$(dc run --rm -T --no-deps init owner-device 2>/dev/null)" || fail "init owner-device failed; services remain stopped"
  [ -n "$l" ] || fail "init owner-device returned no link; services remain stopped"
  dc up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || fail "the deployment did not come back after owner-device"
  printf '%s' "$l"
}
dev() { python3 "$root/release/home-app/device.py" "$@"; }
first="$(owner_device)"
case "$first" in https://localhost:"$port"/oauth/invitation\?code=*\&pin=*) ;; *) fail "init owner-device printed no valid link" ;; esac
python3 -c 'import sys, urllib.parse as u; q = dict(u.parse_qsl(u.urlparse(sys.argv[1]).query)); sys.exit(0 if q.get("pin") == sys.argv[2] and q.get("code") else 1)' "$first" "$pin" \
  || fail "owner-device link does not preserve the local CA pin"
t1="$(dev redeem "https://localhost:$port" "$work/ca.crt" "$first" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token",""))')"
[ -n "$t1" ] || fail "the owner's device link did not redeem"
[ -z "$(dev redeem "https://localhost:$port" "$work/ca.crt" "$first" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token",""))')" ] \
  || fail "the first owner's link redeemed twice"
[ "$(dev status "https://localhost:$port" "$work/ca.crt" "$t1" /admin/api/v1/overview)" = 200 ] || fail "the owner's device has no server rights"
second="$(owner_device)"
python3 -c 'import sys, urllib.parse as u; q = dict(u.parse_qsl(u.urlparse(sys.argv[1]).query)); sys.exit(0 if q.get("pin") == sys.argv[2] and q.get("code") else 1)' "$second" "$pin" \
  || fail "replacement owner-device link does not preserve the local CA pin"
[ "$(dev status "https://localhost:$port" "$work/ca.crt" "$t1" /admin/api/v1/overview)" = 401 ] \
  || fail "after a second owner-device, the first owner device still works"
t2="$(dev redeem "https://localhost:$port" "$work/ca.crt" "$second" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token",""))')"
[ -n "$t2" ] && [ "$(dev status "https://localhost:$port" "$work/ca.crt" "$t2" /admin/api/v1/overview)" = 200 ] \
  || fail "the new owner link did not give a working device"
[ -z "$(dev redeem "https://localhost:$port" "$work/ca.crt" "$second" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token",""))')" ] \
  || fail "the owner's link redeemed twice"
printf '  %-46s %s\n' "the owner's device from the host" "server rights; a second run signs the first out (401); once only"
echo
echo "ok — no web interface, no credential reading across members, and the owner's suspension reaches reading"
