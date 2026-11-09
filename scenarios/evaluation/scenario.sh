#!/usr/bin/env bash
# Stand up a throwaway Voptrack server with something in it, and destroy it.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
. "$root/ci/select-image.sh"
port="${VOPTRACK_TRY_PORT:-7443}"
base="https://localhost:${port}"
compose=(docker compose -p "${VOPTRACK_SCENARIO_PROJECT:-evaluation}" -f "$here/compose.yaml")

# The server runs as whoever invoked this. See the comment in compose.yaml:
# everything it reads or writes is bind-mounted and owned by this person.
export VOPTRACK_UID="$(id -u)" VOPTRACK_GID="$(id -g)"


up() {
  mkdir -p "$here/.try/data"; chmod 700 "$here/.try" "$here/.try/data"
  [ -f "$here/.try/server.crt" ] || openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=tesserad" -addext "subjectAltName=DNS:tesserad,DNS:localhost,IP:127.0.0.1" \
    -keyout "$here/.try/server.key" -out "$here/.try/server.crt" 2>/dev/null
  chmod 600 "$here/.try/server.key"

  # `encrypted_storage_confirmed` is an attestation that storage is encrypted,
  # and it is set here without asking. That is defensible for exactly this
  # scenario and nowhere else: nothing real ever enters this volume, `down`
  # destroys it, and the flag exists to protect records that here do not
  # exist. The single-user and multi-user scenarios make you state it,
  # because there the records are yours or somebody else's.
  [ -f "$here/.try/server.json" ] || cat > "$here/.try/server.json" <<JSON
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

  VOPTRACK_HTTPS_PORT=$port VOPTRACK_HTTP_PORT="${VOPTRACK_TRY_HTTP_PORT:-$((port - 363))}" "${compose[@]}" up -d --wait >/dev/null 2>&1

  if [ ! -f "$here/.try/password" ]; then
    openssl rand -base64 18 > "$here/.try/password"; chmod 600 "$here/.try/password"
    "${compose[@]}" stop tesserad >/dev/null 2>&1
    local run=(--rm --no-deps -v "$here/.try:/try")
    "${compose[@]}" run --interactive=false -T "${run[@]}" tesserad -config /etc/tessera/server.json \
      -add-person you -grants read,write,schema,erase -password-file /try/password >/dev/null
    "${compose[@]}" run --interactive=false -T "${run[@]}" tesserad -config /etc/tessera/server.json \
      -add-token you -secret-output /try/token >/dev/null
    "${compose[@]}" start tesserad >/dev/null; sleep 3
  fi

  "${compose[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt </dev/null > "$here/.try/ca.crt"
  python3 "$here/seed.py" "$base" "$(tr -d '\r\n' < "$here/.try/token")" "$here/.try/ca.crt"

  cat <<EOF

  Open   ${base}/admin
  Sign in as  you
  Password in $here/.try/password

Your browser will warn about the certificate. That is correct — it is one
Caddy issued to itself for localhost, and nothing has vouched for it. For a
throwaway on your own machine, accept it; for anything else, do not.

  make try-down   destroys all of this, volume included
EOF
}

case "${1:-}" in
  up) up ;;
  down) "${compose[@]}" down -v >/dev/null 2>&1; rm -rf "$here/.try"; echo "destroyed" ;;
  *) echo "usage: scenario.sh {up|down}" >&2; exit 2 ;;
esac
