// fs_test.mjs: check in Chromium that a Go program's files are an
// Emscripten program's (needs go and emcc on the PATH):
//
//	GOROOT=... node fs_test.mjs
//
// A page has an Emscripten program (fs_emscripten.c) with IDBFS mounted
// at /data, and runs a Go program (fs_prog.go) three times: with /data
// kept in IndexedDB (runGo's persist), which must have what Emscripten
// stored there, and Emscripten what Go then stores; again, with new
// files in memory; and with the Emscripten program's FS as its files.
// It prints what each saw: fs_test.expected.txt has what that should be.
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';

const here = path.dirname(new URL(import.meta.url).pathname);
const lib = path.join(here, '../../../../../lib/wasm');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'go-fs-test-'));
const env = { ...process.env, GOOS: 'wasip1', GOARCH: 'wasm', GOWASM: 'gc', NODE_OPTIONS: '' };
execFileSync('go', ['build', '-o', path.join(tmp, 'prog.wasm'), path.join(here, 'fs_prog.go')], { env, stdio: 'inherit' });
execFileSync('emcc', [path.join(here, 'fs_emscripten.c'), '-o', path.join(tmp, 'em.mjs'), '-sMODULARIZE', '-sEXPORT_ES6',
  '-sENVIRONMENT=web,node,worker', '-sEXPORTED_RUNTIME_METHODS=FS,IDBFS', '-lidbfs.js'], { env: { ...process.env, NODE_OPTIONS: '' }, stdio: 'inherit' });

const page = `<!doctype html><meta charset="utf-8"><script type="module">
import init from '/em.mjs';
import { runGo } from '/wasm_gc_browser.js';
const lines = [];
const print = (l) => lines.push(l);
try {
  const M = await init();
  const FS = M.FS;
  const syncfs = (populate) => new Promise((done, fail) => FS.syncfs(populate, (err) => (err ? fail(err) : done())));
  FS.mkdir('/data');
  FS.mount(M.IDBFS, {}, '/data');
  FS.writeFile('/data/from_emscripten.txt', 'written by Emscripten\\n');
  FS.mkdir('/data/sub');
  FS.writeFile('/data/sub/x.bin', new Uint8Array([1, 2, 3]));
  await syncfs(false);
  const module = await WebAssembly.compileStreaming(fetch('/prog.wasm'));
  const go = (arg, options) => runGo(module, { args: ['prog', arg], stdout: print, stderr: print, ...options });

  print('exit ' + await go('kept', { persist: ['/data'] }));
  await syncfs(true);
  print('emscripten reads: ' + FS.readFile('/data/from_go.txt', { encoding: 'utf8' }).trim() + ' | ' + FS.readFile('/data/sub/go/deep.txt', { encoding: 'utf8' }).trim());
  print('emscripten lists /data/sub: ' + FS.readdir('/data/sub').filter((n) => n[0] !== '.').sort().join(' '));

  print('exit ' + await go('again', { persist: ['/data'] }));

  print('exit ' + await go('live', { fs: FS }));
  print('emscripten reads live: ' + FS.readFile('/tmp/go_live.txt', { encoding: 'utf8' }).trim());
  print('emscripten lists /data: ' + FS.readdir('/data').filter((n) => n[0] !== '.').sort().join(' '));
} catch (e) {
  print('error: ' + (e && e.stack || e));
}
await fetch('/done', { method: 'POST', body: lines.join('\\n') });
</script>`;

let browser;
const finish = (code) => {
  try { process.kill(-browser.pid, 'SIGKILL'); } catch {}
  for (let i = 0; i < 20; i++) {
    try {
      fs.rmSync(tmp, { recursive: true, force: true });
      break;
    } catch {}
  }
  process.exit(code);
};
const types = { '.js': 'text/javascript', '.mjs': 'text/javascript', '.wasm': 'application/wasm' };
const server = http.createServer((req, res) => {
  if (req.url === '/done') {
    let text = '';
    req.on('data', (d) => { text += d; });
    req.on('end', () => {
      res.end();
      console.log(text);
      const want = fs.readFileSync(path.join(here, 'fs_test.expected.txt'), 'utf8').trim();
      if (text.trim() !== want) console.error('\nFAIL: not what fs_test.expected.txt has');
      finish(text.trim() === want ? 0 : 1);
    });
    return;
  }
  const name = req.url.slice(1);
  const file = name === '' ? null : [path.join(tmp, name), path.join(lib, name)].find((f) => fs.existsSync(f));
  if (name !== '' && !file) {
    res.writeHead(404);
    return res.end();
  }
  res.writeHead(200, { 'Content-Type': file ? types[path.extname(file)] : 'text/html' });
  res.end(file ? fs.readFileSync(file) : page);
});
server.listen(0, '127.0.0.1', () => {
  browser = spawn(process.env.GOWASMBROWSER || 'chromium', ['--headless=new', '--user-data-dir=' + path.join(tmp, 'profile'), '--no-first-run',
    '--disable-gpu', '--disable-gpu-sandbox', `http://127.0.0.1:${server.address().port}/`], { stdio: 'ignore', detached: true });
});
setTimeout(() => {
  console.error('FAIL: the page did not finish');
  finish(1);
}, 60000);
