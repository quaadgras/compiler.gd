// prebuild.mjs go.wasm goroot.tar.gz cache.tar pkgs...: build the
// packages pkgs with the go command of the playground (go.wasm, GOOS=js)
// as the page runs it (play/worker.mjs: GOROOT at /goroot, the cache at
// /home/cache, files in memory), and write the cache it made as a tar,
// for the page to start with.
import fs from 'node:fs';
import zlib from 'node:zlib';
import { runGo } from './wasm_gc_browser.js';
import { memFS } from './wasm_gc_fs.js';
import { untar, tar, env } from './files.mjs';

const [wasm, goroot, out, ...pkgs] = process.argv.slice(2);
const files = memFS();
untar(files, '/goroot', zlib.gunzipSync(fs.readFileSync(goroot)));
files.mkdirTree('/home/cache');
files.mkdirTree('/work');
// The page's global, but for its own fs, process and path (which
// runGo sets: node's own process is not the program's).
const own = {};
const global = new Proxy(globalThis, {
  get: (t, k) => (k in own ? own[k] : k === 'fs' || k === 'process' || k === 'path' ? undefined : Reflect.get(t, k)),
  set: (t, k, v) => { own[k] = v; return true; },
});
const t0 = performance.now();
const code = await runGo(fs.readFileSync(wasm), {
  args: ['go', 'build', ...pkgs], env, fs: files, global,
  stdout: (l) => console.log(l), stderr: (l) => console.error(l),
});
console.error(`go build: exit ${code} after ${Math.round(performance.now() - t0)} ms`);
if (code !== 0) process.exit(1);
fs.writeFileSync(out, tar(files, '/home/cache'));
process.exit(0);
