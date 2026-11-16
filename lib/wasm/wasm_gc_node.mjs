// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasm_gc_node.mjs prog.wasm args...: run a GOWASM=gc program under node.
// go_wasip1_wasm_exec runs it for go run and go test with GOWASM=gc.
import fs from 'node:fs';
import v8 from 'node:v8';
import vm from 'node:vm';
import { WASI } from 'node:wasi';
import { runWasmGC } from './wasm_gc.js';

// Goroutines run on stacks the engine makes (JSPI): as deep as Go's go.
v8.setFlagsFromString('--wasm-stack-switching-stack-size=8192');
// The baseline compiler gives every local of a function a slot in its
// frame: deep recursion through large functions (regexp/syntax) runs out
// of stack. Optimized code from the start.
v8.setFlagsFromString('--no-liftoff');
// All the functions are compiled up front.
v8.setFlagsFromString('--no-wasm-lazy-compilation');
// After memory grows a lot, V8 may collect garbage for the external
// memory at the next host call; if that is on a goroutine's stack, it
// aborts ("Check failed: isolate_->IsOnCentralStack()"). So it is
// collected right away, on the host's stack (see wasm_gc.js growmem).
v8.setFlagsFromString('--expose-gc');
const gc = vm.runInNewContext('gc');

// The system interface of GOOS=wasip1, for what package os does.
const wasi = new WASI({ version: 'preview1', args: process.argv.slice(2), env: { ...process.env, PWD: process.cwd() }, preopens: { '/': '/' }, returnOnExit: false });

const module = new WebAssembly.Module(fs.readFileSync(process.argv[2]));
await runWasmGC(module, {
  wasi: {
    imports: wasi.getImportObject().wasi_snapshot_preview1,
    start: (instance) => wasi.initialize({ exports: { memory: instance.exports.mem } }),
  },
  write: (fd, text) => (fd === 2 ? console.error : console.log)(text),
  exit: (code) => process.exit(code),
  collect: (major) => (major ? gc() : gc({ type: 'minor' })),
  nanotime: () => process.hrtime.bigint(),
  trace: !!process.env.GOWASMGC_TRACE,
});
