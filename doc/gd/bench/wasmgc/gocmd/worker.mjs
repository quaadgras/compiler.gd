// worker.mjs: the go command (out/go.wasm, GOOS=js) in a worker of
// index.html. Its files are in memory (memFS): GOROOT, unpacked from
// out/goroot.tar.gz, at /goroot; the program at /work; $HOME, with the
// build cache, at /home, which is kept in IndexedDB, so that a build
// after a reload compiles only what changed.
//
// Messages from the page: {build: source} builds main.go with that
// source and, if that works, runs it. To the page: {line, fd} for what
// the programs print, {status} for what is happening, {done} when a
// build (and run) is over.
import { runGo } from '../../../../../lib/wasm/wasm_gc_browser.js';
import { memFS } from '../../../../../lib/wasm/wasm_gc_fs.js';

const post = (m) => postMessage(m);
const status = (s) => post({ status: s });
const ms = (t0) => Math.round(performance.now() - t0) + ' ms';

// untar writes the files of a ustar archive into fs under dir.
function untar(fs, dir, tar) {
  const text = (o, n) => {
    let end = o;
    while (end < o + n && tar[end] !== 0) end++;
    return new TextDecoder().decode(tar.subarray(o, end));
  };
  let n = 0;
  for (let o = 0; o + 512 <= tar.length && tar[o] !== 0; ) {
    const name = [text(o + 345, 155), text(o, 100)].filter((s) => s).join('/');
    const size = parseInt(text(o + 124, 12).trim() || '0', 8);
    const kind = String.fromCharCode(tar[o + 156] || 48);
    o += 512;
    if (kind === '0') {
      const path = dir + '/' + name;
      fs.mkdirTree(path.slice(0, path.lastIndexOf('/')));
      fs.writeFile(path, tar.slice(o, o + size));
      n++;
    }
    o += Math.ceil(size / 512) * 512;
  }
  return n;
}

const env = {
  GOROOT: '/goroot',
  HOME: '/home',
  GOPATH: '/home/go',
  GOCACHE: '/home/cache',
  PWD: '/work',
  GOPROXY: 'off',
  GOTOOLCHAIN: 'local',
  GOTELEMETRY: 'off',
};

const ready = (async () => {
  let t0 = performance.now();
  status('loading the go command (out/go.wasm)…');
  const go = await WebAssembly.compileStreaming(fetch('out/go.wasm'));
  post({ fd: 0, line: `[go.wasm compiled in ${ms(t0)}]` });
  t0 = performance.now();
  status('unpacking GOROOT…');
  const fs = memFS();
  const tar = new Uint8Array(await new Response((await fetch('out/goroot.tar.gz')).body.pipeThrough(new DecompressionStream('gzip'))).arrayBuffer());
  const n = untar(fs, '/goroot', tar);
  fs.mkdirTree('/home');
  await fs.persist('/home');
  fs.mkdirTree('/work');
  fs.writeFile('/work/go.mod', 'module play\n\ngo 1.25\n');
  post({ fd: 0, line: `[GOROOT: ${n} files in ${ms(t0)}]` });
  return { go, fs };
})();

onmessage = async ({ data }) => {
  try {
    const { go, fs } = await ready;
    fs.writeFile('/work/main.go', data.build);
    let t0 = performance.now();
    status('go build…');
    const out = { stdout: (line) => post({ line, fd: 1 }), stderr: (line) => post({ line, fd: 2 }) };
    const code = await runGo(go, { args: ['go', 'build', '-o', '/work/main.wasm', '.'], env, fs, ...out });
    await fs.sync();
    post({ fd: 0, line: `[go build: exit ${code} after ${ms(t0)}]` });
    if (code === 0) {
      t0 = performance.now();
      status('running…');
      const bin = fs.readFile('/work/main.wasm');
      const run = await runGo(bin, { args: ['main'], env: { PWD: '/work' }, fs, ...out });
      post({ fd: 0, line: `[main.wasm (${bin.length} bytes): exit ${run} after ${ms(t0)}]` });
    }
  } catch (e) {
    post({ line: String((e && e.stack) || e), fd: 2 });
  }
  status('');
  post({ done: true });
};
