#!/bin/bash
# check.sh: the layout of the structs of layout.go, as GOOS=js
# GOARCH=wasm lays them out (wasm32, stage 16 of ../../../wasm-gc.md),
# against clang's --target=wasm32 for the same structs in C (layout.h).
# It fails if any size, alignment or field offset differs. A measuring
# stick, not part of any build: it needs clang and node.
#
# Fields are matched by position (Go's names are capitalized, C's not).
set -e
cd "$(dirname "$0")"
export GOROOT=${GOROOT:-$(cd ../../../../.. && pwd)} NODE_OPTIONS=
export PATH=$GOROOT/bin:$GOROOT/lib/wasm:$PATH
out=$(mktemp -d)
trap 'rm -rf $out' EXIT
printf 'module layout\n\ngo 1.26\n' > $out/go.mod
cp layout.go $out/
(cd $out && GOOS=js GOARCH=wasm go build -o layout.wasm . && go_js_wasm_exec ./layout.wasm) > $out/go.txt
# The C side: a static assertion for each line Go printed.
{
	echo '#include "layout.h"'
	awk '
	$1 == "struct" { s = $2; n = 0
		printf "_Static_assert(sizeof(struct %s) == %d, \"size of %s: Go %d\");\n", s, $3, s, $3
		printf "_Static_assert(_Alignof(struct %s) == %d, \"alignment of %s: Go %d\");\n", s, $4, s, $4 }
	$1 == "field" { print "FIELD " $2 " " n " " $4; n++ }
	' $out/go.txt
} > $out/check.c.in
# Fields by position: their C names, from layout.h.
python3 - "$out" <<'PY'
import re, sys
out = sys.argv[1]
h = open('layout.h').read()
fields = {}
for name, body in re.findall(r'struct (\w+) \{(.*?)\};', h):
    names = []
    for decl in body.split(';'):
        decl = decl.strip()
        if not decl:
            continue
        m = re.search(r'(\w+)\s*(\[\d+\])?$', decl)
        names.append(m.group(1))
    fields[name] = names
lines = []
for l in open(out + '/check.c.in'):
    if l.startswith('FIELD '):
        _, s, i, off = l.split()
        f = fields[s][int(i)]
        lines.append('_Static_assert(offsetof(struct %s, %s) == %s, "offset of %s.%s: Go %s");\n' % (s, f, off, s, f, off))
    else:
        lines.append(l)
open(out + '/check.c', 'w').write(''.join(lines))
PY
cp layout.h $out/
n=$(grep -c _Static_assert $out/check.c)
if clang --target=wasm32 -ffreestanding -fsyntax-only $out/check.c; then
	echo "ok: $n sizes, alignments and offsets as clang --target=wasm32 has them"
else
	echo "FAIL: Go and clang's wasm32 lay these structs out differently"
	exit 1
fi
