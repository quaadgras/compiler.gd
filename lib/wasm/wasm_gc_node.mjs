// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasm_gc_node.mjs prog.wasm args...: run a GOWASM=gc program under node.
// go_wasip1_wasm_exec runs it for go run and go test with GOWASM=gc, and
// go_js_wasm_exec for GOOS=js: such a program has node's globals,
// with node's fs, process and require, as under wasm_exec_node.js.
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
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

const module = new WebAssembly.Module(fs.readFileSync(process.argv[2]));
const isJS = WebAssembly.Module.imports(module).some((i) => i.module === 'gojs');
if (isJS) {
  globalThis.require = createRequire(import.meta.url);
  globalThis.fs = fs;
  globalThis.path = path;
  process.env.TMPDIR ??= os.tmpdir();
}
const wasi = nodeWASI(process.argv.slice(2));

// GOOS=js: when node has nothing left to do and no goroutine can run,
// the program is deadlocked.
let idle = null;
process.on('beforeExit', () => {
  const f = idle;
  idle = null;
  if (f) f();
});

await runWasmGC(module, {
  idle: (deadlock) => { idle = deadlock; },
  wasi: { imports: wasi.imports, start: (inst) => wasi.start(inst.exports.mem) },
  write: (fd, text) => (fd === 2 ? console.error : console.log)(text),
  exit: (code) => process.exit(code),
  collect: (major) => (major ? gc() : gc({ type: 'minor' })),
  nanotime: () => process.hrtime.bigint(),
  trace: !!process.env.GOWASMGC_TRACE,
  maxStacks: Number(process.env.GOWASMGC_MAXSTACKS || 0), // (for testing: see wasm_gc.js)
  poison: !!process.env.GOWASMGC_POISON,
});
