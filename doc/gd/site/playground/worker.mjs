// worker.mjs: the playground's go command (GOOS=js, built by
// ../build.sh) in a worker of playground.html, so that the page stays live
// while it compiles. Its files are in memory (memFS): GOROOT at /goroot;
// the program at /work; $HOME at /home, kept in IndexedDB, with the build
// cache, which starts as the one build.sh made of common packages.
//
// From the page: {run: source}, Go or C (see isGo). To the page: {line, fd} (fd 1 and 2 are
// the programs' output, 0 the playground's notes), {status, done?,
// total?} while loading, {ready}, {build: 'building' or how it went,
// failed?}, {finished} when a run is over.
import { runGo } from './wasm_gc_browser.js';
import { memFS } from './wasm_gc_fs.js';
import { env, untar } from './files.mjs';

const post = (m) => postMessage(m);
const note = (line) => post({ line, fd: 0 });
const ms = (t0) => (performance.now() - t0 < 10000 ? Math.round(performance.now() - t0) + ' ms' : ((performance.now() - t0) / 1000).toFixed(1) + ' s');

// The large files (their names have a hash of what is in them) are kept
// in Cache Storage: a browser's HTTP cache may not keep a response of
// 24 MB (Chrome keeps an entry of at most an eighth of its cache, a
// private window very little), however long its headers allow.
const store = (async () => {
  try {
    return await caches.open('gd-playground');
  } catch {
    return null; // (No Cache Storage: not a secure context.)
  }
})();

// keepOnly deletes what the store has but names.
async function keepOnly(names) {
  const s = await store;
  if (!s) return;
  const want = new Set(names.map((n) => new URL(n, location.href).href));
  for (const req of await s.keys()) if (!want.has(req.url)) await s.delete(req);
}

// fetchBody returns the stream of the body of url, from the store or
// the network (keeping it), reporting progress.
async function fetchBody(url, what) {
  const s = await store;
  let res = s && (await s.match(url));
  const cached = !!res;
  if (!res) {
    res = await fetch(url);
    if (!res.ok) throw new Error(`${url}: ${res.status} ${res.statusText}`);
    if (s) s.put(url, res.clone()).catch(() => {}); // (Full or not allowed: it is downloaded again next time.)
  }
  const label = (cached ? 'loading ' : 'downloading ') + what + (cached ? ' (cached)' : '');
  const total = Number(res.headers.get('content-length')) || 0;
  let done = 0;
  return res.body.pipeThrough(new TransformStream({
    transform(chunk, c) {
      done += chunk.length;
      post({ status: label, done, total });
      c.enqueue(chunk);
    },
  }));
}

const gunzip = (stream) => stream.pipeThrough(new DecompressionStream('gzip'));
const bytes = async (stream) => new Uint8Array(await new Response(stream).arrayBuffer());

// isGo reports whether source is Go: whether its first line that is not
// blank or a comment starts with "package". Anything else is C.
function isGo(source) {
  let block = false;
  for (let line of source.split('\n')) {
    line = line.trim();
    if (block) {
      const end = line.indexOf('*/');
      if (end < 0) continue;
      block = false;
      line = line.slice(end + 2).trim();
    }
    if (line.startsWith('/*')) {
      const end = line.indexOf('*/', 2);
      if (end < 0) { block = true; continue; }
      line = line.slice(end + 2).trim();
    }
    if (line === '' || line.startsWith('//')) continue;
    return /^package\b/.test(line);
  }
  return true;
}

// A C program is main.c, with this Go program to run its main: the go
// command translates the C with cc (as for any import "C" with cgo off).
const cMain = `// Code that runs main.c's main (the playground's).
package main

// #include <stdlib.h>
// int main(void);
// // exit, not os.Exit: it flushes C's stdout (a file, not a terminal).
// static void run(void) { exit(main()); }
import "C"

func main() { C.run() }
`;

// removeTree removes dir and what is in it.
function removeTree(fs, dir) {
  for (const e of fs.readdir(dir)) {
    if (e === '.' || e === '..') continue;
    const p = dir + '/' + e;
    if (fs.isDir(fs.stat(p).mode)) removeTree(fs, p);
    else fs.unlink(p);
  }
  fs.rmdir(dir);
}

const ready = (async () => {
  const t0 = performance.now();
  const manifest = await (await fetch('manifest.json', { cache: 'no-cache' })).json();
  keepOnly([manifest.go, manifest.goroot, manifest.cache]).catch(() => {});
  const goP = (async () => WebAssembly.compileStreaming(new Response(gunzip(await fetchBody(manifest.go, 'the go command')), { headers: { 'Content-Type': 'application/wasm' } })))();
  const gorootP = (async () => bytes(gunzip(await fetchBody(manifest.goroot, 'the standard library'))))();
  const fs = memFS();
  fs.mkdirTree('/home');
  await fs.persist('/home');
  // The cache of another version of the go command is of no use.
  let version = '';
  try { version = fs.readFile('/home/version', { encoding: 'utf8' }); } catch {}
  const want = manifest.go + ' ' + manifest.cache;
  const fresh = version !== want;
  if (fresh) {
    try { removeTree(fs, '/home/cache'); } catch {}
    fs.mkdirTree('/home/cache');
    untar(fs, '/home/cache', await bytes(gunzip(await fetchBody(manifest.cache, 'the build cache'))));
    fs.writeFile('/home/version', want);
  }
  untar(fs, '/goroot', await gorootP);
  fs.mkdirTree('/work');
  fs.writeFile('/work/go.mod', 'module play\n\ngo 1.25\n');
  post({ status: 'compiling the go command' });
  const go = await goP;
  note(`[ready in ${ms(t0)}${fresh ? '' : ', build cache from an earlier visit'}]`);
  post({ ready: manifest.version });
  return { go, fs };
})();
ready.catch((e) => {
  post({ line: 'failed to load the go command: ' + ((e && e.message) || e), fd: 2 });
  post({ status: 'failed' });
});

onmessage = async ({ data }) => {
  try {
    const { go, fs } = await ready;
    if (isGo(data.run)) {
      fs.writeFile('/work/main.go', data.run);
      try { fs.unlink('/work/main.c'); } catch {}
    } else {
      fs.writeFile('/work/main.c', data.run);
      fs.writeFile('/work/main.go', cMain);
    }
    try { fs.unlink('/work/main.wasm'); } catch {}
    const out = { stdout: (line) => post({ line, fd: 1 }), stderr: (line) => post({ line, fd: 2 }) };
    let t0 = performance.now();
    post({ build: 'building' });
    const code = await runGo(go, { args: ['go', 'build', '-o', '/work/main.wasm', '.'], env, fs, ...out });
    fs.sync().catch(() => {});
    if (code === 0) {
      const bin = fs.readFile('/work/main.wasm');
      post({ build: `built in ${ms(t0)} · ${(bin.length / 1024).toFixed(0)} KB` });
      t0 = performance.now();
      post({ status: 'running' });
      const run = await runGo(bin, { args: ['main'], env: { PWD: '/work', HOME: '/home' }, fs, ...out });
      note(`[exit ${run} after ${ms(t0)}]`);
    } else {
      post({ build: `build failed after ${ms(t0)}`, failed: true });
    }
  } catch (e) {
    // (Safari's stack does not have the message.)
    const stack = (e && e.stack) || '';
    post({ line: stack.includes(String(e)) ? stack : String(e) + (stack ? '\n' + stack : ''), fd: 2 });
    post({ build: 'failed', failed: true });
  }
  post({ finished: true });
};
