import { readFileSync } from "node:fs";
const { instance } = await WebAssembly.instantiate(readFileSync(new URL("./linear.wasm", import.meta.url)));
const e = instance.exports;
let t0 = performance.now();
const sum = e.trees(16);
console.log("trees: check", sum, "in", Math.round(performance.now() - t0), "ms");
e.setup();
const frames = 2000, times = new Float64Array(frames);
let check = Math.fround(0);
t0 = performance.now();
for (let f = 0; f < frames; f++) {
  const s = performance.now();
  check = Math.fround(check + e.frame(f));
  times[f] = performance.now() - s;
}
const total = performance.now() - t0;
times.sort();
console.log("frames: check", check, "health", e.health(), "total", Math.round(total), "ms");
console.log("frame ms: median", times[frames / 2].toFixed(4), "p99", times[frames * 99 / 100 | 0].toFixed(4), "max", times[frames - 1].toFixed(4));
console.log("rss MB", (process.memoryUsage().rss / 1e6).toFixed(0));
