#!/usr/bin/env bash
# A Research server on this machine for the app's integration tests: the real
# release stack (Tessera, the Console, Caddy) on https://localhost:8444, with a
# synthetic study designed in the Console and one participant joined.
#
#   dev.sh up      start it (idempotent: an existing one is kept) and seed it once
#   dev.sh token   sign the participant in again and rewrite their tokens --
#                  refresh tokens rotate, so whoever refreshes must keep the new
#                  one, and this replaces a spent one
#   dev.sh down    stop it and delete its data
#
# Everything the app needs lands in .voptrack-research-dev/, which is not
# committed: ca.crt (the server's own local CA root, whose pin is tls_pin; trust it in the simulator with
#   xcrun simctl keychain booted add-root-cert .voptrack-research-dev/ca.crt)
# and participant.json (account, password, store, OAuth client, tokens) --
# the one to record with -- and participant-2.json, the same for a second
# participant whose store a withdrawal test may erase.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
# Overridable so local CI can run a disposable twin beside the app team's
# server: its own project, state, ports and network, never this one's.
state="${VOPTRACK_RESEARCH_DEV_STATE:-$root/.voptrack-research-dev}"
project="${VOPTRACK_RESEARCH_DEV_PROJECT:-voptrack-research-dev}"
port="${VOPTRACK_RESEARCH_DEV_PORT:-8444}"
dc() { docker compose -p "$project" -f "$root/release/compose.yaml" "$@"; }
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_RESEARCH_DEV_HTTP_PORT:-8480}"
# A display name, so the app shows one beside the address (Tessera alpha.28).
export VOPTRACK_NAME="${VOPTRACK_NAME:-Research dev server}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_VARIANT=research
# Its own network, so the gates -- which use the compose default -- can run
# while it is up.
net="${VOPTRACK_RESEARCH_DEV_NET:-10.117.48}"
export VOPTRACK_PROXY_SUBNET=$net.0/24 VOPTRACK_PROXY_ADDRESS=$net.2 VOPTRACK_PROXY_POOL=$net.128/25
export VOPTRACK_CONSOLE_PORT="${VOPTRACK_RESEARCH_DEV_CONSOLE_PORT:-9444}" # its own, beside a gate's
case "${1:-up}" in
  down)
    dc down -v --remove-orphans
    rm -rf "$state"
    echo "research-dev: stopped, data deleted" ;;
  up)
    # Init takes the exclusive maintenance guard, including on repeated up.
    dc stop console tesserad >/dev/null 2>&1 || { echo "dev: could not stop both producers" >&2; exit 1; }
    dc up -d --wait >/dev/null 2>&1 || { dc logs --tail 20 >&2; echo "research-dev: did not become healthy" >&2; exit 1; }
    # Compose does not recreate Caddy when only its inline Caddyfile changed,
    # so a dev server would keep serving the old routes; recreating it is cheap.
    dc up -d --no-deps --force-recreate --wait caddy >/dev/null 2>&1 || { echo "research-dev: Caddy did not come back" >&2; exit 1; }
    mkdir -p "$state" && chmod 700 "$state"
    dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$state/ca.crt"
    dc exec -T caddy cat /var/lib/voptrack-public/tls-pin > "$state/tls-pin"
    if [ ! -f "$state/participant.json" ]; then
      dc run --rm -T init password > "$state/owner" 2>/dev/null; chmod 600 "$state/owner"
      python3 "$root/release/research/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$state/ca.crt" "$state/owner" "$state"
    elif [ ! -f "$state/participant-2.json" ]; then
      python3 "$root/release/research/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$state/ca.crt" "$state/owner" "$state" join
    fi
    echo "research-dev: $VOPTRACK_PUBLIC_URL -- Console: $VOPTRACK_PUBLIC_URL/ (researcher: owner, password in $state/owner)"
    echo "research-dev: the app's files are in $state" ;;
  token)
    [ -f "$state/participant.json" ] || { echo "research-dev: not seeded yet -- run dev.sh up" >&2; exit 1; }
    python3 "$root/release/research/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$state/ca.crt" "$state/owner" "$state" token ;;
  *) echo "usage: dev.sh up|token|down" >&2; exit 2 ;;
esac
