#!/usr/bin/env bash
# Prove release/compose.yaml starts from nothing.
#
# "Nothing" is literal: the deployment runs from a directory holding only a copy
# of compose.yaml, so any file this repository happens to have lying around
# cannot be what made it work. Conformance itself is proven by the scenario
# gates on the same image; this proves what only this compose can get wrong --
# first-run bootstrap, the public issuer, sign-in through Caddy, a restart that
# changes nothing, and a refusal that says why.
#
# Everything it creates is removed on exit, volumes included: the host this was
# written on has little disk to spare.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_GATE_PROJECT:-voptrack-release-gate}"
https_port="${VOPTRACK_RELEASE_HTTPS_PORT:-7543}"
http_port="${VOPTRACK_RELEASE_HTTP_PORT:-7580}"
cp "$root/release/compose.yaml" "$work/"

dc() { ( cd "$work" && docker compose -p "$project" "$@" ); }
cleanup() { ci_phase cleanup dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT

say()  { printf '  %-40s %s\n' "$1" "$2"; }
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc logs --tail 30 >&2 2>/dev/null || true; exit 1; }

export VOPTRACK_HTTPS_PORT="$https_port" VOPTRACK_HTTP_PORT="$http_port"
export VOPTRACK_DOMAIN=127.0.0.1
export VOPTRACK_PUBLIC_URL="https://127.0.0.1:${https_port}"
unset VOPTRACK_STORAGE_ENCRYPTED

echo "Pull and start"
say "host directory holds" "$(ls "$work")"

# Without the attestation nothing may start, and the reason must be printed.
if ci_phase bootstrap dc up -d --wait >/dev/null 2>&1; then
  fail "the deployment started without VOPTRACK_STORAGE_ENCRYPTED=yes"
fi
# Captured first, then searched. Piping into grep -q raced: grep exits at the
# first match, docker compose logs dies of SIGPIPE if it is still writing, and
# pipefail turns that into a failure of a check that had in fact passed.
init_log="$(dc logs init 2>/dev/null || true)"
grep -q "VOPTRACK_STORAGE_ENCRYPTED is not" <<<"$init_log" \
  || fail "the refusal did not say what was missing"
[ -z "$(dc ps -q tesserad 2>/dev/null)" ] || [ "$(docker inspect -f '{{.State.Running}}' "$(dc ps -aq tesserad)")" != true ] \
  || fail "the server is running although init refused"
say "without the storage attestation" "refused, with the reason"
dc down -v >/dev/null 2>&1

export VOPTRACK_STORAGE_ENCRYPTED=yes
# The personal owner deliberately holds dataset administration only. Provision
# a disposable server operator through the supported stopped-host CLI before
# first serving; do not broaden the real personal owner's rights.
dc run --rm -T init >/dev/null 2>&1 || fail "initial generation failed"
dc run --rm --no-deps -T --entrypoint sh init -c '
  umask 077
  openssl rand -base64 24 > /var/lib/voptrack-bootstrap/backup-gate-password
  tesserad -config /var/lib/voptrack-config/server.json -add-person backup-gate-operator \
    -may-administer-server -password-file /var/lib/voptrack-bootstrap/backup-gate-password >/dev/null
' >/dev/null 2>&1 || fail "could not provision the disposable backup operator"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "docker compose up did not become healthy"
say "first start" "healthy"

dc run --rm -T init password > "$work/pw" 2>/dev/null || fail "could not read the initial password"

# The startup pair is acquired through the public catalog and exact leases.
# Never inspect private acquisition directories or guess artifact host paths.
chmod 600 "$work/pw"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
dc run --rm -T --entrypoint cat init /var/lib/voptrack-bootstrap/backup-gate-password > "$work/operator-password"
chmod 600 "$work/operator-password"
acquire() { python3 "$root/release/online-backup/acquire.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/operator-password" "$@" --account backup-gate-operator; }
acquire "$work/startup" || fail "could not acquire native startup pair"
module="$(cd "$root" && go list -m -f '{{.Dir}}' github.com/ekarpov/tessera)"
verify_pair() {
  local dir="$1" metadata deployment
  metadata="$(cat "$dir/metadata-id")"; deployment="$(cat "$dir/deployment")"
  # The supported upstream verifier is used as a dependency, not copied into
  # Voptrack. Only this disposable offline verifier sees the retained key.
  tar -C "$dir" -cf - . | docker run --rm -i --network none --entrypoint sh     -v "$module/tools:/tools:ro" -v "${project}_config:/config:ro" python:3-alpine -c     'mkdir -m700 /tmp/pair; tar -xf - -C /tmp/pair; exec python3 /tools/verify_acquisition.py --metadata "/tmp/pair/$1" --key-file /config/backup.key --deployment "$2"' sh "$metadata" "$deployment" >/dev/null
}
restore_backup() {
  local pair="$work/startup" fence="$work/startup"
  [ "$1" = oldest ] && fence="$work/newer-evidence"
  verify_pair "$pair" || return 1
  verify_pair "$fence" || return 1
  [ "$(cat "$pair/deployment")" = "$(cat "$fence/deployment")" ] || return 1
  local input="$work/restore-input"
  mkdir -m700 "$input"
  cp "$pair/$(cat "$pair/archive-id")" "$input/archive"
  cp "$fence/$(cat "$fence/evidence-id")" "$input/evidence"
  tar -C "$input" -cf - . | docker run --rm -i --entrypoint sh --user 10001:10001     -v "${project}_config:/var/lib/voptrack-config:ro" -v "${project}_bootstrap:/var/lib/voptrack-bootstrap:ro"     --tmpfs /tmp:uid=10001,mode=0700 "${VOPTRACK_IMAGE}" -c '
      set -e
      mkdir -m700 /tmp/pair /tmp/restored /tmp/rbackups
      tar -xf - -C /tmp/pair
      sed -e "s#\"/var/lib/tessera\"#\"/tmp/restored\"#" -e "s#0.0.0.0:8443#127.0.0.1:9443#"           -e "s#/var/lib/voptrack-backups#/tmp/rbackups#" /var/lib/voptrack-config/server.json > /tmp/restore.json
      tesserad -config /tmp/restore.json -restore /tmp/pair/archive         -backup-key-file /var/lib/voptrack-config/backup.key -retirement-fence /tmp/pair/evidence >/dev/null
      tesserad -config /tmp/restore.json >/tmp/log 2>&1 &
      for i in $(seq 1 30); do curl -sf --cacert /var/lib/voptrack-config/server.crt https://127.0.0.1:9443/.well-known/tessera >/dev/null && break; sleep 1; done
      curl -s -o /dev/null -w "%{http_code}" --cacert /var/lib/voptrack-config/server.crt         -u "owner:$(cat /var/lib/voptrack-bootstrap/owner-password)" https://127.0.0.1:9443/admin' 2>&1 | tail -1
  local rc=${PIPESTATUS[1]}
  rm -rf "$input"
  return "$rc"
}
restored="$(restore_backup newest || true)"
[ "$restored" = 200 ] || fail "startup online pair did not authenticate and restore"
say "the startup pair, MAC-verified and restored" "starts; owner signs in"
chmod 600 "$work/pw"
[ -s "$work/pw" ] || fail "the initial password is empty"
say "owner's initial password" "readable through init, not the server"

if dc exec -T tesserad test -e /var/lib/voptrack-bootstrap/owner-password 2>/dev/null; then
  fail "the server can see the owner's initial password"
fi
say "server's view of that password" "none — volume not mounted"

dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt" \
  || fail "could not read Caddy's internal CA"

# Through Caddy, against the public issuer: administration, and a person
# completing the whole OAuth consent flow.
( cd "$root" && go run ./cmd/verify -url "$VOPTRACK_PUBLIC_URL" -ca "$work/ca.crt" \
    -conformance=false \
    -admin-user owner -admin-password-file "$work/pw" \
    -signin-user owner -signin-password-file "$work/pw" ) >"$work/verify.log" 2>&1 \
  || { cat "$work/verify.log" >&2; fail "signing in through the public address failed"; }
# An exit code of zero is not evidence that the checks ran. Both must be named
# as passing, so a flag that silently stops enabling one fails here.
for check in "signing in" "administration"; do
  grep -Eq "^[[:space:]]*ok[[:space:]]+$check\$" "$work/verify.log" \
    || { cat "$work/verify.log" >&2; fail "the gate did not report \"$check\" as passing"; }
done
issuer="$(curl -s --cacert "$work/ca.crt" "$VOPTRACK_PUBLIC_URL/.well-known/oauth-authorization-server" \
  | sed -n 's/.*"issuer":"\([^"]*\)".*/\1/p')"
[ "$issuer" = "$VOPTRACK_PUBLIC_URL" ] || fail "the advertised issuer is '$issuer', not the public address"
say "administration and sign-in via Caddy" "both passed"
say "advertised issuer" "$issuer"
discovery() { curl -s --cacert "$work/ca.crt" "$VOPTRACK_PUBLIC_URL/.well-known/tessera"; }
got="$(curl -s --cacert "$work/ca.crt" "$VOPTRACK_PUBLIC_URL/.well-known/voptrack")"
pin="sha256/$(openssl x509 -in "$work/ca.crt" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256 -binary | base64)"
[ "$got" = "{\"product\": \"personal\", \"timezone\": \"UTC\", \"week_start\": \"monday\", \"web_interface\": true, \"tls_pin\": \"$pin\"}" ] \
  || fail "/.well-known/voptrack answered '$got' for a personal server at its IP address"
# Public: no pin, and Caddy is left to get a certificate the ordinary way.
dc stop console tesserad >/dev/null 2>&1 || fail "could not quiesce for configuration generation"
public_tls="$(VOPTRACK_TLS=public dc run --rm -T --entrypoint sh init -c 'VOPTRACK_TLS=public /usr/local/bin/voptrack-init init >/dev/null 2>&1; cat /var/lib/voptrack-public/tls-pin /var/lib/voptrack-public/tls.caddy')"
case "$public_tls" in *"tls internal"*|*sha256/*) fail "VOPTRACK_TLS=public still announced a pin or internal certificates: $public_tls" ;; esac
[ -z "$(VOPTRACK_TLS=public dc run --rm -T --entrypoint sh init -c 'VOPTRACK_TLS=public /usr/local/bin/voptrack-init init >/dev/null 2>&1; ls /var/lib/voptrack-public/ca.pem 2>/dev/null')" ] \
  || fail "VOPTRACK_TLS=public still offers a root to anchor at"
VOPTRACK_TLS=local dc run --rm -T init >/dev/null 2>&1 || fail "init did not go back to local"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "could not restart after configuration generation"
case "$(discovery)" in *'"display_name"'*) fail "discovery carries a display name although none was set" ;; esac

# A restart, keeping the volumes: init runs again and must change nothing that
# matters. The password is the sharpest check -- a second provisioning would
# either fail or silently replace it.
before="$(cat "$work/pw")"
dc down >/dev/null 2>&1
# Renaming the server is a setting and a restart: init rewrites the
# configuration on every start (Tessera alpha.28's display_name).
export VOPTRACK_NAME="Gate Home · Дом"
ci_phase bootstrap dc up -d --wait >/dev/null 2>&1 || fail "the deployment did not come back after a restart"
after="$(dc run --rm -T init password 2>/dev/null)"
[ "$before" = "$after" ] || fail "the owner's password changed across a restart"
status="$(curl -s --cacert "$work/ca.crt" -o /dev/null -w '%{http_code}' \
  -u "owner:$after" "$VOPTRACK_PUBLIC_URL/admin")"
[ "$status" = 200 ] || fail "the owner cannot sign in after a restart ($status)"
say "restart" "same owner, same password, signs in"
case "$(discovery)" in *'"display_name":"Gate Home · Дом"'*) ;; *) fail "after setting VOPTRACK_NAME and restarting, discovery does not carry it" ;; esac
say "display name" "absent until set; set by a restart"

# init runs on every start. If it ever replaced the backup key, every archive
# made before would become unreadable -- so restore the OLDEST archive, from
# before the restart, with the key as it is now.
count="$(acquire "$work/unused" --count)" || fail "catalog unavailable after restart"
[ "$count" -ge 2 ] || fail "restart did not publish another pair"
acquire "$work/newer-evidence" --evidence || fail "new evidence could not be acquired"
restored="$(restore_backup oldest || true)"
[ "$restored" = 200 ] || fail "an archive from before the restart no longer restores with the current key ($restored)"
say "an archive from before the restart" "still restores with today's key"

echo
echo "ok — the release compose starts from an empty directory and survives a restart"
