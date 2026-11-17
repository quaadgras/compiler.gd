// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasm_gc_node.mjs prog.wasm args...: run a GOWASM=gc program under node.
// go_wasip1_wasm_exec runs it for go run and go test with GOWASM=gc.
import fs from 'node:fs';
import v8 from 'node:v8';
import vm from 'node:vm';
import { runWasmGC } from './wasm_gc.js';
import { nodeWASI } from './wasm_gc_wasi_node.mjs';

// Goroutines run on stacks the engine makes (JSPI), of the size they
// have in a browser: a goroutine whose stack grows goes on on another
// (see hop in wasm_gc.js).
// Optimized code from the start: the tests are long.
v8.setFlagsFromString('--no-liftoff');
// All the functions are compiled up front.
v8.setFlagsFromString('--no-wasm-lazy-compilation');
// After memory grows a lot, V8 may collect garbage for the external
// memory at the next host call; if that is on a goroutine's stack, it
// aborts ("Check failed: isolate_->IsOnCentralStack()"). So it is
// collected right away, on the host's stack (see wasm_gc.js growmem).
v8.setFlagsFromString('--expose-gc');
const gc = vm.runInNewContext('gc');
// (More flags of the engine, to try the program with: GOWASMGC_V8FLAGS.)
for (const flag of (process.env.GOWASMGC_V8FLAGS || '').split(/\s+/).filter(Boolean)) v8.setFlagsFromString(flag);

const wasi = nodeWASI(process.argv.slice(2));

const module = new WebAssembly.Module(fs.readFileSync(process.argv[2]));
await runWasmGC(module, {
  wasi: { imports: wasi.imports, start: (inst) => wasi.start(inst.exports.mem) },
  write: (fd, text) => (fd === 2 ? console.error : console.log)(text),
  exit: (code) => process.exit(code),
  collect: (major) => (major ? gc() : gc({ type: 'minor' })),
  nanotime: () => process.hrtime.bigint(),
  trace: !!process.env.GOWASMGC_TRACE,
});
