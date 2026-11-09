#!/usr/bin/env bash
# Stand up, verify and tear down the single-user scenario.
#
# Verification is the point. Anything can start a container; this ends by
# running Tessera's own conformance suite against the deployment over its
# real URL, through the real proxy, with TLS verification enabled.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
. "$root/ci/select-image.sh"
domain="${VOPTRACK_DOMAIN:-localhost}"
port="${VOPTRACK_HTTPS_PORT:-443}"
base="https://${domain}:${port}"
compose=(docker compose -p "${VOPTRACK_SCENARIO_PROJECT:-single-user}" -f "$here/compose.yaml")

# The server runs as whoever invoked this. See the comment in compose.yaml:
# everything it reads or writes is bind-mounted and owned by this person.
export VOPTRACK_UID="$(id -u)" VOPTRACK_GID="$(id -g)"


init() {
  mkdir -p "$here/tls" "$here/.state/data"
  chmod 700 "$here/.state/data"
  if [ ! -f "$here/tls/server.crt" ]; then
    # The internal hop's certificate. Never presented to a client -- Caddy
    # holds the public one -- so its subject only has to satisfy TLS itself.
    openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
      -subj "/CN=tesserad" \
      -addext "subjectAltName=DNS:tesserad,DNS:localhost,IP:127.0.0.1" \
      -keyout "$here/tls/server.key" -out "$here/tls/server.crt" 2>/dev/null
    chmod 600 "$here/tls/server.key"
    echo "internal certificate generated"
  fi
  if [ ! -f "$here/server.json" ] && [ "${VOPTRACK_STORAGE_ENCRYPTED:-}" != "yes" ]; then
    cat >&2 <<'WHY'
Refusing to write server.json.

The server will not start until `encrypted_storage_confirmed` is true, and that
flag is an attestation by you, not a setting that does anything: it does not
encrypt storage, it records that you have arranged for it. Records live on this
volume in the clear otherwise.

Encrypt the volume this deployment will use, then:

    VOPTRACK_STORAGE_ENCRYPTED=yes make scenario-single-user-up

Setting it without encrypting the volume makes the attestation false, and
nothing downstream will catch that for you.
WHY
    return 1
  fi
  if [ ! -f "$here/server.json" ]; then
    # The issuer is the PUBLIC address, not the internal one: it is what a
    # client is told to authenticate against, and it must match what the
    # person's browser reaches.
    cat > "$here/server.json" <<JSON
{
  "listen": "0.0.0.0:8443",
  "issuer": "${base}",
  "data_dir": "/var/lib/tessera",
  "tls_certificate": "/etc/tessera/tls/server.crt",
  "tls_key": "/etc/tessera/tls/server.key",
  "engine": "sqlite",
  "content": true,
  "encrypted_storage_confirmed": true
}
JSON
    echo "server.json written for ${base}"
  fi
}

up() {
  init
  "${compose[@]}" up -d --wait 2>&1 | tail -3
  provision
  echo
  echo "Administration: ${base}/admin"
  echo "  account   owner"
  echo "  password  $here/.gate/admin-password"
  echo
  echo "The password is in that file and is not printed. Move it somewhere"
  echo "private, or change it, before this is a deployment anybody relies on."
}
down() { "${compose[@]}" down -v 2>&1 | tail -2; rm -rf "$here/.state" "$here/.gate"; }

# Everything this deployment needs provisioned, in one stopped window.
#
# Provisioning takes the same exclusive database lock as the server, so it
# cannot run beside one -- the second process is refused with
# dataset_unavailable rather than queued. Doing all three principals together
# costs one restart instead of three.
#
# Three, because they are genuinely different roles:
#   owner         a person who may erase, which is what makes them an
#                 administrator and lets them sign in to /admin
#   gate-full     read, write and schema for the conformance suite
#   gate-refused  write and NOT read, without which core and recovery cannot
#                 run and optional reads skips its own assertions
#
# Automation tokens rather than the token endpoint: client_credentials
# requires a dataset identifier, and a service cannot discover one without a
# token it cannot get without naming it. An operator provisioning locally
# already knows which dataset, so this avoids the circularity rather than
# working around it. A token intersects with what its principal holds, which
# is what makes a write-only person yield a write-only token.
provision() {
  if [ -f "$here/.gate/token" ] && [ -f "$here/.gate/refused" ] \
     && [ -f "$here/.gate/admin-password" ]; then return; fi
  mkdir -p "$here/.gate"; chmod 700 "$here/.gate"

  # Generated once and left on disk deliberately: an operator has to be able
  # to sign in, and a password printed to a terminal ends up in scrollback,
  # in a screen recording, and in whatever captured the build log.
  [ -f "$here/.gate/admin-password" ] || { openssl rand -base64 18 > "$here/.gate/admin-password"; chmod 600 "$here/.gate/admin-password"; }
  local pw; pw="$(mktemp)"; openssl rand -base64 18 > "$pw"

  "${compose[@]}" stop tesserad >/dev/null 2>&1
  rm -f "$here/.gate/token" "$here/.gate/refused"
  local admin=(--rm --no-deps -v "$here/.gate/admin-password:/tmp/pw:ro" -v "$here/.gate:/gate")
  local other=(--rm --no-deps -v "$pw:/tmp/pw:ro" -v "$here/.gate:/gate")
  local cfg=(-config /etc/tessera/server.json)

  # erase is what AuthenticateAdministrator looks for, and alpha.3 no longer
  # grants it by default -- `read,write` is the default now, so an
  # administrator has to be asked for explicitly.
  "${compose[@]}" run --interactive=false -T "${admin[@]}" tesserad "${cfg[@]}" \
    -add-person owner -grants read,write,schema,erase -password-file /tmp/pw >/dev/null
  "${compose[@]}" run --interactive=false -T "${other[@]}" tesserad "${cfg[@]}" \
    -add-person gate-full -grants read,write,schema -password-file /tmp/pw >/dev/null
  "${compose[@]}" run --interactive=false -T "${other[@]}" tesserad "${cfg[@]}" \
    -add-person gate-refused -grants write -password-file /tmp/pw >/dev/null
  "${compose[@]}" run --interactive=false -T "${other[@]}" tesserad "${cfg[@]}" -add-token gate-full -secret-output /gate/token >/dev/null
  "${compose[@]}" run --interactive=false -T "${other[@]}" tesserad "${cfg[@]}" -add-token gate-refused -secret-output /gate/refused >/dev/null

  rm -f "$pw"
  "${compose[@]}" start tesserad >/dev/null
  sleep 3
}

# Verification runs against a DISPOSABLE TWIN of this deployment, never against
# the deployment itself.
#
# Tessera's conformance suite says in its own documentation that it must address
# a disposable synthetic dataset: it creates types and records and leaves them
# behind. Run against a real deployment it would write synthetic junk into the
# operator's data permanently, and fail on the second run with 409 because the
# fixtures it expects to create already exist. Found by running it twice, which
# is the only way that shows up.
#
# So the twin: same image, same compose, same Caddyfile, same configuration
# template, its own volumes and ports, destroyed afterwards. What that proves
# is that this recipe produces a conformant deployment — which is the claim a
# scenario catalogue actually makes. It does not prove anything about the
# records in a running instance, and it is not supposed to.
verify() {
  local twin port project
  twin="$(mktemp -d)"; port="${VOPTRACK_TWIN_PORT:-18443}"; project="${VOPTRACK_TWIN_PROJECT:-voptrack-single-user-verify}"
  trap 'docker compose -p "$project" -f "$twin/compose.yaml" down -v >/dev/null 2>&1; rm -rf "$twin"' RETURN

  cp "$here/Caddyfile" "$here/compose.yaml" "$twin/"
  mkdir -p "$twin/tls" "$twin/.gate" "$twin/.state/data"; chmod 700 "$twin/.gate" "$twin/.state/data"
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=tesserad" \
    -addext "subjectAltName=DNS:tesserad,DNS:localhost,IP:127.0.0.1" \
    -keyout "$twin/tls/server.key" -out "$twin/tls/server.crt" 2>/dev/null
  local twinbase="https://localhost:${port}"
  sed "s|\"issuer\": \".*\"|\"issuer\": \"${twinbase}\"|" "$here/server.json" > "$twin/server.json"

  local tc=(docker compose -p "$project" -f "$twin/compose.yaml")
  echo "Standing up a disposable twin on ${twinbase}"
  VOPTRACK_HTTPS_PORT=$port VOPTRACK_HTTP_PORT="${VOPTRACK_TWIN_HTTP_PORT:-18080}" "${tc[@]}" up -d --wait >/dev/null 2>&1

  local pw; pw="$twin/.gate/admin-password"; openssl rand -base64 18 > "$pw"; chmod 600 "$pw"
  "${tc[@]}" stop tesserad >/dev/null 2>&1
  local run=(--rm --no-deps -v "$twin/.gate:/gate" -v "$pw:/tmp/pw:ro")
  local cfg=(-config /etc/tessera/server.json)
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person owner -grants read,write,schema,erase -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-full -grants read,write,schema -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-refused -grants write -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token gate-full -secret-output /gate/token >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token gate-refused -secret-output /gate/refused >/dev/null
  "${tc[@]}" start tesserad >/dev/null; sleep 3

  local ca; ca="$twin/ca.crt"
  "${tc[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"

  echo "Conformance gate against the twin"
  ( cd "$root" && go run ./cmd/verify -url "$twinbase" \
      -token "$(tr -d '\r\n' < "$twin/.gate/token")" \
      -refused-token "$(tr -d '\r\n' < "$twin/.gate/refused")" \
      -ca "$ca" -signin-user owner -signin-password-file "$pw" \
      -admin-user owner -admin-password-file "$pw" )
}

case "${1:-}" in
  init) init ;;
  up) up ;;
  verify) verify ;;
  down) down ;;
  *) echo "usage: scenario.sh {init|up|verify|down}" >&2; exit 2 ;;
esac
