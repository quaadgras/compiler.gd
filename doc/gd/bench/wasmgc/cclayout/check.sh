#!/bin/bash
# check.sh: the layout of C's types as cmd/cc lays them out for GOOS=js
# GOARCH=wasm (wasm32: ILP32, cmd/cc's target.go; doc/gd/cc.md, "C on
# wasm32"), against clang's --target=wasm32 for the same declarations
# (layout.h), and against Go's layout of the structs layout.go declares.
# It fails if any size, alignment or member offset differs. A measuring
# stick, not part of any build: it needs clang and node.
#
# GOWASM in the environment picks the build (GOWASM=gc,split,ptr64 has
# 8-byte words and LP64, and must fail).
set -e
cd "$(dirname "$0")"
export GOROOT=${GOROOT:-$(cd ../../../../.. && pwd)} NODE_OPTIONS=
export PATH=$GOROOT/bin:$GOROOT/lib/wasm:$PATH GOOS=js GOARCH=wasm CGO_ENABLED=0
out=$(mktemp -d)
trap 'rm -rf $out' EXIT
cp layout.h layout.go $out/
# A C program that prints each struct's size and alignment and each
# member's offset (but a bit-field's), and the C assertions that clang
# checks the same numbers with.
python3 - "$out" <<'PY'
import re, sys
out = sys.argv[1]
h = open('layout.h').read()
prog = ['#include <stdio.h>', '#include "layout.h"', 'int main(void) {']
decls = []
for kw, name, body in re.findall(r'(struct|union)\s+(?:__attribute__\(\(\w+\)\)\s+)?(\w+)\s*\{(.*?)\};', h):
    t = kw + ' ' + name
    decls.append((t, name, body))
    prog.append('\tprintf("struct %s %%zu %%zu\\n", sizeof(%s), _Alignof(%s));' % (name, t, t))
    for i, d in enumerate(x.strip() for x in body.split(';') if x.strip()):
        if ':' in d:
            continue
        m = re.search(r'\(\*(\w+)\)', d) or re.search(r'(\w+)\s*(\[\d*\])*$', d)
        prog.append('\tprintf("field %s %d %%zu\\n", offsetof(%s, %s));' % (name, i, t, m.group(1)))
prog.append('}')
open(out + '/main.c', 'w').write('\n'.join(prog) + '\n')
PY
mkdir $out/cc $out/go
printf 'module cclayout\n\ngo 1.27\n' > $out/cc/go.mod
cp $out/go.mod $out/go/ 2>/dev/null || printf 'module golayout\n\ngo 1.27\n' > $out/go/go.mod
go tool cc -main -I $out -o $out/cc/c.go $out/main.c
(cd $out/cc && go build -o cc.wasm . && go_js_wasm_exec ./cc.wasm) > $out/cc.txt
cp layout.go $out/go/
(cd $out/go && go build -o go.wasm . && go_js_wasm_exec ./go.wasm) > $out/go.txt
# clang: an assertion for each number cmd/cc printed.
python3 - "$out" <<'PY' > $out/check.c
import re, sys
out = sys.argv[1]
h = open('layout.h').read()
kinds, names = {}, {}
for kw, name, body in re.findall(r'(struct|union)\s+(?:__attribute__\(\(\w+\)\)\s+)?(\w+)\s*\{(.*?)\};', h):
    kinds[name] = kw
    ds = [x.strip() for x in body.split(';') if x.strip()]
    names[name] = [(re.search(r'\(\*(\w+)\)', d) or re.search(r'(\w+)\s*(\[\d*\])*$', d)).group(1) for d in ds]
print('#include "layout.h"')
for l in open(out + '/cc.txt'):
    f = l.split()
    t = kinds[f[1]] + ' ' + f[1]
    if f[0] == 'struct':
        print('_Static_assert(sizeof(%s) == %s, "size of %s: cmd/cc %s");' % (t, f[2], t, f[2]))
        print('_Static_assert(_Alignof(%s) == %s, "alignment of %s: cmd/cc %s");' % (t, f[3], t, f[3]))
    else:
        m = names[f[1]][int(f[2])]
        print('_Static_assert(offsetof(%s, %s) == %s, "offset of %s.%s: cmd/cc %s");' % (t, m, f[3], t, m, f[3]))
PY
n=$(grep -c _Static_assert $out/check.c)
fail=0
if clang --target=wasm32 -ffreestanding -fsyntax-only -I $out $out/check.c; then
	echo "ok: $n sizes, alignments and offsets of cmd/cc's as clang --target=wasm32 has them"
else
	echo "FAIL: cmd/cc and clang's wasm32 lay these out differently"
	fail=1
fi
# Go: every line Go printed is cmd/cc's.
if missing=$(grep -vxFf $out/cc.txt $out/go.txt); then
	echo "FAIL: Go lays these out differently from cmd/cc:"
	echo "$missing"
	fail=1
else
	echo "ok: $(wc -l < $out/go.txt) sizes, alignments and offsets of Go's as cmd/cc has them"
fi
exit $fail
