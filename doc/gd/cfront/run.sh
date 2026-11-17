#!/bin/sh
# run.sh file.c: translate file.c, run it, and compare with gcc's build.
set -e
cd "$(dirname "$0")"
: "${TMPDIR:=/tmp}"
name=$(basename "$1" .c)
go build -o "$TMPDIR/cfront" .
mkdir -p "demo/$name"
"$TMPDIR/cfront" -main -o "demo/$name/${name}_c.go" "$1"
gcc -w -o "$TMPDIR/${name}_gcc" "$1"
"$TMPDIR/${name}_gcc" > "$TMPDIR/${name}_gcc.txt"
go run "./demo/$name" > "$TMPDIR/${name}_go.txt"
diff "$TMPDIR/${name}_gcc.txt" "$TMPDIR/${name}_go.txt"
echo "$name: same output as gcc"
