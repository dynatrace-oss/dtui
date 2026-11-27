#!/usr/bin/env bash
# Builds the dtui browser wasm (cmd/web) for the Chrome extension.
#
# Three upstream modules ship no js/wasm build support, so a plain
# `GOOS=js GOARCH=wasm go build ./cmd/web` fails to compile:
#   - charmbracelet/bubbletea : no TTY init/resize/suspend for js
#   - atotto/clipboard        : no readAll/writeAll for js
#   - Dynatrace/dtctl         : sdk/session/refresh_lock_unix.go (`!windows`)
#                               wrongly compiles on js and needs x/sys/unix;
#                               pkg/output/live_{unix,windows}.go define no
#                               resize hooks for js; pkg/usage/lock_unix.go
#                               has the same `!windows` flaw.
#
# We can't patch these in the module cache (`go build -overlay` forbids GOMODCACHE
# replacements, and the cache is read-only). So we copy just these four modules
# into wasm/.deps/ (gitignored), add the missing js build-tagged files, and wire
# them in through a SCOPED workspace file (wasm/go.work.wasm) selected via the
# GOWORK env var. Nothing here touches the repo's go.mod: the native build,
# `go build ./...`, `make test`, and CI are unaffected because they never set
# GOWORK and never see wasm/.deps.
#
# The real fix for the three dtctl gaps is to upstream `//go:build js` variants
# into dtctl (Dynatrace-owned). See docs/design/WASM_CHROME_EXTENSION.md.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/.." && pwd)"
patches="$here/patches"
deps="$here/.deps"
out="$here/ui"
mkdir -p "$out"

# GOWORK=off here, in mod_dir and in copy_mod: under an inherited GOWORK that
# names the workspace written below, a module resolves to its copy in wasm/.deps.
# No fallback for the Go version: go refuses a workspace that names an older
# one than go.mod, so a guess would only replace the real error by that one.
gover="$(cd "$repo" && GOWORK=off go list -m -f '{{.GoVersion}}')"

# The physical path of wasm/.deps, which need not exist yet.
deps_real="$(cd "$here" && pwd -P)/.deps"
if [ -d "$deps" ]; then
  deps_real="$(cd "$deps" && pwd -P)"
fi

# mod_dir <module-path> prints the directory go resolves the module to.
mod_dir() {
  (cd "$repo" && GOWORK=off go list -m -f '{{.Dir}}' "$1")
}

# copy_mod <module-path> <dest-name> finds the module's source and queues its
# copy into wasm/.deps/<dest-name>. Nothing is removed or copied until every
# module has a usable source, so a refusal leaves wasm/.deps as it was.
copy_srcs=()
copy_dests=()
copy_mod() {
  local mod="$1" dest="$deps/$2" src real
  # Look first and download only what is missing: offline, a module that is
  # extracted but whose zip has left the download cache cannot be downloaded
  # again, and needs no download. A module the cache knows only by its go.mod
  # has an empty Dir, which would turn the copy into a copy of the filesystem
  # root, so that one is extracted first.
  src="$(mod_dir "$mod" 2>/dev/null || true)"
  if [ -z "$src" ] || [ ! -f "$src/go.mod" ]; then
    (cd "$repo" && GOWORK=off go mod download "$mod")
    src="$(mod_dir "$mod")"
  fi
  if [ -z "$src" ] || [ ! -f "$src/go.mod" ]; then
    echo "error: no source for $mod in the module cache (go list reports '$src')" >&2
    exit 1
  fi
  # A replace (in go.mod, or in a -modfile named by GOFLAGS) that points the
  # module into wasm/.deps makes the source the directory the copy removes.
  real="$(cd "$src" && pwd -P)"
  case "$real/" in
    "$deps_real"/*)
      echo "error: $mod resolves to $real, inside $deps, which this script removes and rebuilds." >&2
      echo "       Drop the replace that points $mod there and run again." >&2
      exit 1
      ;;
  esac
  copy_srcs+=("$src")
  copy_dests+=("$dest")
}

echo "==> resolving module sources"
copy_mod github.com/charmbracelet/bubbletea bubbletea
copy_mod github.com/atotto/clipboard clipboard
copy_mod github.com/Dynatrace/dtctl dtctl
copy_mod github.com/Dynatrace/dtctl/sdk dtctl-sdk

echo "==> copying patched module sources into $deps"
for i in "${!copy_srcs[@]}"; do
  # A run interrupted before the chmod below leaves the module cache's
  # read-only directories behind, which rm cannot empty.
  [ ! -e "${copy_dests[$i]}" ] || chmod -R u+w "${copy_dests[$i]}"
  rm -rf "${copy_dests[$i]}"
  mkdir -p "${copy_dests[$i]}"
  cp -R "${copy_srcs[$i]}/." "${copy_dests[$i]}/"
  chmod -R u+w "${copy_dests[$i]}"
done

echo "==> applying js/wasm patches"
cp "$patches/bubbletea_tty_wasm.go.txt" "$deps/bubbletea/tty_wasm.go"
cp "$patches/clipboard_js.go.txt" "$deps/clipboard/clipboard_js.go"
# Retag the unix refresh-lock off js, and add the js no-op stub beside it.
sed -i.bak 's|^//go:build !windows$|//go:build !windows \&\& !js|' \
  "$deps/dtctl-sdk/session/refresh_lock_unix.go"
rm -f "$deps/dtctl-sdk/session/refresh_lock_unix.go.bak"
cp "$patches/dtctl_sdk_refresh_lock_js.go.txt" "$deps/dtctl-sdk/session/refresh_lock_js.go"
cp "$patches/dtctl_output_live_js.go.txt" "$deps/dtctl/pkg/output/live_js.go"
# Same retag for the usage-ledger lock (dtctl >= v0.43).
sed -i.bak 's|^//go:build !windows$|//go:build !windows \&\& !js|' \
  "$deps/dtctl/pkg/usage/lock_unix.go"
rm -f "$deps/dtctl/pkg/usage/lock_unix.go.bak"
cp "$patches/dtctl_usage_lock_js.go.txt" "$deps/dtctl/pkg/usage/lock_js.go"

echo "==> writing scoped workspace $here/go.work.wasm"
cat > "$here/go.work.wasm" <<WORK
go $gover

use ..

replace github.com/charmbracelet/bubbletea => ./.deps/bubbletea
replace github.com/atotto/clipboard => ./.deps/clipboard
replace github.com/Dynatrace/dtctl => ./.deps/dtctl
replace github.com/Dynatrace/dtctl/sdk => ./.deps/dtctl-sdk
WORK

echo "==> building dtui.wasm (GOOS=js GOARCH=wasm)"
(
  cd "$repo"
  GOWORK="$here/go.work.wasm" GOOS=js GOARCH=wasm go build -ldflags="-s -w" \
    -o "$out/dtui.wasm" github.com/dynatrace-oss/dtui/cmd/web
)
raw_size=$(wc -c < "$out/dtui.wasm")
echo "    build: $raw_size bytes ($((raw_size / 1024 / 1024)) MB)"

# Optional Binaryen pass — shrinks ~30-40%. A Chrome extension has no hard asset
# cap, but smaller is a faster first load. Skipped if wasm-opt is unavailable.
if command -v wasm-opt >/dev/null 2>&1; then
  echo "==> optimizing with wasm-opt -Oz"
  wasm-opt -Oz --enable-bulk-memory --enable-sign-ext \
    --enable-mutable-globals --enable-nontrapping-float-to-int \
    "$out/dtui.wasm" -o "$out/dtui.wasm"
  echo "    optimized: $(wc -c < "$out/dtui.wasm") bytes"
else
  echo "==> wasm-opt not found; skipping size optimization (install binaryen to shrink)"
fi

echo "==> copying wasm_exec.js from $(go env GOROOT)"
cp -f "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"

# The extension loads dtui.wasm from its own directory (chrome.runtime.getURL),
# so sync the freshly built artifacts there too. Both are gitignored. Skipping
# this is how a stale binary sneaks in — e.g. missing a newly wired feature.
ext="$repo/extension"
echo "==> syncing artifacts into $ext"
cp "$out/dtui.wasm" "$ext/dtui.wasm"
cp -f "$out/wasm_exec.js" "$ext/wasm_exec.js"

echo "==> done: $out/dtui.wasm + $out/wasm_exec.js (also copied into extension/)"
