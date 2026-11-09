#!/usr/bin/env bash
# Build and publish the release image, by hand: there is no hosted CI.
#
#   ci/release-image.sh IMAGE:TAG [--push]      (make release-image IMAGE=…)
#
# Without --push it loads one platform into the local Docker store.
# With --push it publishes the selected platforms to IMAGE:TAG.
# Either way it refuses a commit that local CI has not passed (make ci or make
# ci-remote), so what is published is what was gated. Logging in to the
# registry is the operator's, beforehand.
#
# Platforms: linux/amd64 for servers and most home machines, linux/arm64 for
# small ones (a Raspberry Pi, an ARM mini PC). The image compiles SQLite with
# cgo, so each platform is compiled natively under BuildKit -- the one that is
# not this machine's runs under emulation, which is slow but needs nothing set up
# on Docker Desktop.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
image="${1:?usage: ci/release-image.sh IMAGE:TAG [--push]}"
push="${2:-}"
platforms="${VOPTRACK_PLATFORMS:-linux/amd64}"
product="${VOPTRACK_PRODUCT:-standard}"
case "$product" in home) variant=household;; research) variant=research;; standard) variant=standard;; *) echo 'release-image: product must be home, research or standard' >&2; exit 2;; esac
if [ "$push" != --push ] && [[ "$platforms" == *,* ]]; then
  echo 'release-image: local preparation loads one platform; choose one with VOPTRACK_PLATFORMS' >&2; exit 2
fi
case "$push" in ''|--push) ;; *) echo "release-image: unknown argument" >&2; exit 2 ;; esac
[ "$#" -le 2 ] || { echo "release-image: too many arguments" >&2; exit 2; }
case "$image" in *:*) ;; *) echo "release-image: name a tag, e.g. registry/voptrack-server:v0.1.0" >&2; exit 2 ;; esac
case "$image" in *:latest) echo "release-image: publish a version, not latest" >&2; exit 2 ;; esac

status="$(git status --porcelain)" || exit 2
[ -z "$status" ] || { echo "release-image: the working tree has changes; commit them and run CI first" >&2; exit 2; }
sha="$(git rev-parse --verify HEAD)"
python3 "$root/ci/qualification.py" "$sha"


proxy="$(mktemp -d)"
builder="voptrack-release-$$"
cleanup() { docker buildx rm "$builder" >/dev/null 2>&1 || true; rm -rf "$proxy"; }
trap cleanup EXIT
./deploy/modproxy.sh "$proxy/modules" >/dev/null
# A builder of its own: the default one cannot build several platforms at once.
docker buildx create --name "$builder" --driver docker-container >/dev/null

args=(--builder "$builder" --platform "$platforms" --build-context modproxy="$proxy/modules"
      -f deploy/Containerfile --label "org.opencontainers.image.revision=$sha"
      --label "org.opencontainers.image.source=https://github.com/ekarpov/voptrack-server"
      --label "org.opencontainers.image.version=${image##*:}"
      --build-arg "VOPTRACK_DEFAULT_VARIANT=$variant"
      --label "app.voptrack.product=$product"
      --metadata-file "$proxy/build-metadata.json" -t "$image")
if [ "$push" = --push ]; then
  docker buildx build "${args[@]}" --push .
  echo "release-image: published $image ($platforms) from $(git rev-parse --short "$sha")"
  digest="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["containerimage.digest"])' "$proxy/build-metadata.json")"
  echo "release-image: immutable reference ${image%:*}@${digest}"
else
  docker buildx build "${args[@]}" --load .
  actual="$(docker run --rm --network none --tmpfs /var/lib/tessera --entrypoint /usr/local/bin/tesserad "$image" -version)"
  expected="$(python3 "$root/ci/tessera_pin.py")"
  [ "$actual" = "$expected" ] || { echo 'release-image: loaded Tessera version mismatch' >&2; exit 1; }
  echo "release-image: prepared $image ($platforms, $product) from $(git rev-parse --short "$sha"); nothing published"
  docker image inspect --format '{{.Id}}' "$image"
fi
