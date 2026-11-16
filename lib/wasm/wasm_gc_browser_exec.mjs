// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasm_gc_browser_exec.mjs prog.wasm args...: run a GOWASM=gc program in
// a browser, as wasm_gc_node.mjs runs it under node. go_wasip1_wasm_exec
// runs it for go run and go test when GOWASIRUNTIME names a browser
// (chromium or firefox; GOWASIRUNTIMEARGS has more arguments for it):
//
//	GOOS=wasip1 GOARCH=wasm GOWASM=gc GOWASIRUNTIME=chromium go test strings
//
// A headless browser is started with a profile of its own and loads a
// page, from a server on this machine, that runs the program in a
// worker with wasm_gc_browser.js. The program has the files, the
// arguments, the environment and the standard input and output it has
// under node: the worker asks the server for each call of the system
// interface that needs them, and waits for the answer (which only a
// worker may do), and the server makes the call with node's. The calls
// go through memory the worker shares with a second worker, which has
// a WebSocket to the server: a synchronous request, which is used for
// what does not fit there, takes ten times as long.
//
// The browser is as its users have it: no flags of the engine are set
// (GOWASIRUNTIMEARGS='--js-flags=--expose-gc', for one, gives
// runtime.GC a collector, as it has under node).
import { spawn } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { nodeWASI } from './wasm_gc_wasi_node.mjs';

const here = path.dirname(new URL(import.meta.url).pathname);
const kind = process.env.GOWASIRUNTIME || 'chromium';
const args = process.argv.slice(2);
if (args.length === 0) {
  console.error('usage: wasm_gc_browser_exec.mjs prog.wasm args...');
  process.exit(2);
}

// The system interface is node's, working on a memory of the server's:
// what a call reads is put where the program has it, and what it writes
// is sent back from there.
const wasi = nodeWASI(args);
const memory = new WebAssembly.Memory({ initial: 1, maximum: 65536 });
wasi.start(memory);
// (With room after what a call is given: node's reads past a path that
// ends where the memory does.)
const ensure = (end) => {
  const have = memory.buffer.byteLength;
  if (end + 4096 > have) memory.grow(Math.ceil((end + 4096 - have) / 65536));
};
const ENOSYS = 52;

// The page, and the worker that runs the program.
const page = `<!doctype html>
<meta charset="utf-8">
<title>go test</title>
<script type="module">
const fail = (text) => fetch('/fail', { method: 'POST', body: text });
const worker = new Worker('/worker.js', { type: 'module' });
worker.onerror = (e) => fail('worker: ' + (e.message || 'failed to load'));
worker.onmessageerror = () => fail('worker: message error');
// (The server knows by these that the page is alive: a page that
// crashed says nothing.)
setInterval(() => fetch('/alive', { method: 'POST' }), 1000);
</script>
`;

const worker = `import { runGo } from '/wasm_gc_browser.js';

// The memory shared with the worker that talks to the server (io): a
// state (1 while a call is made), a length, and the bytes of the call,
// then of its answer.
const shared = new SharedArrayBuffer(8 + (4 << 20));
const state = new Int32Array(shared, 0, 2), data = new Uint8Array(shared, 8);
const io = new Worker('/io.js');
await new Promise((ready) => { io.onmessage = ready; io.postMessage(shared); });

// call makes a call of the system interface on the server: ins are the
// parts of memory ([address, length]) it reads, outs those it writes.
function call(mem, name, args, ins = [], outs = []) {
  const head = new TextEncoder().encode(JSON.stringify({ name, args: args.map((a) => (typeof a === 'bigint' ? String(a) : a)), ins, outs }));
  const size = 4 + head.length + ins.reduce((n, [, l]) => n + l, 0);
  const fits = Math.max(size, 4 + outs.reduce((n, [, l]) => n + l, 0)) <= data.length;
  const body = fits ? data : new Uint8Array(size);
  new DataView(body.buffer, body.byteOffset).setUint32(0, head.length, true);
  body.set(head, 4);
  let at = 4 + head.length;
  for (const [p, l] of ins) {
    body.set(new Uint8Array(mem(), p, l), at);
    at += l;
  }
  let res = data;
  if (fits) {
    state[1] = size;
    Atomics.store(state, 0, 1);
    Atomics.notify(state, 0);
    Atomics.wait(state, 0, 1);
  } else {
    const req = new XMLHttpRequest();
    req.open('POST', '/sys', false);
    req.responseType = 'arraybuffer';
    req.send(body);
    res = new Uint8Array(req.response);
  }
  const errno = new DataView(res.buffer, res.byteOffset).getInt32(0, true);
  // (A call that fails writes nothing.)
  at = 4;
  for (const [p, l] of errno === 0 ? outs : []) {
    new Uint8Array(mem(), p, l).set(res.subarray(at, at + l));
    at += l;
  }
  return errno;
}

// The memory each call reads and writes, by its arguments (a).
const part = (p, n) => [p >>> 0, n >>> 0];
const iovecs = (mem, p, n) => {
  const v = new DataView(mem()), list = [];
  for (let i = 0; i < n; i++) list.push(part(v.getUint32(p + 8 * i, true), v.getUint32(p + 8 * i + 4, true)));
  return list;
};
const name = (i) => (mem, a) => [[part(a[i], a[i + 1])], []];
const names = (i, j) => (mem, a) => [[part(a[i], a[i + 1]), part(a[j], a[j + 1])], []];
const reads = (np) => (mem, a) => [[part(a[1], 8 * a[2])], [...iovecs(mem, a[1], a[2]), part(a[np], 4)]];
const writes = (np) => (mem, a) => [[part(a[1], 8 * a[2]), ...iovecs(mem, a[1], a[2])], [part(a[np], 4)]];
const calls = {
  fd_advise: null, fd_allocate: null, fd_close: null, fd_datasync: null, fd_sync: null,
  fd_fdstat_set_flags: null, fd_fdstat_set_rights: null, fd_filestat_set_size: null,
  fd_filestat_set_times: null, fd_renumber: null, sock_shutdown: null,
  fd_fdstat_get: (mem, a) => [[], [part(a[1], 24)]],
  fd_filestat_get: (mem, a) => [[], [part(a[1], 64)]],
  fd_prestat_get: (mem, a) => [[], [part(a[1], 8)]],
  fd_prestat_dir_name: (mem, a) => [[], [part(a[1], a[2])]],
  fd_read: reads(3), fd_pread: reads(4),
  fd_write: writes(3), fd_pwrite: writes(4),
  fd_readdir: (mem, a) => [[], [part(a[1], a[2]), part(a[4], 4)]],
  fd_seek: (mem, a) => [[], [part(a[3], 8)]],
  fd_tell: (mem, a) => [[], [part(a[1], 8)]],
  path_create_directory: name(1), path_remove_directory: name(1), path_unlink_file: name(1),
  path_filestat_get: (mem, a) => [[part(a[2], a[3])], [part(a[4], 64)]],
  path_filestat_set_times: name(2),
  path_link: names(2, 5), path_rename: names(1, 4), path_symlink: names(0, 3),
  path_open: (mem, a) => [[part(a[2], a[3])], [part(a[8], 4)]],
  path_readlink: (mem, a) => [[part(a[1], a[2])], [part(a[3], a[4]), part(a[5], 4)]],
  poll_oneoff: (mem, a) => [[part(a[0], 48 * a[2])], [part(a[1], 32 * a[2]), part(a[3], 4)]],
  sock_accept: (mem, a) => [[], [part(a[2], 4)]],
};

const config = await (await fetch('/config')).json();
// (A line the runtime prints, on the standard error.)
const none = () => shared;
const send = (fd, line) => call(none, '$write', [fd, line + '\\n']);
let code = 2;
try {
  code = await runGo('/prog.wasm', {
    args: config.args,
    env: config.env,
    stdout: (line) => send(1, line),
    stderr: (line) => send(2, line),
    reserve: config.reserve,
    collect: typeof gc === 'function' ? (major) => (major ? gc() : gc({ type: 'minor' })) : null,
    wasi: (mem) => Object.fromEntries(Object.entries(calls).map(([n, parts]) => [n, (...a) => call(mem, n, a, ...(parts ? parts(mem, a) : []))])),
  });
} catch (e) {
  send(2, String(e && e.stack || e));
}
call(none, '$exit', [code]);
`;

// The worker that makes the calls of the first, which waits.
const ioWorker = `onmessage = async (e) => {
  const state = new Int32Array(e.data, 0, 2), data = new Uint8Array(e.data, 8);
  const ws = new WebSocket('ws://' + location.host + '/sys');
  ws.binaryType = 'arraybuffer';
  await new Promise((open) => { ws.onopen = open; });
  const next = async () => {
    const w = Atomics.waitAsync(state, 0, 0);
    if (w.async) await w.value;
    ws.send(data.slice(0, state[1]));
  };
  ws.onmessage = (m) => {
    data.set(new Uint8Array(m.data));
    Atomics.store(state, 0, 0);
    Atomics.notify(state, 0);
    next();
  };
  postMessage('ready');
  next();
};
`;

// The browser, with a profile of its own.
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'go-browser-'));
let browser, done = false;
const kill = () => {
  if (browser) {
    try { process.kill(-browser.pid, 'SIGKILL'); } catch {}
  }
};
function exit(code) {
  if (done) return;
  done = true;
  kill();
  // (The browser's processes may still write to the profile.)
  for (let i = 0; i < 20; i++) {
    try {
      fs.rmSync(profile, { recursive: true, force: true });
      break;
    } catch {}
  }
  process.exitCode = code;
  // (What was written to the standard output is flushed first.)
  process.stdout.write('', () => process.exit(code));
}
for (const [sig, code] of [['SIGINT', 130], ['SIGTERM', 143], ['SIGHUP', 129], ['SIGQUIT', 131]]) process.on(sig, () => exit(code));

// sys makes the call of the system interface that data has (see call in
// the worker) and returns its answer: the error and what it wrote.
function sys(data) {
  const n = data.readUInt32LE(0);
  const call = JSON.parse(data.subarray(4, 4 + n).toString());
  if (call.name === '$write') {
    fs.writeSync(call.args[0] === 1 ? 1 : 2, call.args[1]);
    return Buffer.alloc(4);
  }
  if (call.name === '$exit') {
    setImmediate(() => exit(call.args[0]));
    return Buffer.alloc(4);
  }
  let at = 4 + n, size = 4;
  for (const [p, l] of call.ins) {
    ensure(p + l);
    new Uint8Array(memory.buffer, p, l).set(data.subarray(at, at + l));
    at += l;
  }
  for (const [p, l] of call.outs) {
    ensure(p + l);
    size += l;
  }
  let errno = ENOSYS;
  const f = wasi.imports[call.name];
  if (typeof f === 'function') {
    errno = f(...call.args.map((a) => (typeof a === 'string' ? BigInt(a) : a)));
  }
  const out = Buffer.alloc(size);
  out.writeInt32LE(errno, 0);
  at = 4;
  for (const [p, l] of call.outs) {
    out.set(new Uint8Array(memory.buffer, p, l), at);
    at += l;
  }
  return out;
}

const body = (req) => new Promise((done) => {
  const parts = [];
  req.on('data', (d) => parts.push(d));
  req.on('end', () => done(Buffer.concat(parts)));
});
const types = { '.js': 'text/javascript', '.wasm': 'application/wasm', '.html': 'text/html' };
// (The page is isolated from other origins: memory can be shared
// between its workers.)
const isolated = { 'Cross-Origin-Opener-Policy': 'same-origin', 'Cross-Origin-Embedder-Policy': 'require-corp', 'Cross-Origin-Resource-Policy': 'same-origin' };
let alive = Date.now(), started = false;
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const send = (type, data) => {
    res.writeHead(200, { 'Content-Type': type, 'Cache-Control': 'no-store', ...isolated });
    res.end(data);
  };
  alive = Date.now();
  switch (url.pathname) {
    case '/':
      return send(types['.html'], page);
    case '/worker.js':
      return send(types['.js'], worker);
    case '/io.js':
      return send(types['.js'], ioWorker);
    case '/wasm_gc.js': case '/wasm_gc_browser.js':
      return send(types['.js'], fs.readFileSync(path.join(here, url.pathname)));
    case '/prog.wasm':
      return send(types['.wasm'], fs.readFileSync(args[0]));
    case '/config':
      started = true;
      return send('application/json', JSON.stringify({
        args,
        env: { ...process.env, PWD: process.cwd(), GOWASIRUNTIME: 'node' },
        reserve: process.env.GOWASMRESERVE ? Number(process.env.GOWASMRESERVE) << 20 : undefined,
      }));
    case '/alive':
      return send('text/plain', '');
    case '/fail': {
      fs.writeSync(2, (await body(req)).toString() + '\n');
      send('text/plain', '');
      return exit(2);
    }
    case '/sys':
      return send('application/octet-stream', sys(await body(req)));
  }
  res.writeHead(404);
  res.end();
});

// The WebSocket of the worker's calls: each message is a call, and is
// answered with one.
server.on('upgrade', (req, socket) => {
  const accept = crypto.createHash('sha1').update(req.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
  socket.setNoDelay(true);
  let buf = Buffer.alloc(0), message = [];
  socket.on('error', () => {});
  socket.on('data', (d) => {
    alive = Date.now();
    buf = buf.length ? Buffer.concat([buf, d]) : d;
    for (;;) {
      if (buf.length < 2) return;
      let n = buf[1] & 0x7f, at = 2;
      if (n === 126) {
        if (buf.length < 4) return;
        n = buf.readUInt16BE(2);
        at = 4;
      } else if (n === 127) {
        if (buf.length < 10) return;
        n = Number(buf.readBigUInt64BE(2));
        at = 10;
      }
      if (buf.length < at + 4 + n) return;
      const mask = buf.subarray(at, at + 4), data = Buffer.from(buf.subarray(at + 4, at + 4 + n));
      for (let i = 0; i < n; i++) data[i] ^= mask[i & 3];
      const op = buf[0] & 0x0f, fin = buf[0] & 0x80;
      buf = buf.subarray(at + 4 + n);
      if (op === 8) return socket.end();
      if (op > 2) continue; // (a ping or a pong)
      message.push(data);
      if (!fin) continue;
      const out = sys(message.length === 1 ? data : Buffer.concat(message));
      message = [];
      const head = out.length < 126 ? Buffer.from([0x82, out.length])
        : out.length < 65536 ? Buffer.from([0x82, 126, out.length >> 8, out.length & 0xff])
        : Buffer.concat([Buffer.from([0x82, 127]), Buffer.alloc(8)]);
      if (out.length >= 65536) head.writeBigUInt64BE(BigInt(out.length), 2);
      socket.write(Buffer.concat([head, out]));
    }
  });
});

server.listen(0, '127.0.0.1', () => {
  const url = `http://127.0.0.1:${server.address().port}/`;
  const more = (process.env.GOWASIRUNTIMEARGS || '').split(/\s+/).filter(Boolean);
  let cmd, argv;
  if (kind === 'firefox') {
    // (JSPI is not yet on by default.)
    fs.writeFileSync(path.join(profile, 'user.js'), [
      'user_pref("javascript.options.wasm_js_promise_integration", true);',
      'user_pref("browser.shell.checkDefaultBrowser", false);',
      'user_pref("datareporting.policy.dataSubmissionEnabled", false);',
      'user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);',
      'user_pref("app.update.enabled", false);',
      '',
    ].join('\n'));
    cmd = process.env.GOWASMBROWSER || 'firefox';
    argv = ['--headless', '--no-remote', '--profile', profile, ...more, url];
  } else {
    cmd = process.env.GOWASMBROWSER || ['chromium', 'chromium-browser', 'google-chrome', 'google-chrome-stable'].find((c) =>
      (process.env.PATH || '').split(':').some((d) => fs.existsSync(path.join(d, c)))) || 'chromium';
    argv = ['--headless=new', '--user-data-dir=' + profile, '--no-first-run', '--no-default-browser-check',
      // (Nothing is drawn; and where the GPU process cannot start in its
      // sandbox, the browser gives up.)
      '--disable-gpu', '--disable-gpu-sandbox',
      '--disable-extensions', '--disable-background-networking', '--disable-component-update', '--disable-sync',
      // (The page is not on a screen: its timers must not be slowed.)
      '--disable-background-timer-throttling', '--disable-renderer-backgrounding', '--disable-backgrounding-occluded-windows',
      ...more, url];
  }
  let launched, tries = 0;
  const launch = () => {
    tries++;
    launched = Date.now();
    const b = spawn(cmd, argv, { stdio: ['ignore', 'ignore', process.env.GOWASMBROWSERLOG ? 'inherit' : 'ignore'], detached: true });
    browser = b;
    b.on('error', (err) => {
      console.error(`${cmd}: ${err.message}`);
      exit(2);
    });
    b.on('exit', (code) => {
      if (!done && browser === b) {
        console.error(`${cmd}: exited (${code}) before the program did`);
        exit(2);
      }
    });
    // If this process is killed, the browser must not stay: a shell
    // waits for the one or the other to end.
    spawn('sh', ['-c', `while kill -0 ${process.pid} 2>/dev/null && kill -0 ${b.pid} 2>/dev/null; do sleep 1; done; kill -9 -${b.pid} 2>/dev/null; kill -0 ${process.pid} 2>/dev/null || rm -rf "${profile}"`],
      { stdio: 'ignore', detached: true }).unref();
  };
  launch();
  // A page that stops saying it is alive has crashed (the engine
  // aborted, or ran out of memory). A browser whose page does not load
  // (which happens, on a busy machine) is started again.
  setInterval(() => {
    if (done) return;
    if (!started && Date.now() - launched > 20000) {
      if (tries === 3) {
        console.error(`${cmd}: the page did not load`);
        return exit(2);
      }
      const old = browser;
      browser = null;
      try { process.kill(-old.pid, 'SIGKILL'); } catch {}
      launch();
    } else if (started && Date.now() - alive > 10000) {
      console.error(`${cmd}: the page crashed`);
      exit(2);
    }
  }, 1000).unref();
});
