#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$root"
go build -o "$work/tesserad" github.com/ekarpov/tessera/cmd/tesserad
go build -tags devfaults -o "$work/tesserad-devfaults" github.com/ekarpov/tessera/cmd/tesserad
expected=$(go list -m -f '{{.Version}}' github.com/ekarpov/tessera)
for binary in "$work/tesserad" "$work/tesserad-devfaults"; do
  actual=$("$binary" -version)
  [ "$actual" = "$expected" ] || { printf 'CLI version mismatch: expected %s, got %s\n' "$expected" "$actual" >&2; exit 1; }
done
printf 'CLI binaries: normal and devfaults report declared pin %s\n' "$expected"
go version -m "$work/tesserad"
go version -m "$work/tesserad-devfaults"
python3 "$root/release/native-settings/producer-recovery.py" "$work/tesserad" "$work/tesserad-devfaults"
