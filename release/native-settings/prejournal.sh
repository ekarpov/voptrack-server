#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
project=voptrack-alpha36-prejournal
export VOPTRACK_GATE_WORK="$work" VOPTRACK_GATE_PROJECT="$project"
export VOPTRACK_CANDIDATE_IMAGE="${VOPTRACK_IMAGE:?select immutable candidate}"
legacy=sha256:44144687147636315f19fdbb1bd5747c51a7e8c933ba603d3bd6223e90fb57ba
[ "$(docker run --rm --entrypoint /usr/local/bin/tesserad "$legacy" -version)" = v0.1.0-alpha.35 ]
export VOPTRACK_IMAGE="$legacy" VOPTRACK_VARIANT=household VOPTRACK_STORAGE_ENCRYPTED=yes
export VOPTRACK_PUBLISH_PREFIX=127.0.0.1: VOPTRACK_HTTPS_PORT=30743 VOPTRACK_HTTP_PORT=30780 VOPTRACK_CONSOLE_PORT=30744 VOPTRACK_PUBLIC_URL=https://localhost:30743
export VOPTRACK_PROXY_SUBNET=10.117.93.0/24 VOPTRACK_PROXY_POOL=10.117.93.128/25 VOPTRACK_PROXY_ADDRESS=10.117.93.2
cp "$root/release/compose.yaml" "$work/compose.yaml"
dc(){ (cd "$work" && docker compose -p "$project" "$@"); }
cleanup(){ docker rm -f "$project-bootstrap" >/dev/null 2>&1 || true; dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
dc up -d --wait >/dev/null 2>&1
dc run --rm -T init password > "$work/owner" 2>/dev/null
chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
python3 "$root/release/native-settings/prejournal.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner"
