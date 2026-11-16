#!/bin/bash
# build.sh [prog.go]: compile prog.go (default main.go), the runtime in
# src/runtime/wasmgc and the packages of the standard library it imports
# with GOWASM=gc (GOOS=wasip1), and link out/prog.wasm:
#
#	./build.sh && node run.mjs out/main.wasm
#
# The go command cannot build for GOWASM=gc yet, so this drives the
# compiler and the linker of the toolchain in ../../../../.. directly
# (see ../std/build.py). A function that uses something the compiler
# does not support yet is compiled to a trap, with a note on standard
# error unless GOWASMGC_QUIET is set.
set -e
cd "$(dirname "$0")"
prog=${1:-main.go}
name=$(basename ${prog%.go})
mkdir -p out
python3 ../std/build.py -o out/$name.wasm $prog
ls -l out/$name.wasm
