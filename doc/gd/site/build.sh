#!/bin/bash
# build.sh [dir]: make the site in dir (default dist/), with the
# playground (playground.html, playground/): the go command for GOOS=js (go.wasm,
# stripped and gzipped: under Pages' 25 MiB a file), what it reads of GOROOT,
# and a build cache of common packages (made by playground/prebuild.mjs, as the
# page's go command makes it), so that a first build is quick.
# Needs bin/go built (src/make.bash) and node 24.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
out=${1:-$here/dist}
rm -rf "$out"
mkdir -p "$out/playground"
cp "$here"/*.html "$here"/style.css "$out/"
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
	slices sort strconv strings sync sync/atomic text/tabwriter time unicode unicode/utf8 unique
gzip -9n "$tmp/cache.tar"

# Names with a hash of the contents, so the files can be cached forever.
name() { echo "$1.$(sha256sum "$2" | cut -c1-12)$3"; }
gzip -9n -c "$tmp/go.wasm" > "$tmp/go.wasm.gz"
wasm=$(name go "$tmp/go.wasm.gz" .wasm.gz)
if [ "$(stat -c %s "$tmp/go.wasm.gz")" -ge $((25 << 20)) ]; then
	echo "go.wasm.gz is over 25 MiB, the limit of a file on Pages" >&2
	exit 1
fi
cp "$tmp/go.wasm.gz" "$out/playground/$wasm"
goroot=$(name goroot "$tmp/goroot.tar.gz" .tar.gz)
cache=$(name cache "$tmp/cache.tar.gz" .tar.gz)
cp "$tmp/goroot.tar.gz" "$out/playground/$goroot"
cp "$tmp/cache.tar.gz" "$out/playground/$cache"
rm "$out/playground/prebuild.mjs"
cat > "$out/playground/manifest.json" <<JSON
{"go": "$wasm", "goSize": $(stat -c %s "$tmp/go.wasm"), "goroot": "$goroot", "cache": "$cache",
 "version": "$(cd "$root" && git rev-parse --short HEAD)"}
JSON
cat > "$out/_redirects" <<REDIRECTS
/play /playground 301
REDIRECTS
cat > "$out/_headers" <<HEADERS
/playground/*.gz
  Cache-Control: public, max-age=31536000, immutable
/playground/manifest.json
  Cache-Control: no-cache
HEADERS
ls -l "$out" "$out/playground" >&2
