#!/usr/bin/env bash
# A Home server on this machine for the app's integration tests: the real
# release stack (Tessera, the Console, Caddy) on https://localhost:8544, with a
# household set up through the Console -- the owner, Sara, Ben and Lina; Sara
# and Ben record the inputs of specs/vectors/home-aggregation-v1.json and share
# Spending, not Weight; Lina shares nothing and is suspended.
#
#   dev.sh up      start it (idempotent: an existing one is kept) and seed it once
#   dev.sh token   sign every member in again and rewrite their tokens --
#                  refresh tokens rotate, so whoever refreshes must keep the new
#                  one, and this replaces a spent one (Lina, suspended, is kept)
#   dev.sh down    stop it and delete its data
#
# Everything the app needs lands in .voptrack-home-dev/, which is not
# committed: ca.crt (the server's own local CA root, whose pin is tls_pin; trust it in the simulator with
#   xcrun simctl keychain booted add-root-cert .voptrack-home-dev/ca.crt)
# and home.json (each member's account, password, recovery code, store, OAuth
# client and tokens; who shares what; who is suspended; the household answers).
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
# Overridable so local CI can run a disposable twin beside the app team's
# server: its own project, state, ports and network, never this one's.
state="${VOPTRACK_HOME_DEV_STATE:-$root/.voptrack-home-dev}"
project="${VOPTRACK_HOME_DEV_PROJECT:-voptrack-home-dev}"
port="${VOPTRACK_HOME_DEV_PORT:-8544}"
dc() { docker compose -p "$project" -f "$root/release/compose.yaml" "$@"; }
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_HOME_DEV_HTTP_PORT:-8580}"
# A display name, so the app shows one beside the address (Tessera alpha.28).
export VOPTRACK_NAME="${VOPTRACK_NAME:-Home dev server · Дом}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_VARIANT=household
# Its own network, so the gates -- which use the compose default -- can run
# while it is up.
net="${VOPTRACK_HOME_DEV_NET:-10.117.49}"
export VOPTRACK_PROXY_SUBNET=$net.0/24 VOPTRACK_PROXY_ADDRESS=$net.2 VOPTRACK_PROXY_POOL=$net.128/25
export VOPTRACK_CONSOLE_PORT="${VOPTRACK_HOME_DEV_CONSOLE_PORT:-9445}" # its own, beside a gate's
# The release shape (make home-app-dev): no web interface; seeded as the app
# would, through Tessera alone.
export VOPTRACK_CONSOLE="${VOPTRACK_HOME_DEV_CONSOLE:-on}"
case "${1:-up}" in
  down)
    dc down -v --remove-orphans
    rm -rf "$state"
    echo "home-dev: stopped, data deleted" ;;
  up)
    # Init takes the exclusive maintenance guard, including on repeated up.
    dc stop console tesserad >/dev/null 2>&1 || { echo "dev: could not stop both producers" >&2; exit 1; }
    dc up -d --wait >/dev/null 2>&1 || { dc logs --tail 20 >&2; echo "home-dev: did not become healthy" >&2; exit 1; }
    # Compose does not recreate Caddy when only its inline Caddyfile changed,
    # so a dev server would keep serving the old routes; recreating it is cheap.
    dc up -d --no-deps --force-recreate --wait caddy >/dev/null 2>&1 || { echo "home-dev: Caddy did not come back" >&2; exit 1; }
    mkdir -p "$state" && chmod 700 "$state"
    dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$state/ca.crt"
    dc exec -T caddy cat /var/lib/voptrack-public/tls-pin > "$state/tls-pin"
    if [ ! -f "$state/home.json" ]; then
      dc run --rm -T init password > "$state/owner" 2>/dev/null; chmod 600 "$state/owner"
      python3 "$root/release/household/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$state/ca.crt" "$state/owner" "$state"
      "$0" links
    fi
    if [ "$VOPTRACK_CONSOLE" = off ]; then echo "home-dev: $VOPTRACK_PUBLIC_URL -- no web interface, the app does everything (owner: password in $state/owner)"; else echo "home-dev: $VOPTRACK_PUBLIC_URL -- Console: $VOPTRACK_PUBLIC_URL/ (owner: password in $state/owner)"; fi
    echo "home-dev: the app's files are in $state" ;;
  links)
    # Fresh single-use links for the app's join tests, written into home.json:
    # an unused member invitation, and the owner's device link from the host
    # (which signs out every device the owner had -- both producers stop for it).
    [ -f "$state/home.json" ] || { echo "home-dev: not seeded yet -- run dev.sh up" >&2; exit 1; }
    # The owner's link first: owner-device signs out the owner's devices, and
    # Tessera then cancels the unused invitations those devices issued -- so an
    # invitation made before it would be dead on arrival.
    dc stop console tesserad >/dev/null 2>&1 || { echo "home-dev: could not stop both producers" >&2; exit 1; }
    running="$(dc ps --status running -q console tesserad)" || { echo "home-dev: could not check producer state" >&2; exit 1; }
    [ -z "$running" ] || { echo "home-dev: a producer is still running" >&2; exit 1; }
    owner_link="$(dc run --rm -T --no-deps init owner-device 2>/dev/null)" \
      || { echo "home-dev: owner-device failed; services remain stopped" >&2; exit 1; }
    [ -n "$owner_link" ] || { echo "home-dev: no owner device link; services remain stopped" >&2; exit 1; }
    dc up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || { echo "home-dev: did not come back after owner-device" >&2; exit 1; }
    invitation="$(python3 "$root/release/household/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$state/ca.crt" "$state/owner" "$state" invite)" \
      || { echo "home-dev: could not issue a member invitation" >&2; exit 1; }
    python3 - "$state/home.json" "$invitation" "$owner_link" <<'PY'
import json, os, sys
path, invitation, owner = sys.argv[1:4]
doc = json.load(open(path))
doc["member_invitation"] = invitation  # unused, single use; redeem on a device or in the browser
doc["owner_device_link"] = owner       # single use, 24 hours; every earlier owner device is signed out
with open(os.open(path, os.O_WRONLY | os.O_TRUNC), "w") as f:
    json.dump(doc, f, indent=2)
PY
    echo "home-dev: fresh member invitation and owner device link in $state/home.json" ;;
  token)
    [ -f "$state/home.json" ] || { echo "home-dev: not seeded yet -- run dev.sh up" >&2; exit 1; }
    python3 "$root/release/household/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$state/ca.crt" "$state/owner" "$state" token ;;
  *) echo "usage: dev.sh up|token|links|down" >&2; exit 2 ;;
esac
