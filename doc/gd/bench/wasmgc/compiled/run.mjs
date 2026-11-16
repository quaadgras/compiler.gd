// run.mjs prog.wasm: run a GOWASM=gc module
import fs from 'node:fs';
import v8 from 'node:v8';
import vm from 'node:vm';

// Goroutines run on stacks the engine makes (JSPI): as deep as Go's go.
v8.setFlagsFromString('--wasm-stack-switching-stack-size=8192');
// The baseline compiler gives every local of a function a slot in its
// frame, and the compiler gives every value a local: deep recursion
// through large functions (regexp/syntax) runs out of stack. Optimized
// code from the start, until the compiler reuses locals.
v8.setFlagsFromString('--no-liftoff');
// Compiling a function lazily, at its first call, on a goroutine's
// stack aborts V8 ("Check failed: isolate_->IsOnCentralStack()"):
// compile them all up front.
v8.setFlagsFromString('--no-wasm-lazy-compilation');
// After memory grows a lot, V8 may collect garbage for the external
// memory at the next host call; if that is on a goroutine's stack, it
// aborts ("Check failed: isolate_->IsOnCentralStack()"). So it is
// collected right away, on the host's stack (see growmem).
v8.setFlagsFromString('--expose-gc');
const collect = vm.runInNewContext('gc');
import { WASI } from 'node:wasi';

// The system interface of GOOS=wasip1, for what package os does.
const wasi = new WASI({ version: 'preview1', args: process.argv.slice(2), env: { ...process.env, PWD: process.cwd() }, preopens: { '/': '/' }, returnOnExit: false });

let line = [];
const flush = () => { console.log(new TextDecoder().decode(new Uint8Array(line))); line = []; };
let instance;
const objids = new WeakMap();
let lastid = 0;

// A block of linear memory is freed when the object that owns it is collected.
// (Freeing a block may queue its cleanups: see runtime/wasmgc/linear.go.)
const registry = new FinalizationRegistry((addr) => {
  instance.exports.free(BigInt(addr));
  if (idle) {
    idle = false;
    schedule();
  }
});

// Goroutines (see runtime/wasmgc/proc.go). Each runs on a stack of its own:
// one that blocks calls park, which suspends it until wake is called.
const parked = new Map();
let idle = false;

// The cleanups of runtime.AddCleanup: the runtime is told when the
// object is collected, and a goroutine runs the cleanup.
const cleanupTokens = new Map();
const cleanups = new FinalizationRegistry((id) => {
  cleanupTokens.delete(id);
  instance.exports.cleanup(id);
  if (idle) {
    idle = false;
    schedule();
  }
});
function growmem(pages) {
  const r = instance.exports.mem.grow(pages);
  instance.exports.mem.buffer.byteLength;
  if (pages >= 16) collect({ type: 'minor' });
  return r;
}
const fail = (err) => { if (line.length) flush(); console.error(err); process.exit(2); };
function schedule() {
  const next = instance.exports.sched();
  if (next < 0n) {
    // Nothing can run until a timer fires, if one is set, or a cleanup
    // is due (see cleanups).
    idle = true;
    const wait = instance.exports.timerwait();
    if (wait < 0n) {
      // Before deadlock is declared, what was collected may have
      // cleanups to run.
      collect();
      setTimeout(() => { if (idle) instance.exports.deadlock(); }, 10);
    } else {
      // (A longer delay than 2^31-1 ms would fire at once.)
      setTimeout(() => { idle = false; schedule(); }, Math.min(Number(wait / 1000000n), 0x7fffffff));
    }
    return;
  }
  idle = false;
  const id = Number(next >> 1n);
  if (next & 1n) {
    queueMicrotask(() => gorun(BigInt(id)).then(schedule, fail));
  } else {
    const wake = parked.get(id);
    parked.delete(id);
    wake();
  }
}

// The names of the functions, from the module's name section, for
// runtime.FuncForPC.
let funcNames = new Map(), nimports = 0;
const weakids = new WeakMap(), weakrefs = new Map();
let lastweak = 0;
function readNames(module) {
  nimports = WebAssembly.Module.imports(module).filter((i) => i.kind === 'function').length;
  for (const sec of WebAssembly.Module.customSections(module, 'name')) {
    const b = new Uint8Array(sec);
    let i = 0;
    const leb = () => { let r = 0, s = 0, c; do { c = b[i++]; r |= (c & 0x7f) << s; s += 7; } while (c & 0x80); return r >>> 0; };
    while (i < b.length) {
      const id = b[i++], len = leb(), end = i + len;
      if (id === 1) {
        for (let n = leb(); n > 0; n--) {
          const idx = leb(), l = leb();
          funcNames.set(idx, new TextDecoder().decode(b.subarray(i, i + l)));
          i += l;
        }
      }
      i = end;
    }
  }
}

const imports = {
  gd: {
    putc: (c) => { if (c === 10) flush(); else line.push(c); },
    nanotime: () => process.hrtime.bigint(),
    walltime: () => BigInt(Date.now()) * 1000000n,
    funcname: (pc, addr, n) => {
      const name = funcNames.get(Number(pc >> 16n) - 0x1000 + nimports) || '';
      const bytes = new TextEncoder().encode(name).subarray(0, n);
      new Uint8Array(instance.exports.mem.buffer, Number(addr), bytes.length).set(bytes);
      return bytes.length;
    },
    callers: (addr, n) => {
      const limit = Error.stackTraceLimit;
      Error.stackTraceLimit = n + 64;
      const stack = new Error().stack;
      Error.stackTraceLimit = limit;
      const pcs = new BigUint64Array(instance.exports.mem.buffer, Number(addr), n);
      let k = 0;
      for (const m of stack.matchAll(/wasm-function\[(\d+)\]/g)) {
        if (k === n) break;
        pcs[k++] = (BigInt(Number(m[1]) - nimports + 0x1000) << 16n) + 1n;
      }
      return k;
    },
    // (Memory grows on the host's stack: see runtime/wasmgc/linear.go.)
    // (The buffer of memory that has grown is made here too: the
    // first look at it, from a goroutine's stack, would abort.)
    grow: new WebAssembly.Suspending((pages) => new Promise((done) => queueMicrotask(() => done(growmem(pages))))),
    growsync: (pages) => growmem(pages),
    cleanup: (obj, id) => {
      const token = {};
      cleanupTokens.set(id, token);
      cleanups.register(obj, id, token);
    },
    uncleanup: (id) => {
      const token = cleanupTokens.get(id);
      if (token) {
        cleanups.unregister(token);
        cleanupTokens.delete(id);
      }
    },
    // runtime.GC: a full collection, from a task of its own, on the
    // host's stack; then the finalization callbacks may run.
    gc: new WebAssembly.Suspending(() => new Promise((done) => setTimeout(() => { collect(); setTimeout(done, 0); }, 0))),
    weakmake: (o) => {
      let id = weakids.get(o);
      if (id === undefined) {
        weakids.set(o, id = ++lastweak);
        weakrefs.set(id, new WeakRef(o));
      }
      return id;
    },
    weakget: (id) => {
      const o = weakrefs.get(id)?.deref();
      if (o === undefined) {
        weakrefs.delete(id);
        return null;
      }
      return o;
    },
    objid: (o) => {
      let id = objids.get(o);
      if (id === undefined) objids.set(o, id = ++lastid);
      return id;
    },
    register: (p, addr) => registry.register(p, addr),
    // (The next goroutine is chosen from a microtask, on the host's stack.)
    park: new WebAssembly.Suspending((id) => new Promise((wake) => { parked.set(id, wake); queueMicrotask(schedule); })),
    exit: (code) => {
      if (line.length) flush();
      if (code !== 0 && process.env.GOWASMGC_TRACE) console.error(new Error('exit ' + code).stack);
      process.exit(code);
    },
  },
};
Object.assign(imports, wasi.getImportObject());
const module = new WebAssembly.Module(fs.readFileSync(process.argv[2]));
readNames(module);
instance = await WebAssembly.instantiate(module, imports);
wasi.initialize({ exports: { memory: instance.exports.mem } });
const gorun = WebAssembly.promising(instance.exports.gorun);
WebAssembly.promising(instance.exports.run)().catch(fail);
