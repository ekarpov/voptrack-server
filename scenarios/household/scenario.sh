#!/usr/bin/env bash
# A household: one shared store, and a personal store each.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
. "$root/ci/select-image.sh"
domain="${VOPTRACK_DOMAIN:-localhost}"
port="${VOPTRACK_HTTPS_PORT:-10443}"
base="https://${domain}:${port}"
compose=(docker compose -p "${VOPTRACK_SCENARIO_PROJECT:-household}" -f "$here/compose.yaml")

# The server runs as whoever invoked this. See the comment in compose.yaml.
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

`encrypted_storage_confirmed` is an attestation by you, not a setting. On a
household server the volume holds your family's records, which is a stronger
reason to mean it than your own would be.

    VOPTRACK_STORAGE_ENCRYPTED=yes make scenario-household-up
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

# The dataset the server creates for itself becomes the SHARED store, and the
# owner administers from it. An administrator must hold erase on some dataset
# -- that is what AuthenticateAdministrator looks for -- so somebody in the
# household is entitled to destroy the shared store. That is a real property
# of this arrangement and not an oversight: pick the person you would trust
# with it.
provision_owner() {
  [ -f "$here/.state/owner-password" ] && return
  mkdir -p "$here/.state"; chmod 700 "$here/.state"
  openssl rand -base64 18 > "$here/.state/owner-password"
  chmod 600 "$here/.state/owner-password"
  "${compose[@]}" stop tesserad >/dev/null 2>&1
  local run=(--rm --no-deps -v "$here/.state:/state")
  "${compose[@]}" run --interactive=false -T "${run[@]}" tesserad -config /etc/tessera/server.json \
    -add-person owner -grants read,write,schema,erase -may-create-datasets \
    -password-file /state/owner-password >/dev/null
  "${compose[@]}" run --interactive=false -T --rm --no-deps -v "$here/.state:/state" tesserad \
    -config /etc/tessera/server.json -add-token owner -secret-output /state/owner-token >/dev/null
  "${compose[@]}" start tesserad >/dev/null
  sleep 3
  # Which dataset is the shared one, recorded rather than guessed later: once
  # personal stores exist, "the only one" stops identifying it.
  local ca; ca="$(mktemp)"
  "${compose[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"
  curl -sS --cacert "$ca" -H "Authorization: Bearer $(tr -d '\r\n' < "$here/.state/owner-token")" \
    "${base}/v1/datasets" | sed -n 's/.*"id":"\(s-[0-9a-f]*\)".*/\1/p' | head -1 > "$here/.state/shared-dataset"
  rm -f "$ca"
}

up() {
  init
  "${compose[@]}" up -d --wait 2>&1 | tail -3
  provision_owner
  cat <<EOF

Administration: ${base}/admin
  account   owner
  password  $here/.state/owner-password

Add people with:  make scenario-household-add-member NAME=alice
EOF
}

# A member gets two stores: read and write on the shared one, and a personal
# one of their own that nobody else can reach. Adding an existing person to a
# second dataset extends them rather than replacing them, so the two calls
# compose.
add_member() {
  local name="${1:-}"
  [ -n "$name" ] || { echo "usage: scenario.sh add-member NAME" >&2; return 2; }
  local shared; shared="$(cat "$here/.state/shared-dataset")"
  local token; token="$(tr -d '\r\n' < "$here/.state/owner-token")"
  local ca; ca="$(mktemp)"
  "${compose[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"

  local made personal
  made="$(curl -sS --cacert "$ca" -H "Authorization: Bearer ${token}" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen | tr 'A-Z' 'a-z')" \
    -d "{\"display_name\":\"${name} (personal)\"}" "${base}/v1/datasets")"
  rm -f "$ca"
  personal="$(printf '%s' "$made" | sed -n 's/.*"dataset_id":"\([^"]*\)".*/\1/p')"
  [ -n "$personal" ] || { echo "could not create a personal store for ${name}: $made" >&2; return 1; }

  openssl rand -base64 18 > "$here/.state/${name}-password"
  chmod 600 "$here/.state/${name}-password"
  "${compose[@]}" stop tesserad >/dev/null 2>&1
  local run=(--rm --no-deps -v "$here/.state:/state")
  # On the shared store: read and write, never erase or schema. Everyone who
  # writes there reads all of it -- that is what a shared store is -- so erase
  # stays with the owner and the types stay curated.
  "${compose[@]}" run --interactive=false -T "${run[@]}" tesserad -config /etc/tessera/server.json \
    -add-person "$name" -dataset "$shared" -grants read,write \
    -password-file "/state/${name}-password" >/dev/null
  # On their own: schema too, because it is theirs to shape.
  "${compose[@]}" run --interactive=false -T "${run[@]}" tesserad -config /etc/tessera/server.json \
    -add-person "$name" -dataset "$personal" -grants read,write,schema \
    -password-file "/state/${name}-password" >/dev/null
  "${compose[@]}" start tesserad >/dev/null; sleep 3
  echo "${name}: shared ${shared}, personal ${personal}"
  echo "  password $here/.state/${name}-password"
}

# Against a disposable twin, never this deployment: the suites write and
# clean nothing up, and here the records would be a family's.
verify() {
  local twin port project
  twin="$(mktemp -d)"; port="${VOPTRACK_TWIN_PORT:-20443}"; project="${VOPTRACK_TWIN_PROJECT:-voptrack-household-verify}"
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
  VOPTRACK_HTTPS_PORT=$port VOPTRACK_HTTP_PORT="${VOPTRACK_TWIN_HTTP_PORT:-20080}" "${tc[@]}" up -d --wait >/dev/null 2>&1

  local pw="$twin/.state/pw"; openssl rand -base64 18 > "$pw"; chmod 600 "$pw"
  "${tc[@]}" stop tesserad >/dev/null 2>&1
  local run=(--rm --no-deps -v "$twin/.state:/state")
  local cfg=(-config /etc/tessera/server.json)
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person owner -grants read,write,schema,erase -may-create-datasets -password-file /state/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-full -grants read,write,schema -password-file /state/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person gate-refused -grants write -password-file /state/pw >/dev/null
  # Two members of one household, both on the shared store.
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person member-a -grants read,write,schema -password-file /state/pw >/dev/null
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-person member-b -grants read,write -password-file /state/pw >/dev/null
  for who in gate-full gate-refused member-a member-b; do
    "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token "$who" -secret-output "/state/$who" >/dev/null
  done
  "${tc[@]}" run --interactive=false -T "${run[@]}" tesserad "${cfg[@]}" -add-token owner -secret-output /state/owner >/dev/null
  "${tc[@]}" start tesserad >/dev/null; sleep 3

  local ca="$twin/ca.crt"
  "${tc[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$ca"

  # A personal store, so isolation is checked against a real dataset the
  # caller may not use rather than against its own baseline. This is what
  # alpha.4 made possible: before it, no credential an operator could mint
  # was able to create a dataset at all.
  local made foreign
  made="$(curl -sS --cacert "$ca" -H "Authorization: Bearer $(tr -d '\r\n' < "$twin/.state/owner")" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen | tr 'A-Z' 'a-z')" \
    -d '{"display_name":"somebody personal"}' "${twinbase}/v1/datasets")"
  foreign="$(printf '%s' "$made" | sed -n 's/.*"dataset_id":"\([^"]*\)".*/\1/p')"
  [ -n "$foreign" ] || { echo "could not create a personal store for the isolation check: $made" >&2; return 1; }

  echo "Gate against the twin"
  ( cd "$root" && go run ./cmd/verify -url "$twinbase" \
      -token "$(tr -d '\r\n' < "$twin/.state/gate-full")" \
      -refused-token "$(tr -d '\r\n' < "$twin/.state/gate-refused")" \
      -shared-token "$(tr -d '\r\n' < "$twin/.state/member-b")" \
      -ca "$ca" -isolation -foreign-dataset "$foreign" \
      -signin-user member-a -signin-password-file "$pw" \
      -admin-user owner -admin-password-file "$pw" )
}

case "${1:-}" in
  init) init ;;
  up) up ;;
  add-member) shift; add_member "${1:-}" ;;
  verify) verify ;;
  down) "${compose[@]}" down -v 2>&1 | tail -2; rm -rf "$here/.state" ;;
  *) echo "usage: scenario.sh {init|up|add-member NAME|verify|down}" >&2; exit 2 ;;
esac
