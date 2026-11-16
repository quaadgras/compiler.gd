#!/bin/bash
# one.sh test.go: build and run one test of $GOROOT/test, keeping the output
cd "$(dirname "$0")"; export GOROOT=$(cd ../../../../.. && pwd) GOOS=wasip1 GOARCH=wasm GOWASM=gc NODE_OPTIONS=
tool=$GOROOT/pkg/tool/linux_amd64; n=out/one
$tool/compile -dwarf=false -p main -importcfg ../std/out/importcfg -o $n.a $GOROOT/test/$1 2>&1 | cut -c1-300 | head -${L:-6}
$tool/link -checklinkname=0 -importcfg ../std/out/importcfg.link -o $n.wasm $n.a 2>&1 | head -5 && node --disable-warning=ExperimentalWarning --stack-size=4000 ../compiled/run.mjs $n.wasm 2>&1 | head -${L:-12} | cut -c1-200
