// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasm_gc_node.mjs prog.wasm args...: run a GOWASM=gc program under node.
// go_wasip1_wasm_exec runs it for go run and go test with GOWASM=gc.
import fs from 'node:fs';
import path from 'node:path';
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

// Node's WASI (uvwasi) differs from the specification, and from what
// package os expects, in a few calls, which are wrapped here.
let instance;
const node = wasi.getImportObject().wasi_snapshot_preview1;
const mem = () => instance.exports.mem.buffer;
const str = (p, n) => Buffer.from(mem(), p, n);
const EINVAL = 28, ELOOP = 32, ESUCCESS = 0;
const errno = { ENOENT: 44, EACCES: 2, EPERM: 63, ENOTDIR: 54, EISDIR: 31, ELOOP: 32, EINVAL: 28, EEXIST: 20 };
const fail = (err) => errno[err.code] ?? 29; // (EIO)
const OFLAG_DIRECTORY = 2, LOOKUP_SYMLINK_FOLLOW = 1, RIGHT_FD_WRITE = 1n << 6n;
// The rights for which uvwasi opens for writing (FD_DATASYNC, FD_WRITE,
// FD_ALLOCATE, FD_FILESTAT_SET_SIZE), which a directory cannot be.
const WRITE_RIGHTS = (1n << 0n) | (1n << 6n) | (1n << 8n) | (1n << 22n);
// fd_fdstat_get reports the host's open flags (O_WRONLY, O_APPEND...)
// where WASI's fdflags go: O_WRONLY (1) reads as FDFLAG_APPEND, and
// package os, setting non-blocking mode on what it read, turns append
// on. They are translated.
const hostFlags = [
  [fs.constants.O_APPEND, 1], // FDFLAG_APPEND
  [fs.constants.O_DSYNC, 2], // FDFLAG_DSYNC
  [fs.constants.O_NONBLOCK, 4], // FDFLAG_NONBLOCK
  [fs.constants.O_SYNC, 16], // FDFLAG_SYNC (includes O_DSYNC on Linux)
];
// The host path of each descriptor opened (uvwasi works by path too),
// for what is done here with package fs.
const paths = new Map([[3, '/']]);
const hostPath = (fd, p, n) => paths.has(fd) ? path.resolve(paths.get(fd), str(p, n).toString()) : undefined;
// uvwasi keeps whole seconds; fstflags: ATIM, ATIM_NOW, MTIM, MTIM_NOW.
function setTimes(file, follow, atim, mtim, fst) {
  try {
    const st = (follow ? fs.statSync : fs.lstatSync)(file, { bigint: true });
    const now = BigInt(Date.now()) * 1000000n;
    const a = fst & 2 ? now : fst & 1 ? atim : st.atimeNs;
    const m = fst & 8 ? now : fst & 4 ? mtim : st.mtimeNs;
    const sec = (ns) => Number(ns / 1000n) / 1e6; // (microseconds: what a double keeps)
    (follow ? fs.utimesSync : fs.lutimesSync)(file, sec(a), sec(m));
    return ESUCCESS;
  } catch (err) {
    return fail(err);
  }
}
const imports = {
  ...node,
  fd_fdstat_get(fd, statp) {
    const r = node.fd_fdstat_get(fd, statp);
    if (r === 0) {
      const v = new DataView(mem());
      const host = v.getUint16(statp + 2, true);
      let flags = 0;
      for (const [h, w] of hostFlags) if (h && (host & h) === h) flags |= w;
      v.setUint16(statp + 2, flags, true);
    }
    return r;
  },
  path_open(dirfd, lookup, p, n, oflags, base, inheriting, fdflags, fdp) {
    if (str(p, n).includes(0)) return EINVAL;
    const file = hostPath(dirfd, p, n);
    // uvwasi follows a symbolic link at the end of the path even when
    // asked not to (os.Root resolves links itself, and relies on that).
    if (!(lookup & LOOKUP_SYMLINK_FOLLOW) && file !== undefined) {
      try {
        if (fs.lstatSync(file).isSymbolicLink()) return ELOOP;
      } catch {}
    }
    // A directory gets the rights of what is opened in it: package os
    // opens one (os.Root) with read rights only, and uvwasi, unlike
    // other runtimes, then refuses to create files in it.
    let dir = oflags & OFLAG_DIRECTORY;
    if (!dir && file !== undefined) {
      try {
        dir = fs.statSync(file).isDirectory();
      } catch {}
    }
    if (dir) base |= inheriting & ~WRITE_RIGHTS;
    // Opened for reading, a file keeps FD_FILESTAT_SET_SIZE in package
    // syscall's rights, for which uvwasi opens it for writing too
    // (refused for read-only files).
    if (!(base & RIGHT_FD_WRITE)) base &= ~WRITE_RIGHTS;
    const r = node.path_open(dirfd, lookup, p, n, oflags, base, inheriting, fdflags, fdp);
    if (r === 0 && file !== undefined) paths.set(new DataView(mem()).getUint32(fdp, true), file);
    return r;
  },
  fd_close(fd) {
    paths.delete(fd);
    return node.fd_close(fd);
  },
  fd_renumber(from, to) {
    const r = node.fd_renumber(from, to);
    if (r === 0) {
      paths.delete(to);
      if (paths.has(from)) paths.set(to, paths.get(from));
      paths.delete(from);
    }
    return r;
  },
  // uvwasi removes the directory itself for ".".
  path_remove_directory(fd, p, n) {
    if (path.basename(str(p, n).toString()) === '.') return EINVAL;
    return node.path_remove_directory(fd, p, n);
  },
  path_filestat_set_times(fd, lookup, p, n, atim, mtim, fst) {
    const file = hostPath(fd, p, n);
    if (file === undefined) return node.path_filestat_set_times(fd, lookup, p, n, atim, mtim, fst);
    return setTimes(file, lookup & LOOKUP_SYMLINK_FOLLOW, atim, mtim, fst);
  },
  fd_filestat_set_times(fd, atim, mtim, fst) {
    if (!paths.has(fd)) return node.fd_filestat_set_times(fd, atim, mtim, fst);
    return setTimes(paths.get(fd), true, atim, mtim, fst);
  },
};

const module = new WebAssembly.Module(fs.readFileSync(process.argv[2]));
await runWasmGC(module, {
  wasi: {
    imports,
    start: (inst) => { instance = inst; wasi.initialize({ exports: { memory: inst.exports.mem } }); },
  },
  write: (fd, text) => (fd === 2 ? console.error : console.log)(text),
  exit: (code) => process.exit(code),
  collect: (major) => (major ? gc() : gc({ type: 'minor' })),
  nanotime: () => process.hrtime.bigint(),
  trace: !!process.env.GOWASMGC_TRACE,
});
