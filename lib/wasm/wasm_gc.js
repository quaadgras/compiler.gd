// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasm_gc.js runs a GOWASM=gc program: the part of the host that does
// not depend on where it runs. wasm_gc_node.mjs (node) and
// wasm_gc_browser.js (browsers) give it the system interface (WASI) and
// what the engine allows them.
//
// The program needs WebAssembly GC and JSPI (WebAssembly.Suspending and
// WebAssembly.promising): each goroutine runs on a stack of its own.

// runWasmGC instantiates module and runs the program. host has:
//   wasi:      the imports of wasi_snapshot_preview1, and start(instance)
//              to give it the memory (or null);
//   write(fd, text): a line of output from the runtime's print (fd 2);
//   exit(code): the program ended;
//   collect(major): collect garbage now, if the engine allows it (else
//              null): runtime.GC, and after memory grows (V8 aborts if
//              it collects for the memory later, on a goroutine's stack);
//   nanotime(): a monotonic clock, as a BigInt of nanoseconds;
//   trace:     print the stack when the program fails.
// It returns a promise that is settled when the program has started.
export async function runWasmGC(module, host) {
  let line = [];
  const flush = () => { host.write(2, new TextDecoder().decode(new Uint8Array(line))); line = []; };
  let instance;
  const objids = new WeakMap();
  let lastid = 0;
  const collect = host.collect || (() => {});

  // A block of linear memory is freed when the object that owns it is
  // collected. (Freeing a block may queue its cleanups: see
  // runtime/wasmgc/linear.go.)
  const registry = new FinalizationRegistry((addr) => {
    instance.exports.free(BigInt(addr));
    wakeIfIdle();
  });

  // Goroutines (see runtime/wasmgc/proc.go). Each runs on a stack of its
  // own: one that blocks calls park, which suspends it until wake is
  // called.
  const parked = new Map();
  let idle = false;
  const wakeIfIdle = () => {
    if (idle) {
      idle = false;
      schedule();
    }
  };

  // The cleanups of runtime.AddCleanup: the runtime is told when the
  // object is collected, and a goroutine runs the cleanup.
  const cleanupTokens = new Map();
  const cleanups = new FinalizationRegistry((id) => {
    cleanupTokens.delete(id);
    instance.exports.cleanup(id);
    wakeIfIdle();
  });

  // Memory grows on the host's stack (see runtime/wasmgc/linear.go), and
  // its buffer is made here: the first look at it, from a goroutine's
  // stack, could abort.
  function growmem(pages) {
    const r = instance.exports.mem.grow(pages);
    instance.exports.mem.buffer.byteLength;
    if (pages >= 16) collect(false);
    return r;
  }

  // A goroutine that ran out of the engine's stack (the engine's stacks
  // do not grow) fails as Go's do: a fatal error, which deferred calls
  // do not see. id is the goroutine's.
  const isOverflow = (err) =>
    (err instanceof RangeError && /call stack/i.test(err.message)) || // V8, JavaScriptCore
    (err && err.name === 'InternalError' && /recursion/i.test(err.message)); // SpiderMonkey
  const fail = (err, id = 1) => {
    if (err && err.goExit !== undefined) return; // (the program exited)
    if (line.length) flush();
    if (isOverflow(err)) {
      const out = ["runtime: goroutine stack exceeds the WebAssembly engine's stack size", 'fatal error: stack overflow', '', `goroutine ${id} [running]:`];
      // (As many frames as the engine keeps in an error's stack.)
      for (const l of String(err.stack).split('\n')) {
        if (l.includes('wasm-function')) out.push(l.trim().replace(/^at /, '').split(/[ @(]/)[0] + '(...)');
      }
      out.push('...additional frames elided...');
      host.write(2, out.join('\n'));
      host.exit(2);
      return;
    }
    host.write(2, String(err && err.stack || err));
    host.exit(2);
  };
  let gorun;
  function schedule() {
    const next = instance.exports.sched();
    if (next < 0n) {
      // Nothing can run until a timer fires, if one is set, or a
      // cleanup is due (see cleanups).
      idle = true;
      const wait = instance.exports.timerwait();
      if (wait < 0n) {
        // Before deadlock is declared, what was collected may have
        // cleanups to run.
        collect(true);
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
      queueMicrotask(() => gorun(BigInt(id)).then(schedule, (err) => fail(err, id)));
    } else {
      const wake = parked.get(id);
      parked.delete(id);
      wake();
    }
  }

  // The names of the functions, from the module's name section, for
  // runtime.FuncForPC.
  const funcNames = new Map();
  const nimports = WebAssembly.Module.imports(module).filter((i) => i.kind === 'function').length;
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

  const weakids = new WeakMap(), weakrefs = new Map();
  let lastweak = 0;

  let heapStart = 0;
  const imports = {
    gd: {
      heapstart: () => BigInt(heapStart),
      putc: (c) => { if (c === 10) flush(); else line.push(c); },
      nanotime: () => host.nanotime(),
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
      // runtime.GC: a collection (if the engine allows), from a task of
      // its own, on the host's stack; then the finalization callbacks may
      // run.
      gc: new WebAssembly.Suspending(() => new Promise((done) => setTimeout(() => { collect(true); setTimeout(done, 0); }, 0))),
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
      // (The next goroutine is chosen from a microtask, on the host's
      // stack.)
      park: new WebAssembly.Suspending((id) => new Promise((wake) => { parked.set(id, wake); queueMicrotask(schedule); })),
      exit: (code) => {
        if (line.length) flush();
        if (code !== 0 && host.trace) host.write(2, new Error('exit ' + code).stack);
        host.exit(code);
      },
    },
    wasi_snapshot_preview1: host.wasi.imports,
  };
  instance = await WebAssembly.instantiate(module, imports);
  heapStart = instance.exports.mem.buffer.byteLength;
  host.wasi.start(instance);
  if (host.reserve) {
    // Memory reserved up front, from the host's stack: growing it later,
    // on a goroutine's stack, can abort an engine that cannot be told to
    // collect (see growmem).
    const have = instance.exports.mem.buffer.byteLength;
    if (host.reserve > have) {
      instance.exports.mem.grow(Math.ceil((host.reserve - have) / 65536));
      instance.exports.mem.buffer.byteLength;
    }
  }
  gorun = WebAssembly.promising(instance.exports.gorun);
  WebAssembly.promising(instance.exports.run)().catch(fail);
  return instance;
}
