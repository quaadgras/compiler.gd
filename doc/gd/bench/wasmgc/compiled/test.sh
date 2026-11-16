#!/bin/bash
# test.sh: build and run each program in tests with GOWASM=gc, and
# compare what it prints with what the native build prints.
cd "$(dirname "$0")"
export GOROOT=$(cd ../../../../.. && pwd) GOWASMGC_QUIET=1 NODE_OPTIONS=
clean() { grep -v '^goroutine\|^main\.\|^\s/\|^exit status\|^\s*$\|wasm://\|RuntimeError\|^    at \|Node.js'; }
fail=0
for t in tests/*.go; do
	n=$(basename ${t%.go})
	rm -rf out/ref && mkdir -p out/ref && cp $t out/ref/main.go && printf 'module ref\n\ngo 1.26\n' > out/ref/go.mod
	want=$(cd out/ref && $GOROOT/bin/go run . 2>&1 | clean)
	./build.sh $t > out/$n.log 2>&1
	got=$(node --disable-warning=ExperimentalWarning run.mjs out/$n.wasm 2>&1 | clean)
	if [ "$got" = "$want" ]; then echo "ok   $n"; else echo "FAIL $n"; fail=1; fi
done
exit $fail
