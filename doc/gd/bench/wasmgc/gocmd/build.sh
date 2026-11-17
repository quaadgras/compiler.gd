#!/bin/bash
# build.sh: make out/go.wasm (the go command for GOOS=js) and
# out/goroot.tar.gz (what it reads of GOROOT to build programs: std's
# source, go.env, pkg/include), which index.html loads. Serve the
# repository's root (python3 -m http.server) and open
# /doc/gd/bench/wasmgc/gocmd/.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../../.." && pwd)
mkdir -p "$here/out"
(cd "$root/src" && GOROOT=$root GOOS=js GOARCH=wasm "$root/bin/go" build -o "$here/out/go.wasm" ./cmd/go)
cd "$root"
{
	echo go.env
	[ -f VERSION ] && echo VERSION
	find pkg/include -type f
	find src -path src/cmd -prune -o -type d -name testdata -prune -o -type f \
		\( -name '*.go' -o -name '*.s' -o -name '*.h' -o -name go.mod -o -name go.sum -o -name modules.txt \) \
		! -name '*_test.go' -print
} | tar --format=ustar -cf - -T - | gzip -9 > "$here/out/goroot.tar.gz"
ls -l "$here/out"
