#!/bin/bash
# binaries.sh: the gd go command for each platform it is offered for, in
# r2/ (served as download.compiler.gd): gd-<goos>-<goarch>.tar.gz, with
# the one file go, or gd-windows-<goarch>.zip, with go.exe. The binary
# is all of the toolchain: it carries the standard library (unpacked in
# $GDPATH/std on first use) and runs compile, asm and link in-process,
# for any GOOS and GOARCH. Built with the toolchain of this tree
# (bin/go, after src/make.bash), -trimpath, so that it finds nothing of
# this machine. The names are fixed: the bucket keeps only the latest.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
out=$here/r2
mkdir -p "$out"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
	goos=${target%/*} goarch=${target#*/}
	exe=go
	[ "$goos" = windows ] && exe=go.exe
	mkdir -p "$tmp/$goos-$goarch"
	(cd "$root/src" && GOROOT=$root GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 "$root/bin/go" build -trimpath -o "$tmp/$goos-$goarch/$exe" ./cmd/go)
	if [ "$goos" = windows ]; then
		rm -f "$out/gd-$goos-$goarch.zip"
		(cd "$tmp/$goos-$goarch" && zip -q -9 -X "$out/gd-$goos-$goarch.zip" "$exe")
	else
		tar -C "$tmp/$goos-$goarch" --owner=0 --group=0 --mtime=@0 -cf - "$exe" | gzip -9n > "$out/gd-$goos-$goarch.tar.gz"
	fi
	echo "binaries: $goos/$goarch $(stat -c %s "$tmp/$goos-$goarch/$exe") bytes" >&2
done
ls -l "$out"/gd-* >&2
