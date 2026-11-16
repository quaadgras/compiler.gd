// memfs.mjs prog.wasm args...: run a GOWASM=gc program under node as a
// browser would (lib/wasm/wasm_gc_browser.js), with its files in memory
// (wasm_gc_fs.js): a copy of the working directory, and little else of
// the machine's. For the tests of the file system:
//
//	GOOS=wasip1 GOARCH=wasm GOWASM=gc go test -exec "node --stack-size=4000 $PWD/memfs.mjs" os
//
// With GOWASMFS=emscripten the files are those of an Emscripten program
// (its FS), built from fs_emscripten.c: see there.
import fs from 'node:fs';
import path from 'node:path';
import v8 from 'node:v8';
import { runGo } from '../../../../../lib/wasm/wasm_gc_browser.js';
import { memFS } from '../../../../../lib/wasm/wasm_gc_fs.js';

// (As lib/wasm/wasm_gc_node.mjs.)
v8.setFlagsFromString('--wasm-stack-switching-stack-size=8192');
v8.setFlagsFromString('--no-liftoff');

let files;
if (process.env.GOWASMFS === 'emscripten') {
  const { default: init } = await import(process.env.GOWASMFS_MODULE);
  files = (await init()).FS;
} else {
  files = memFS();
}
const cwd = process.cwd();
files.mkdirTree(cwd);
const copy = (dir) => {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      files.mkdir(p);
      copy(p);
    } else if (e.isSymbolicLink()) {
      files.symlink(fs.readlinkSync(p), p);
    } else if (e.isFile()) {
      files.writeFile(p, fs.readFileSync(p));
    }
  }
};
copy(cwd);
// (And the files at the top of GOROOT, which the tests of os look for.)
const goroot = process.env.GOROOT;
if (goroot && cwd.startsWith(goroot + '/')) {
  for (const e of fs.readdirSync(goroot, { withFileTypes: true })) {
    if (e.isFile()) files.writeFile(path.join(goroot, e.name), fs.readFileSync(path.join(goroot, e.name)));
  }
}
const code = await runGo(fs.readFileSync(process.argv[2]), {
  args: process.argv.slice(2),
  env: { ...process.env, PWD: cwd, TMPDIR: '/tmp' },
  fs: files,
  stdout: (line) => console.log(line),
  stderr: (line) => console.error(line),
  reserve: 0,
});
process.exit(code);
