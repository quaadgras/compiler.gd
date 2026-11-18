#!/bin/bash
# build.sh [dir]: make the site in dir (default dist/), with the
# playground (playground.html, playground/), and the playground's large
# files in r2/playground/, which download.compiler.gd (the R2 bucket
# compiler-gd) serves: the go command for GOOS=js (go.wasm, stripped and
# gzipped), what it reads of GOROOT, and a build cache of common packages
# (made by playground/prebuild.mjs, as the page's go command makes it),
# so that a first build is quick. The names are fixed (the bucket keeps
# only the latest); the manifest adds a hash of each to its URL.
# Needs bin/go built (src/make.bash) and node 24.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
out=${1:-$here/dist}
r2=$here/r2/playground
rm -rf "$out" "$r2"
mkdir -p "$out/playground" "$r2"
cp "$here"/playground/*.mjs "$out/playground/"
cp "$root"/lib/wasm/{wasm_gc.js,wasm_gc_js.js,wasm_gc_fs.js,wasm_gc_browser.js} "$out/playground/"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "go.wasm" >&2
(cd "$root/src" && GOROOT=$root GOOS=js GOARCH=wasm "$root/bin/go" build -ldflags='-s -w' -o "$tmp/go.wasm" ./cmd/go)

echo "goroot.tar.gz" >&2
(cd "$root" && {
	echo go.env
	[ -f VERSION ] && echo VERSION
	find pkg/include -type f
	find src -path src/cmd -prune -o -type d -name testdata -prune -o -type f \
		\( -name '*.go' -o -name '*.s' -o -name '*.h' -o -name go.mod -o -name go.sum -o -name modules.txt \) \
		! -name '*_test.go' -print
} | tar --format=ustar --owner=0 --group=0 --mtime=@0 -cf - -T - | gzip -9n > "$tmp/goroot.tar.gz")

echo "cache.tar.gz (prebuild)" >&2
NODE_OPTIONS= node --stack-size=4000 "$out/playground/prebuild.mjs" "$tmp/go.wasm" "$tmp/goroot.tar.gz" "$tmp/cache.tar" \
	bufio bytes container/heap container/list context encoding/base64 encoding/binary encoding/hex \
	encoding/json errors fmt hash/crc32 iter maps math math/big math/bits math/rand/v2 os regexp \
	slices sort strconv strings sync sync/atomic text/tabwriter time unicode unicode/utf8 unique internal/c23
gzip -9n "$tmp/cache.tar"

gzip -9n -c "$tmp/go.wasm" > "$r2/go.wasm.gz"
cp "$tmp/goroot.tar.gz" "$r2/goroot.tar.gz"
cp "$tmp/cache.tar.gz" "$r2/cache.tar.gz"
rm "$out"/playground/*.mjs "$out"/playground/*.js
"$here/pages.sh" "$out"
ls -l "$out" "$out/playground" >&2
