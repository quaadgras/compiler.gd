#!/bin/bash
# pages.sh [dir]: put the pages into the site build.sh made in dir
# (default dist/), and the headers Pages serves them with.
#
# Every file is cached for good under a name of its contents, as
# compiler.gd's Cloudflare zone keeps a stylesheet or a script in the
# browser for 4 hours whatever Pages says: style.<hash>.css, and the
# playground's scripts and manifest in playground/v.<hash>/ (their
# imports are relative). The pages themselves are not kept.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
out=${1:-$here/dist}
pg=$out/playground
# The large files are on download.compiler.gd (FILES), made by build.sh
# in r2/playground; each URL has a hash of the file, so that a browser
# that has the old one asks for the new.
files=${FILES:-https://download.compiler.gd}
r2=$here/r2/playground
url() {
	[ -f "$r2/$1" ] || { echo "pages.sh: no $r2/$1 (run build.sh)" >&2; exit 1; }
	echo "$files/playground/$1?v=$(sha256sum "$r2/$1" | cut -c1-12)"
}
wasm=$(url go.wasm.gz)
goroot=$(url goroot.tar.gz)
cache=$(url cache.tar.gz)
mkdir -p "$pg"
rm -rf "$pg"/v.* "$pg"/*.mjs "$pg"/*.js "$pg"/manifest.json "$out"/*.html "$out"/style*.css

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$here"/playground/worker.mjs "$here"/playground/files.mjs "$tmp/"
cp "$root"/lib/wasm/{wasm_gc.js,wasm_gc_js.js,wasm_gc_fs.js,wasm_gc_browser.js} "$tmp/"
cat > "$tmp/manifest.json" <<JSON
{"go": "$wasm", "goroot": "$goroot", "cache": "$cache", "version": "$(cd "$root" && git rev-parse --short HEAD)"}
JSON
v=v.$(cat "$tmp"/* | sha256sum | cut -c1-12)
mkdir "$pg/$v"
cp "$tmp"/* "$pg/$v/"

css=style.$(sha256sum "$here/style.css" | cut -c1-12).css
cp "$here/style.css" "$out/$css"
for f in "$here"/*.html; do
	sed -e "s#href=\"style.css\"#href=\"$css\"#" -e "s#new Worker('playground/worker.mjs'#new Worker('playground/$v/worker.mjs'#" "$f" > "$out/$(basename "$f")"
done
grep -q "playground/$v/worker.mjs" "$out/playground.html" || { echo "pages.sh: playground.html does not start the worker it should" >&2; exit 1; }

cat > "$out/_redirects" <<REDIRECTS
/play /playground 301
REDIRECTS
cat > "$out/_headers" <<HEADERS
/playground/v.*
  Cache-Control: public, max-age=31536000, immutable
/style.*.css
  Cache-Control: public, max-age=31536000, immutable
HEADERS
echo "pages: $css, playground/$v" >&2
