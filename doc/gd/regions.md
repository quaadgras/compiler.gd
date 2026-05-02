# Regions — generalising Phase G to dynamic memory

Status: design draft, 2026-05-03.
Companion to: `escape-bits.md`, `escape-bits-phase-g-plan.md`.

## Problem

Phase G already lets a callee allocate a single fixed-size pointer-typed
result into caller-provided storage:

```go
// Caller, when escape analysis says the result doesn't escape:
var buf T
p := f(&buf)               // f writes into buf, returns &buf
// vs. when result escapes:
p := f(nil)                // f mallocs, returns the heap pointer
```

The runtime helper `maybeInPlace(outBuf unsafe.Pointer, typ *abi.Type)`
chooses between zero-and-return-outBuf and `mallocgc(typ.Size_, typ, true)`
based on whether the caller passed a buffer. This works because the size
is fixed (`typ.Size_` known at compile time) and the only allocation is
the result struct itself.

Dynamic-size returned values — `string`, `[]T`, `map[K]V`, anything that
the callee builds up by appending or growing — don't fit this shape:

- A `string` result needs a byte buffer of unknown length.
- A `[]T` result needs a backing array; growth via `append` may need
  multiple reallocations during construction.
- A `map[K]V` result has hashtable buckets, overflow chains, and the
  hash table itself.

For each, the heap allocations happen *inside the callee* during
construction — well before the result is returned. A single `outBuf`
parameter can't accommodate them.

The generalisation: instead of one `outBuf` per result, the caller
hands the callee a *region* — a bump-style allocator that the callee
treats as the destination for all dynamic memory associated with the
result. When the caller's lifetime for the result ends, the caller
resets the region; everything goes away at once with no GC pressure.

This is the standard region-based memory management story (Tofte–Talpin,
Cyclone). What we're adding is the gd-specific machinery: ABI for
passing regions through the call graph, a runtime allocator that
honours the region, and an inference path that decides when a callee
should write into the caller's region vs. the heap.

## Mental model

A Phase G outBuf is a region of size 1 holding a single object of known
type. Generalised:

- A **region** is a runtime-managed bump arena with a known parent
  lifetime. The region is a `*Region` value passed implicitly through
  the ABI alongside the regular args.
- An **allocation site inside the callee** that would normally call
  `mallocgc` — `make([]T, n)`, string concat, map create, slice grow,
  composite literal — checks "did my caller pass me a region?" and:
  - If yes, bumps the region's pointer (no GC).
  - If no, falls back to `mallocgc` (GC-managed; same cost as today).
- The **caller's region pointer is decided at call site**, exactly the
  way Phase G's `outBuf` is decided today: from the local escape
  analysis of the callee's results.

The four region kinds we need:

- `&heap` (nil) — current behaviour. Default.
- `&frame` — caller's stack frame; auto-resets on caller return.
  Equivalent to today's Phase G outBuf, generalised to many alloc sites.
- `&arena` — caller-allocated bump arena that lives until explicit
  `Reset()`. Used for tree-shaped data the caller builds and discards
  (parser ASTs, scratch builders).
- `&inherit` — "use whatever region the caller is in". Default for
  transitive calls so plumbing isn't manual through every layer.

## Why this fits the fork

Three pieces are already in place:

1. **Per-method escape mask** in iface tabs (`escape-bits.md`) tells the
   call-site whether a callee retains a pointer arg. The same mechanism
   can carry per-result mask bits saying "this result honours a region
   parameter".
2. **`outBuf` per pointer return** machinery in `escape-bits-phase-g-plan.md`
   already adds an implicit return-slot argument per result. Region is
   the same shape with a different runtime meaning (one pointer, but to a
   `Region` instead of to a fixed-size buffer).
3. **`maybeInPlace`** in `runtime/escape_bits.go:80` is the call-site
   runtime helper that picks stack-vs-heap. We extend it to a
   `regionAlloc(region, size, align, typ)` that picks region-bump vs.
   heap.

So Phase G already laid the ABI groundwork. What's missing is:

- Variable-size allocation against the region (vs. fixed `typ.Size_`).
- Routing the existing dynamic-allocation lowerings (`makeslice`,
  `mapassign`, `concatstrings`, `growslice`, etc.) through the
  region helper when one is in scope.

## Region descriptor

```go
// runtime/region.go (new file)
type Region struct {
    base   unsafe.Pointer   // backing memory start
    bump   unsafe.Pointer   // current allocation pointer
    end    unsafe.Pointer   // backing memory end
    next   *Region          // chained when capacity exhausted
    parent *Region          // for nested scopes (sub-region inside arena)
    flags  uint32           // see below
}

const (
    regionFlagPooled  = 1 << 0  // came from per-P pool, return on Reset
    regionFlagFrame   = 1 << 1  // backing is on caller's stack
    regionFlagInherit = 1 << 2  // forward allocations to parent if local exhausted
)
```

The descriptor itself is one cache line. It sits on the caller's stack
when the region kind is `frame` or is allocated lazily when the kind
is `arena`. Per-P pools amortise the cost of obtaining backing memory
for short-lived arenas.

## Allocator path

A region-aware allocator wrapper:

```go
//go:nosplit
func regionAlloc(r *Region, size, align uintptr, typ *abi.Type) unsafe.Pointer {
    if r == nil {
        return mallocgc(size, typ, true)
    }
    asize := alignUp(size, align)
    p := alignUp(uintptr(r.bump), align)
    if p+asize > uintptr(r.end) {
        return regionGrow(r, size, align, typ)
    }
    r.bump = unsafe.Pointer(p + asize)
    if typ.Pointers() {
        // Region carries its own ptrmask shadow so the GC scans
        // pointer-bearing fields during the region's lifetime.
        regionMarkPointers(r, unsafe.Pointer(p), typ)
    }
    return unsafe.Pointer(p)
}
```

The `regionGrow` slow path either chains a new backing chunk (for
arenas) or falls through to `mallocgc` and links the result to the
region's "promoted" list (for `frame` regions that exhausted their
stack budget).

## ABI

Per-result implicit `*Region` parameter, encoded the same way as Phase G's
`outBuf`. The escape mask grows a "region-honoured" bit per result —
when set, the callee body uses `regionAlloc` for the named result's
working memory.

Three call-site shapes:

```go
// Caller passes its frame region (auto-reset on return):
result := f(stackRegion(&_regionBuf), args...)

// Caller passes an explicit arena (reset by caller later):
result := f(myArena.Region(), args...)

// Caller can't prove non-escape, falls back to heap:
result := f(nil, args...)
```

The `stackRegion(&_regionBuf)` form mirrors Phase G's `&buf` outBuf:
the region descriptor is a stack value; backing memory is a fixed
inline buffer (e.g. 256 B) that grows by chaining heap chunks if the
callee blows past it.

## Per-type plan

### Strings

Lowest-hanging fruit. The hot pattern (Pattern 2 in the allocations
report) is:

```go
func Quote(s string) string {
    buf := make([]byte, ...)         // heap alloc 1
    // ... fill buf ...
    return string(buf)               // heap alloc 2 (slicebytetostring copy)
}
```

Region-aware version:

```go
func Quote(s string, r *Region) string {
    buf := regionMakeByteSlice(r, ...)
    // ... fill buf ...
    return regionString(r, buf)      // alias if r == frame, or copy to heap
}
```

Caller passes `frame` if it doesn't retain the result, `nil` otherwise.
For `frame` callers we get one heap-rep header (caller's frame, free)
and the bytes live in the caller's region (free). Net: zero heap
allocs for the common case.

The `regionString` helper takes care of the SSO inline-rep case
(byte count ≤ 15 → fits in the header, no region storage needed) and
the heap-rep case (header points at region storage; region must outlive
the string).

### Slices

`make([]T, n)` becomes `regionMakeSlice(r, T, n, n)`. `growslice` for
appended elements becomes `regionGrowSlice(r, T, oldSlice, newcap)`.

For `frame` regions we keep the slice on the caller's stack as long
as it fits the inline buffer; spillage goes to a chained heap chunk
linked to the region (freed at Reset).

The annoying edge: `append` may grow a slice across multiple region
extensions. Each grow leaves a dead chunk in the region until Reset.
Acceptable for the common build-then-consume pattern; pathological for
long-lived growing slices (those should opt out and use the heap).

### Maps

Hardest. Map state is a header + hash table + bucket arrays + overflow
chains. Each is a separate alloc today. Region-allocating all of them
needs a region-aware variant of `runtime.mapcreate`/`mapassign` that
threads `*Region` through every internal allocation:

- `hmap` itself in the region.
- Initial `buckets` array in the region.
- Overflow buckets in the region.
- Key/value slots are inline in buckets, no separate allocs.

Defer maps to phase 3. The win for build-and-discard maps is real
(JSON decode populating a temporary map, regexp's compile-time symbol
table) but requires more runtime surgery than strings/slices.

### Pointer fields inside region-allocated values

If a region-allocated value `*T` has a field that points to heap-allocated
memory, the GC must scan the region during the GC cycle to keep the
pointee alive. Solution: each region carries a set of (pointer, type)
records for the values it holds; GC scan adds those to the work list.

If a region-allocated value's field is later overwritten with a pointer
to a *shorter-lived* region, we have a use-after-free hazard. Solution:
write barrier on stores into region-allocated objects. The barrier
checks `dst.region.lifetime ≥ src.region.lifetime`; if not, either
copy-promote `src` to `dst.region` (eager) or refuse and panic
(strict). Eager promotion is the safer default; it costs a per-store
region-compare on writes into region objects only.

Containers stored *into* the heap that point into a region are a
similar risk; the heap-write barrier already runs on every heap
pointer write, so we extend it with a region-promote check. This is
the only barrier-side change required.

## Inference

Same shape as Phase G. The escape solver gains a per-result "region
candidate" bit alongside the existing "stack candidate" bit. Walk's
call-site rewrite passes the appropriate region descriptor (or nil)
based on the bit. Annotated callees opt in to honouring the region
via `//gd:region` (per function) or via type-level region parameters
in a future syntax extension (out of scope here).

For the MVP, infer from the existing Phase G analysis: any function
already eligible for outBuf passing is automatically eligible for
region passing once its body is region-aware. Programmer-annotated
opt-in handles the broader cases (parsers, encoders, builders).

## Phased rollout

**Phase R1 — runtime + ABI scaffold.** Add `runtime.Region`,
`regionAlloc`, `regionString`, region-aware `growslice`. Add the per-result
region-mask bit to escape-bits encoding. No code generation changes
yet; gates everything off by default.

**Phase R2 — strings.** Switch `runtime.concatstrings`, `slicebytetostring`,
and `intstring` to the region-aware path when the result has a region
parameter. Annotate one or two stdlib functions (`strconv.Quote`,
`net.hexString`) for end-to-end validation. Land with `GOREGIONS=1`
gate.

**Phase R3 — slices.** Region-aware `makeslice` and `growslice`. Cover
the common build-up patterns in `bytes.Buffer.Write*`,
`strings.Builder.Write*` (the buf grow inside their methods).

**Phase R4 — maps.** Full region-aware `mapcreate`/`mapassign`.

**Phase R5 — caller-side inference.** Compiler emits region-passing for
any callee whose escape mask says the result-bit is honourable, when
the result is locally non-escaping. Defaults regions to on; opt-out per
file.

**Phase R6 — programmer-facing arena API.** Expose `runtime.Arena` (or
a `arena` package) for explicit lifetime control. Bridges to the
existing region machinery.

## Open questions

1. **Granularity of the region parameter** — one region per function or
   one per result? Per-function is simpler ABI-wise; per-result is more
   precise (some functions return both heap-bound and frame-bound
   results). Start with per-function.

2. **`frame` region exhaustion** — what's the inline buffer size? Too
   small → frequent spill to heap, no win. Too big → wasted stack on
   the common short case. Probably 256 B with two `next`-chained
   heap fallbacks that the per-P pool reuses.

3. **Goroutine boundaries** — a region passed to a function that calls
   `go` would need to either prevent the spawned goroutine from
   capturing region pointers, or promote those captures to the heap.
   Conservative: any `go`/`defer` in a region-aware function falls
   back to heap allocation for captured state. Same conservative
   stance as Phase G.

4. **Reflection** — `reflect.MakeSlice`, `reflect.New`, etc. don't
   know about regions. Either they always heap-allocate (safe but
   loses the win for reflect-heavy code), or we add region-aware
   variants to the reflect API.

5. **`map` deletion semantics** — region-allocated maps can't free
   buckets back to the region (bump-only). For build-up patterns this
   is fine; for "build then prune" patterns we'd need a free-list
   inside the map's region slice, which is beyond MVP.

## Non-goals

- Compile-time lifetime checking (Rust borrows). The fork stays
  garbage-collected; safety is enforced by the write barrier
  promoting cross-region pointers, not by static analysis.
- User-visible region syntax. The MVP is invisible to source code
  except for opt-in pragmas.
- Inter-goroutine region passing. Regions are per-G; cross-G handoff
  always goes through the heap.

## Files that will change

- `src/runtime/region.go` (new) — `Region` type, `regionAlloc`,
  `regionString`, growth logic.
- `src/runtime/string.go` — region-aware `concatstrings`,
  `slicebytetostring`, `intstring` variants.
- `src/runtime/slice.go` — region-aware `makeslice`, `growslice`.
- `src/runtime/mbarrier.go` — write barrier extended with region-promote
  check.
- `src/runtime/mgc.go` — GC scan picks up active regions.
- `src/internal/abi/escape.go` — per-result region-mask bit encoding.
- `src/cmd/compile/internal/escape/` — per-result region-candidate
  inference; piggybacks on Phase G.
- `src/cmd/compile/internal/walk/phaseg_return.go` — call-site
  rewrite: emit region descriptor as the implicit parameter.
- `src/cmd/compile/internal/ssagen/ssa.go` — lowering of
  `make`/`growslice`/string-concat to the region-aware runtime calls
  when in a region-honoured context.

Estimated diff size for the full design: ~2-3k LOC across runtime and
compiler. Phase R1+R2 (strings only, MVP) is roughly 600-800 LOC and
should land first; the rest follows the same pattern with mostly
mechanical extension.
