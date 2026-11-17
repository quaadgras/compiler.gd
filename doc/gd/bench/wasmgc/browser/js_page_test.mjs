// js_page_test.mjs: check in a browser that a GOOS=js program runs in a
// page (lib/wasm/wasm_gc_browser.js's runGo), with the page's document,
// callbacks both ways and files (needs go on the PATH):
//
//	GOROOT=... node js_page_test.mjs [chromium|firefox]
//
// GOWASM (gc by default, or gc,split) is the model. The page runs
// js_page.go and prints what it printed and what the page then sees:
// js_page_test.expected.txt has what that should be.
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';

const here = path.dirname(new URL(import.meta.url).pathname);
const lib = path.join(here, '../../../../../lib/wasm');
const kind = process.argv[2] || 'chromium';
// (The name marks the browser's processes as this test's.)
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'go-browser-jspage-'));
const env = { ...process.env, GOOS: 'js', GOARCH: 'wasm', GOWASM: process.env.GOWASM || 'gc', NODE_OPTIONS: '' };
execFileSync('go', ['build', '-o', path.join(tmp, 'prog.wasm'), path.join(here, 'js_page.go')], { env, stdio: 'inherit' });

const page = `<!doctype html><meta charset="utf-8"><body><script type="module">
import { runGo } from '/wasm_gc_browser.js';
import { memFS } from '/wasm_gc_fs.js';
const lines = [];
const print = (l) => lines.push(l);
try {
  const files = memFS();
  print('exit ' + await runGo('/prog.wasm', { stdout: print, stderr: print, fs: files, files: { '/data/in.txt': 'from the page' } }));
  print('page sees: ' + document.getElementById('greeting').textContent + ' | ' + document.querySelector('button').textContent + ' | ' + files.readFile(globalThis.goWrote, { encoding: 'utf8' }));
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
      const want = fs.readFileSync(path.join(here, 'js_page_test.expected.txt'), 'utf8').trim();
      if (text.trim() !== want) console.error('\nFAIL: not what js_page_test.expected.txt has');
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
  const url = `http://127.0.0.1:${server.address().port}/`;
  const profile = path.join(tmp, 'profile');
  fs.mkdirSync(profile);
  let cmd, argv;
  if (kind === 'firefox') {
    // (JSPI is not yet on by default.)
    fs.writeFileSync(path.join(profile, 'user.js'), 'user_pref("javascript.options.wasm_js_promise_integration", true);\n');
    cmd = 'firefox';
    argv = ['--headless', '--no-remote', '--profile', profile, url];
  } else {
    cmd = 'chromium';
    argv = ['--headless=new', '--user-data-dir=' + profile, '--no-first-run', '--disable-gpu', '--disable-gpu-sandbox', url];
  }
  browser = spawn(process.env.GOWASMBROWSER || cmd, argv, { stdio: 'ignore', detached: true });
});
setTimeout(() => {
  console.error('FAIL: the page did not finish');
  finish(1);
}, 60000);
