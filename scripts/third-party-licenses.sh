#!/usr/bin/env bash
# Regenerates third_party/ — the license inventory and license texts for every
# module compiled into a release binary.
#
# The module set is the union of `go list -deps` over the exact GoReleaser
# targets (CGO_ENABLED=0, linux/darwin/windows x amd64/arm64), so OS-specific
# dependencies are covered. Texts come from the module cache; a module that
# ships no license file must have one in third_party/supplemental/ (with the
# provenance recorded in docs/dev/releasing.md), otherwise this script fails.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$repo/third_party"
texts="$out/licenses"
csv="$out/licenses.csv"
targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)

cd "$repo"
self="$(go list -m)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for target in "${targets[@]}"; do
  CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
    go list -deps -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' . |
    sort -u | rg -v '^$' | rg -v "^$self " |
    sed "s|\$| ${target}|" >>"$work/rows"
done

# classify <file> prints the SPDX ids whose canonical text appears in the file.
classify() {
  local file="$1"
  rg -q 'Apache License' "$file" && rg -q 'Version 2.0' "$file" && echo "Apache-2.0"
  rg -q 'Permission is hereby granted, free of charge' "$file" && echo "MIT"
  if rg -q 'Redistribution and use in source and binary forms' "$file"; then
    if rg -q 'Neither the name' "$file"; then echo "BSD-3-Clause"; else echo "BSD-2-Clause"; fi
  fi
  return 0
}

license_dir() {
  local module="$1" version="$2" dir
  dir="$(go list -m -f '{{.Dir}}' "$module@$version" 2>/dev/null || go list -m -f '{{.Dir}}' "$module")"
  if ls "$dir" | rg -qi '^(licen[cs]e|copying|notice)'; then echo "$dir"; return; fi
  [ -d "$out/supplemental/$module" ] || { echo "no license file for $module@$version and no supplemental text" >&2; exit 1; }
  echo "$out/supplemental/$module"
}

rm -rf "$texts"
mkdir -p "$texts"
echo "module,version,license,targets" >"$csv"

awk '{print $1" "$2}' "$work/rows" | sort -u | while read -r module version; do
  dir="$(license_dir "$module" "$version")"
  dest="$texts/$module"
  mkdir -p "$dest"
  ids=""
  for f in "$dir"/*; do
    name="$(basename "$f")"
    echo "$name" | rg -qi '^(licen[cs]e|copying|notice)' || continue
    [ -f "$f" ] || continue
    # dtctl's NOTICE lists dtctl's own (mostly unrelated) dependencies; only its
    # leading attribution applies to dtui and is reproduced in the root NOTICE.
    [ "$module/$name" = "github.com/dynatrace-oss/dtctl/NOTICE" ] && continue
    cp "$f" "$dest/$name"
    ids="$ids $(classify "$f")"
  done
  license="$(echo "$ids" | tr ' ' '\n' | rg -v '^$' | sort -u | paste -sd'|' - | sed 's/|/ AND /g')"
  [ -n "$license" ] || { echo "could not classify license of $module@$version" >&2; exit 1; }
  covering="$(rg "^$module $version " "$work/rows" | awk '{print $3}' | paste -sd' ' -)"
  echo "$module,$version,$license,$covering" >>"$csv"
done

# The release builds with the toolchain from go.mod (setup-go go-version-file),
# and distribution toolchains do not all ship Go's LICENSE, so it is supplemental.
mkdir -p "$texts/go-standard-library"
cp "$out/supplemental/go-standard-library/LICENSE" "$texts/go-standard-library/LICENSE"
echo "go-standard-library,$(go list -m -f '{{.GoVersion}}'),BSD-3-Clause,${targets[*]}" >>"$csv"
