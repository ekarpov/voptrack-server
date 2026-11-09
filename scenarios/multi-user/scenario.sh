#!/usr/bin/env bash
# Stand up, populate, verify and tear down the multi-user scenario.
#
# One server, one dataset per person, nobody sharing. The owner administers and
# adds people; each person sees their own dataset and cannot learn that anyone
# else's exists.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
. "$root/ci/select-image.sh"
domain="${VOPTRACK_DOMAIN:-localhost}"
port="${VOPTRACK_HTTPS_PORT:-9443}"
base="https://${domain}:${port}"
compose=(docker compose -p "${VOPTRACK_SCENARIO_PROJECT:-multi-user}" -f "$here/compose.yaml")

# The server runs as whoever invoked this. See the comment in compose.yaml:
# everything it reads or writes is bind-mounted and owned by this person.
export VOPTRACK_UID="$(id -u)" VOPTRACK_GID="$(id -g)"

# Compose must bind the port this script puts in the URL it prints and in the
# issuer. Exported rather than left to the caller: when they disagree the
# server is reachable and every address it hands out is wrong, which looks
# like a network fault.
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_HTTP_PORT:-$((port - 363))}"


init() {
  mkdir -p "$here/tls" "$here/.state/data"
  chmod 700 "$here/.state/data"
  if [ ! -f "$here/tls/server.crt" ]; then
    openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=tesserad" \
      -addext "subjectAltName=DNS:tesserad,DNS:localhost,IP:127.0.0.1" \
      -keyout "$here/tls/server.key" -out "$here/tls/server.crt" 2>/dev/null
    chmod 600 "$here/tls/server.key"
  fi
  if [ ! -f "$here/server.json" ] && [ "${VOPTRACK_STORAGE_ENCRYPTED:-}" != "yes" ]; then
    cat >&2 <<'WHY'
Refusing to write server.json.

`encrypted_storage_confirmed` is an attestation by you, not a setting: it does
not encrypt storage, it records that you have arranged for it. On a multi-user
server the records on that volume belong to other people.

Encrypt the volume, then:

    VOPTRACK_STORAGE_ENCRYPTED=yes make scenario-multi-user-up
WHY
    return 1
  fi
  [ -f "$here/server.json" ] || cat > "$here/server.json" <<JSON
{
  "listen": "0.0.0.0:8443",
  "issuer": "${base}",
  "data_dir": "/var/lib/tessera",
  "tls_certificate": "/etc/tessera/tls/server.crt",
  "tls_key": "/etc/tessera/tls/server.key",
  "engine": "sqlite",
  "content": true,
  "encrypted_storage_confirmed": true,
  "dataset_creation": true
}
JSON
}

# The owner holds the dataset this server creates for itself, and erase on it.
# Erase is what AuthenticateAdministrator looks for -- an administrator is a
# person who may erase somewhere -- so an owner with no dataset of their own
# cannot administer. That is a constraint of the protocol, not a choice here.
provision_owner() {
  [ -f "$here/.state/owner-password" ] && return
  mkdir -p "$here/.state"; chmod 700 "$here/.state"
  openssl rand -base64 18 > "$here/.state/owner-password"
  chmod 600 "$here/.state/owner-password"
  "${compose[@]}" stop tesserad >/dev/null 2>&1
  "${compose[@]}" run --interactive=false -T --rm --no-deps -v "$here/.state:/state" tesserad \
    -config /etc/tessera/server.json \
    -add-person owner -grants read,write,schema,erase -may-create-datasets \
    -password-file /state/owner-password >/dev/null
  "${compose[@]}" run --interactive=false -T --rm --no-deps -v "$here/.state:/state" tesserad \
    -config /etc/tessera/server.json -add-token owner -secret-output /state/owner-token >/dev/null
  "${compose[@]}" start tesserad >/dev/null
  sleep 3
}

up() {
  init
  "${compose[@]}" up -d --wait 2>&1 | tail -3
  provision_owner
  cat <<EOF

Administration: ${base}/admin
  account   owner
  password  $here/.state/owner-password

Add people with:  make scenario-multi-user-add-user NAME
EOF
}

# Create a dataset for this person, then provision them on it.
#
# Creation needs an authorization holding the server-level create_dataset
# right, which the owner is given at provisioning with -may-create-datasets.
# It is separate from erase deliberately: a long-lived credential that can
# destroy a dataset is a hazard, and creating destroys nothing.
add_user() {
  local name="${1:-}"
  [ -n "$name" ] || { echo "usage: scenario.sh add-user NAME" >&2; return 2; }
  local token; token="$(tr -d '\r\n' < "$here/.state/owner-token")"
  local ca; ca="$(mktemp)"
  "${compose[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"

  # The key must be a UUID. A malformed one is refused `invalid_identifier`,
  # which reads as though the dataset name were the problem.
  local key body status
  key="$(uuidgen | tr 'A-Z' 'a-z')"
  body="$(curl -sS -w '\n%{http_code}' --cacert "$ca" \
    -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${key}" -d "{\"display_name\":\"${name}\"}" \
    "${base}/v1/datasets")"
  status="$(printf '%s' "$body" | tail -1)"
  rm -f "$ca"
  [ "$status" = "201" ] || { echo "creating a dataset for ${name}: $status" >&2; printf '%s\n' "$body" | head -1 >&2; return 1; }

  local dataset; dataset="$(printf '%s' "$body" | head -1 | sed -n 's/.*"dataset_id":"\([^"]*\)".*/\1/p')"
  openssl rand -base64 18 > "$here/.state/${name}-password"
  chmod 600 "$here/.state/${name}-password"
  "${compose[@]}" stop tesserad >/dev/null 2>&1
  "${compose[@]}" run --interactive=false -T --rm --no-deps -v "$here/.state:/state" tesserad \
    -config /etc/tessera/server.json -add-person "$name" -dataset "$dataset" \
    -grants read,write,schema -password-file "/state/${name}-password" >/dev/null
  "${compose[@]}" start tesserad >/dev/null; sleep 3
  echo "${name}: dataset ${dataset}, password $here/.state/${name}-password"
}

# Remove somebody, which takes TWO actions in two different places because
# neither alone is enough. Verified on v0.1.0-alpha.4:
#
#   revoke their authorizations only  -> they sign in again and get fresh access
#   empty their dataset grants only   -> their existing tokens keep working (200)
#
# Revoking acts on an authorization, which is an installation rather than a
# person; the account's dataset membership is untouched and mints a new one on
# the next sign-in. Emptying the membership stops that, and does not reach
# credentials already issued. So: both, in this order, so there is no window
# where a revoked device can re-authorise itself.
#
# There is no delete. The account remains in the registry, able to
# authenticate and reaching nothing, and Tessera offers no way to remove it.
remove_user() {
  local name="${1:-}"
  [ -n "$name" ] || { echo "usage: scenario.sh remove-user NAME" >&2; return 2; }
  local ca; ca="$(mktemp)"
  "${compose[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"
  local pw; pw="$(cat "$here/.state/owner-password")"

  # Every live authorization of theirs, revoked. Each form spends a
  # single-use token, so the page is re-read for every one.
  local ids
  ids="$(curl -sS --cacert "$ca" -u "owner:${pw}" "${base}/admin/authorizations" | python3 -c "
import re, sys
page = sys.stdin.read()
for row in re.split(r'<tr[^>]*>', page):
    if re.search(r'>\s*$name\s*<', row):
        m = re.search(r'name=\"authorization\"\s+value=\"([^\"]+)\"', row)
        if m: print(m.group(1))
")"
  local id
  for id in $ids; do
    local csrf
    csrf="$(curl -sS --cacert "$ca" -u "owner:${pw}" "${base}/admin/authorizations" \
      | sed -n 's/.*name="csrf" value="\([^"]*\)".*/\1/p' | head -1)"
    curl -sS -o /dev/null --cacert "$ca" -u "owner:${pw}" -X POST \
      -d "csrf=${csrf}" -d "action=revoke" -d "authorization=${id}" \
      "${base}/admin/authorizations"
  done

  # Their dataset, by the name add-user gave it, and their grants on it
  # emptied. -add-person with no grants is how membership is removed; there is
  # no -remove-person, and this is the only route.
  local dataset
  dataset="$(curl -sS --cacert "$ca" -H "Authorization: Bearer $(tr -d '\r\n' < "$here/.state/owner-token")" \
    "${base}/v1/datasets" | python3 -c "
import json, sys
for d in json.load(sys.stdin)['datasets']:
    if d['display_name'] == '$name': print(d['id'])
")"
  rm -f "$ca"
  [ -n "$dataset" ] || { echo "no dataset named ${name}; nothing to remove them from" >&2; return 1; }

  "${compose[@]}" stop tesserad >/dev/null 2>&1
  "${compose[@]}" run --interactive=false -T --rm --no-deps -v "$here/.state:/state" tesserad \
    -config /etc/tessera/server.json -add-person "$name" -dataset "$dataset" \
    -grants "" -password-file "/state/${name}-password" >/dev/null
  "${compose[@]}" start tesserad >/dev/null; sleep 3
  echo "${name}: authorizations revoked, grants on ${dataset} emptied"
  echo "  the account still exists and can still authenticate; it reaches nothing"
}

# Against a disposable twin, never this deployment: Tessera's suites define
# types, write records and upload content and remove none of it, so pointing
# them at a server holding other people's records would damage it.
verify() {
  local twin port project
  twin="$(mktemp -d)"; port="${VOPTRACK_TWIN_PORT:-19443}"; project="${VOPTRACK_TWIN_PROJECT:-voptrack-multi-user-verify}"
  trap 'docker compose -p "$project" -f "$twin/compose.yaml" down -v >/dev/null 2>&1; rm -rf "$twin"' RETURN

  cp "$here/Caddyfile" "$here/compose.yaml" "$twin/"
  mkdir -p "$twin/tls" "$twin/.state/data"; chmod 700 "$twin/.state" "$twin/.state/data"
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=tesserad" \
    -addext "subjectAltName=DNS:tesserad,DNS:localhost,IP:127.0.0.1" \
    -keyout "$twin/tls/server.key" -out "$twin/tls/server.crt" 2>/dev/null
  local twinbase="https://localhost:${port}"
  sed "s|\"issuer\": \".*\"|\"issuer\": \"${twinbase}\"|" "$here/server.json" > "$twin/server.json"

  local tc=(docker compose -p "$project" -f "$twin/compose.yaml")
  echo "Standing up a disposable twin on ${twinbase}"
  VOPTRACK_HTTPS_PORT=$port VOPTRACK_HTTP_PORT="${VOPTRACK_TWIN_HTTP_PORT:-19080}" "${tc[@]}" up -d --wait >/dev/null 2>&1

  local pw="$twin/.state/owner-password"; openssl rand -base64 18 > "$pw"; chmod 600 "$pw"
  "${tc[@]}" stop tesserad >/dev/null 2>&1
  local run=(--rm --no-deps -v "$twin/.state:/state" -v "$pw:/tmp/pw:ro")
  local cfg=(-config /etc/tessera/server.json)
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person owner -grants read,write,schema,erase -may-create-datasets -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-full -grants read,write,schema -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-refused -grants write -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token gate-full -secret-output /state/token >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token gate-refused -secret-output /state/refused >/dev/null
  # Somebody who was given access and then had it taken away. The gate checks
  # they reach nothing; the credential half of offboarding -- revoking live
  # authorizations -- is exercised by `remove-user` rather than here, because
  # this person never signed in and so has none.
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-removed -grants read,write -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-removed -grants "" -password-file /tmp/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token owner -secret-output /state/owner >/dev/null
  "${tc[@]}" start tesserad >/dev/null; sleep 3

  local ca="$twin/ca.crt"
  "${tc[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"

  # A second person's dataset, so isolation is checked against a real one
  # rather than against its own baseline. Until alpha.4 this could not be
  # done -- no credential an operator could mint was able to create a
  # dataset -- so the comparison that matters had never executed.
  local made foreign
  made="$(curl -sS --cacert "$ca" -H "Authorization: Bearer $(tr -d '\r\n' < "$twin/.state/owner")" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen | tr 'A-Z' 'a-z')" \
    -d '{"display_name":"someone else"}' "${twinbase}/v1/datasets")"
  foreign="$(printf '%s' "$made" | sed -n 's/.*"dataset_id":"\([^"]*\)".*/\1/p')"
  [ -n "$foreign" ] || { echo "could not create a second dataset for the isolation check: $made" >&2; return 1; }

  # -isolation without -foreign-dataset checks the baseline this rests on: an
  # identity naming nothing answers 404 unavailable. A second user's dataset is
  # compared against that once creation is possible.
  echo "Gate against the twin"
  ( cd "$root" && go run ./cmd/verify -url "$twinbase" \
      -token "$(tr -d '\r\n' < "$twin/.state/token")" \
      -refused-token "$(tr -d '\r\n' < "$twin/.state/refused")" \
      -ca "$ca" -isolation -foreign-dataset "$foreign" \
      -signin-user gate-full -signin-password-file "$pw" \
      -offboarded-user gate-removed -offboarded-password-file "$pw" \
      -admin-user owner -admin-password-file "$pw" )
}

case "${1:-}" in
  init) init ;;
  up) up ;;
  add-user) shift; add_user "${1:-}" ;;
  remove-user) shift; remove_user "${1:-}" ;;
  verify) verify ;;
  down) "${compose[@]}" down -v 2>&1 | tail -2; rm -rf "$here/.state" ;;
  *) echo "usage: scenario.sh {init|up|add-user NAME|remove-user NAME|verify|down}" >&2; exit 2 ;;
esac
