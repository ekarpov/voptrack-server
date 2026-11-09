#!/bin/sh
# Collect the license files shipped by every Go module in the selected build list.
set -eu
out="${1:?license output directory required}"
mkdir -p "$out/go-toolchain"
cp /usr/local/go/LICENSE "$out/go-toolchain/LICENSE"
go version > "$out/go-toolchain/version.txt"
cp go.sum "$out/go.sum"
modules=$(go list -deps -f '{{with .Module}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}' github.com/ekarpov/tessera/cmd/tesserad ./cmd/...)
[ -n "$modules" ] || { echo 'license collection: empty module inventory' >&2; exit 1; }
modules=$(printf '%s\n' "$modules" | sort -u)
printf '%s\n' "$modules" | cut -d ' ' -f 1 > "$out/modules.txt"
printf '%s\n' "$modules" | while read -r name directory; do
  [ -n "$directory" ] || continue
  target="$out/$name"
  mkdir -p "$target"
  files=$(find "$directory" -maxdepth 2 -type f \( -iname 'license*' -o -iname 'notice*' -o -iname 'copying*' \))
  printf '%s\n' "$files" | while read -r file; do
    [ -n "$file" ] || continue
    relative="${file#"$directory"/}"
    mkdir -p "$target/$(dirname "$relative")"
    cp "$file" "$target/$relative"
  done
  [ -n "$(find "$target" -type f -print -quit)" ] || { echo "license collection: no notice for $name" >&2; exit 1; }
done
