import { readFileSync } from "node:fs";
let freed = 0;
const registry = new FinalizationRegistry((addr) => { freed++; });
const { instance } = await WebAssembly.instantiate(readFileSync(new URL("./fin.wasm", import.meta.url)), {
  env: { register: (h, addr) => registry.register(h, addr), get: (buf, i) => buf[i] },
});
const e = instance.exports;
// 1. can a wasm GC object be registered, and is the callback run after it dies?
e.alloc_registered(1000);
for (let i = 0; i < 5; i++) { global.gc(); await new Promise((r) => setTimeout(r, 20)); }
console.log(`finalizers run for ${freed} of 1000 dead wasm GC objects`);
// 2. cost of registering
const n = 1000000;
let t0 = performance.now(); e.alloc_plain(n); const plain = performance.now() - t0;
t0 = performance.now(); e.alloc_registered(n); const reg = performance.now() - t0;
console.log(`allocate handle: ${(plain * 1e6 / n).toFixed(1)} ns; allocate + register for finalization: ${(reg * 1e6 / n).toFixed(0)} ns`);
// 3. element access: a JS typed array through an import vs linear memory
const len = 1 << 20, reps = 20;
const buf = new Uint8Array(len).fill(3);
new Uint8Array(e.mem.buffer).fill(3, 0, len);
e.sum_js_buffer(buf, len); e.sum_linear(0, len);
t0 = performance.now(); for (let r = 0; r < reps; r++) e.sum_js_buffer(buf, len); const js = performance.now() - t0;
t0 = performance.now(); for (let r = 0; r < reps; r++) e.sum_linear(0, len); const lin = performance.now() - t0;
console.log(`byte sum: JS typed array via import ${(js * 1e6 / len / reps).toFixed(2)} ns/byte, linear memory ${(lin * 1e6 / len / reps).toFixed(2)} ns/byte (${(js / lin).toFixed(0)}x)`);
