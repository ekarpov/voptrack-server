#!/usr/bin/env bash
# Build a local Go module proxy holding exactly what the image build needs, so
# `docker build` fetches nothing and needs no key.
#
#   deploy/modproxy.sh DIR
#
# The modules come from this machine's own Go cache, which `go` filled with its
# own credentials when this repository was first built. The image build then
# reads them through a BuildKit build context (see `make image`) instead of
# cloning a private repository from GitHub over SSH. That removes the reason
# builds ever needed a key -- and with it the temptation to load one into the
# ssh-agent every other process on the machine shares, which is how a build
# once made every other session's git authenticate as a read-only deploy key.
#
# Hashes are still checked: go.sum travels with the build and the proxy is a
# plain file layout go reads like any other.
set -euo pipefail

out="${1:?usage: modproxy.sh DIR}"
root="$(cd "$(dirname "$0")/.." && pwd)"
cache="$(go env GOMODCACHE)/cache/download"
mkdir -p "$out"

cd "$root"
# Make sure the cache holds everything; a no-op when it already does. This is
# the one step that may reach the network, with the user's own git setup.
go mod download

# .mod and .info for every module in the build list, because resolving the
# graph reads them; .zip only for modules that provide a package the build
# compiles, which is far fewer.
copy() { local rel="$1"; mkdir -p "$out/$(dirname "$rel")"; cp -p "$cache/$rel" "$out/$rel"; }
go mod download -json all | python3 -c '
import json, os, sys
cache = sys.argv[1]
text, dec, i = sys.stdin.read(), json.JSONDecoder(), 0
while True:
    while i < len(text) and text[i].isspace():
        i += 1
    if i >= len(text):
        break
    obj, i = dec.raw_decode(text, i)
    for key in ("Info", "GoMod"):
        if obj.get(key):
            print(os.path.relpath(obj[key], cache))
' "$cache" > "$out/.list"
while read -r rel; do copy "$rel"; done < "$out/.list"

needed=$(go list -deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' \
  github.com/ekarpov/tessera/cmd/tesserad ./cmd/console ./cmd/backupreplicator | sort -u | grep -v '@$' || true)
for mv in $needed; do
  zip=$(go mod download -json "$mv" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("Zip",""))')
  [ -n "$zip" ] && copy "${zip#$cache/}"
done
rm -f "$out/.list"
echo "module proxy: $(du -sh "$out" | cut -f1) in $out"
