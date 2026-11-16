import { readFileSync } from "node:fs";
const { instance } = await WebAssembly.instantiate(readFileSync(new URL("./gc.wasm", import.meta.url)));
const e = instance.exports;
const time = (f) => { let best = Infinity, r; for (let i = 0; i < 5; i++) { const t0 = performance.now(); r = f(); best = Math.min(best, performance.now() - t0); } return [best, r]; };
const row = (name, linear, gc) => console.log(name.padEnd(46), `linear ${linear[0].toFixed(1).padStart(7)} ms`, ...Object.entries(gc).map(([k, v]) => `  ${k} ${v[0].toFixed(1).padStart(7)} ms (${(v[0] / linear[0]).toFixed(2)}x)`));

// A. linked list of 1e6 nodes, traversed 50 times
{
  const n = 1000000, reps = 50;
  e.lin_reset();
  const tb = time(() => { e.lin_reset(); return e.lin_list_build(n); });
  const head = e.lin_list_build(n);
  const gb = time(() => e.gc_list_build(n));
  row("A. list build, 1e6 nodes", tb, { "wasm GC": gb });
  const lin = time(() => e.lin_list_sum(head, reps));
  const gc = time(() => e.gc_list_sum(reps));
  if (lin[1] !== gc[1]) throw new Error("A mismatch");
  row("A. list traverse x50", lin, { "wasm GC": gc });
}
// F. the same list as universal objects
{
  const n = 1000000, reps = 50;
  const ub = time(() => e.uni_list_build(n));
  const lin = time(() => e.lin_list_sum(e.lin_list_build(1) && (e.lin_reset(), e.lin_list_build(n)), reps));
  const uni = time(() => e.uni_list_sum(reps));
  row("F. list traverse x50, universal objects", lin, { "universal": uni });
  console.log("F. list build 1e6, universal objects".padEnd(46), `${ub[0].toFixed(1)} ms`);
  const n2 = 20000000;
  const ug = time(() => e.uni_alloc_garbage(n2));
  console.log("F. 2e7 short-lived universal objects".padEnd(46), `${ug[0].toFixed(1)} ms = ${(ug[0] * 1e6 / n2).toFixed(1)} ns each`);
}
// H, I. leaner universal variants
{
  const n = 1000000, reps = 50;
  e.lin_reset();
  const head = e.lin_list_build(n);
  const lin = time(() => e.lin_list_sum(head, reps));
  const b2 = time(() => e.uni2_list_build(n));
  const u2 = time(() => e.uni2_list_sum(reps));
  const b3 = time(() => e.uni3_list_build(n));
  const u3 = time(() => e.uni3_list_sum(reps));
  row("H/I. list traverse x50, lean universal", lin, { "typed refs, byte offsets": u2, "pointer = (words, refs)": u3 });
  console.log("H/I. list build 1e6".padEnd(46), `typed refs ${b2[0].toFixed(1)} ms   (words, refs) ${b3[0].toFixed(1)} ms`);
}
// J. typed objects with tagged pointers
{
  const n = 1000000, reps = 50;
  e.lin_reset();
  const head = e.lin_list_build(n);
  const lin = time(() => e.lin_list_sum(head, reps));
  const b = time(() => e.tag_list_build(n));
  const fast = time(() => e.tag_list_sum(reps));
  const slow = time(() => e.tag_list_sum_interior(reps));
  if (fast[1] !== lin[1] || slow[1] !== lin[1]) throw new Error("J mismatch");
  row("J. list traverse x50, tagged pointers", lin, { "whole-object (fast path)": fast, "interior (accessor call)": slow });
  console.log("J. list build 1e6, tagged".padEnd(46), `${b[0].toFixed(1)} ms`);
}
// G. sieve over 2e7 bytes
{
  const len = 20000000, base = 1 << 28;
  const lin = time(() => { new Uint8Array(e.mem.buffer).fill(0, base, base + len); return e.lin_sieve(base, len); });
  const gc = time(() => e.gc_sieve(len));
  const packed = time(() => e.packed_sieve(len));
  if (lin[1] !== gc[1] || lin[1] !== packed[1]) throw new Error("G mismatch " + lin[1] + " " + gc[1] + " " + packed[1]);
  row("G. sieve over 2e7 bytes", lin, { "GC i8 array": gc, "bytes in i64 words": packed });
}
// B. []int64 of 1e6, increment and sum, 100 times
{
  const len = 1000000, reps = 100, base = 1 << 26;
  e.gc_words_new(len);
  const lin = time(() => e.lin_words_sum(base, len, reps));
  const gc = time(() => e.gc_words_sum(len, reps));
  row("B. []int64 increment+sum x100", lin, { "wasm GC array": gc });
}
// C. []struct of 250k x 4 words, p := &s[i]; p.b = p.a + p.c + 1, 200 times
{
  const len = 250000, reps = 200, base = 1 << 27;
  e.gc_words_new(len * 4);
  e.gc_boxed_new(len);
  const lin = time(() => e.lin_structs(base, len, reps));
  const flat = time(() => e.gc_flat_structs(len, reps));
  const boxed = time(() => e.gc_boxed_structs(len, reps));
  row("C. []struct field update via &s[i] x200", lin, { "GC flat words": flat, "GC boxed structs": boxed });
}
// D. read uint64 at every offset of a 1 MB []byte, 50 times
{
  const len = 1 << 20, reps = 50, base = 1 << 28;
  e.gc_bytes_new(len);
  const lin = time(() => e.lin_bytes_u64(base, len, reps));
  const gc = time(() => e.gc_bytes_u64(len, reps));
  row("D. uint64 from []byte at every offset x50", lin, { "wasm GC i8 array": gc });
}
// E. 2e7 short-lived 16-byte objects
{
  const n = 20000000;
  const gc = time(() => e.gc_alloc_garbage(n));
  console.log("E. allocate 2e7 short-lived objects".padEnd(46), `wasm GC ${gc[0].toFixed(1)} ms = ${(gc[0] * 1e6 / n).toFixed(1)} ns each`);
}
