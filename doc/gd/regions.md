# Regions — generalising Phase G to dynamic memory

Status: design draft, 2026-05-03 (rev 2 — single-pointer ABI, per-pointer
region map).
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
based on whether the caller passed a buffer.

This works for one fixed-size result but breaks down for dynamic memory:

- A `string` result needs a byte buffer of unknown length.
- A `[]T` result needs a backing array; growth via `append` may need
  multiple reallocations during construction.
- A `map[K]V` result has hashtable buckets, overflow chains, and the
  hash table itself.
- A returned struct can have several pointer fields (string + slice +
  pointer-to-other-struct), each with its own desired lifetime.

A single `outBuf` per result can't accommodate any of these, and even
multiple `outBuf`s don't compose well across call chains.

## Design

**Replace** the `outBuf` injected-pointer pattern. Reuse the same
register-passed pointer slot, but point it at a per-pointer region map
rather than at storage:

```
*injected → *[N]byte
            ^^^^^^^
            One byte per pointer-bearing slot in the function's
            full pointer signature (params receivers + results).
            Byte value is the region index in the caller's per-G
            region table:
              0       — heap (default; matches stock Go semantics)
              1..255  — caller-managed region descriptors
```

Pointer count `N` is a compile-time property of the function signature:
walk the param + result + receiver types in canonical order
(depth-first, fields-in-decl-order, slice/string/map header pointer
first, then any embedded pointer fields), count pointer slots. The
ptrmask machinery already does this for GC; we mirror it. A
`func(string, []byte) (Foo, error)` where `Foo struct{ Name string;
Children []*Foo }` and `error` is heap-iface has:

- string param: 1 ptr (data)
- []byte param: 1 ptr (data)
- Foo result: 2 ptrs (Name.data, Children.data)
- error result: 1 ptr (iface data)

Total `N=5`. The injected `*[5]byte` describes which region each of
those 5 pointers lives in (or comes from, on the param side).

**One register, one indirection, full coverage.** Same ABI shape as
today's outBuf — the slot survives unchanged from Phase G, only the
referent semantics change.

## Region table (per-G)

```go
// runtime/region.go
type Region struct {
    base, bump, end unsafe.Pointer
    parent          *Region          // for cross-region promotion check
    flags           uint32
}

type regionTable struct {
    regions [256]*Region   // index 0 unused; heap is implicit
    top     uint8          // next free slot
}

// gp.region carries the per-G table.
```

Caller-side pseudocode for a typical region scope:

```go
// arena := arena.New()
arenaIdx := gp.region.push(arena.Region())   // returns assigned index
// Build the byte array for the call:
var rmap = [3]byte{arenaIdx, arenaIdx, 0}    // result's two pointers
                                              // → arena; error iface
                                              // pointer → heap (escapes)
result := f(&rmap, args...)
// ... use result ...
gp.region.pop(arenaIdx)
arena.Reset()
```

The `push`/`pop` is amortised (per-arena, not per-call). For *frame*
regions (auto-reset on caller return) the index is established at
function entry and released at function exit.

## Allocator path

Every dynamic allocation lowering threads through a region-aware
helper that consults the per-G table:

```go
//go:nosplit
func regionAllocAt(idx uint8, size, align uintptr, typ *abi.Type) unsafe.Pointer {
    if idx == 0 {
        return mallocgc(size, typ, true)
    }
    r := gp.region.regions[idx]
    asize := alignUp(size, align)
    p := alignUp(uintptr(r.bump), align)
    if p+asize > uintptr(r.end) {
        return regionGrow(r, size, align, typ)
    }
    r.bump = unsafe.Pointer(p + asize)
    if typ.Pointers() {
        regionMarkPointers(r, unsafe.Pointer(p), typ)
    }
    return unsafe.Pointer(p)
}
```

Allocation sites that are determined at compile time to fill a specific
result-pointer-slot read the corresponding byte from the injected map
and pass it to `regionAllocAt`. Sites for purely internal temporaries
that don't flow to a result get a per-function default (frame for the
common case; heap for opt-out).

## Per-pointer lifetime — Rust-style with runtime fallback

The per-pointer byte map gives us a Rust-borrow-checker-shaped thing
without the ergonomic cost: each pointer in the function signature
declares which region it belongs to. The compiler infers these bytes
from the call graph; the *runtime* enforces the lifetimes via an
extended write barrier, so we don't need to reject programs that defeat
static analysis — they just degrade to the heap path.

The two static checks the compiler performs at every call site:

1. **Outflow safety.** For each pointer the callee will produce
   (results), the caller assigns a region index. Static rule: the
   chosen region must outlive every use of the produced pointer. Where
   the caller can't prove this, byte=0 (heap) is the conservative
   default.

2. **Inflow safety.** For each pointer the callee receives (params),
   the caller declares which region the pointer's pointee lives in.
   Callee uses this to decide whether storing the param into a
   long-lived structure requires runtime promotion.

Where static analysis is incomplete (indirect calls, reflection,
pointer-bearing closures), the byte map is computed at runtime by
combining the caller's intent with the callee's published per-method
mask (same shape as escape-bits resolution today). The
`runtime_resolveRegionMap(staticBytes [N]byte, methodMask uint64)`
helper performs the AND/OR.

The **runtime safety net** is the cross-region write barrier:

```go
// On any pointer field write `dst.field = src`:
if isInRegion(dst) {
    dstRegion := regionOf(dst)
    if isInRegion(src) {
        srcRegion := regionOf(src)
        if srcRegion.depth > dstRegion.depth {
            // src dies first; promote it into dst's region (or heap).
            src = promote(src, dstRegion)
        }
    }
}
*dstField = src
```

Promotion copies the pointee tree breadth-first into the longer-lived
region until reachable region depth is uniform. Unbounded? In theory
yes, but bounded in practice by the size of the promoted subgraph,
which is exactly what Rust-borrowed code would have refused to compile
— here we accept the copy and keep going.

## Why this is better than the per-slot mask sketch

Previous draft proposed a 64-bit "region word" with 6 bits per slot
encoding kind+index. Drawbacks of that approach:

- Only 10 slot positions (10×6 + 4 metadata) — insufficient for
  multi-pointer structs.
- Conflates struct field positions with parameter positions.
- Requires per-callee decoding of the kind nibble in every prologue.
- Doesn't share machinery with the existing outBuf injected-pointer
  ABI; needs a new register.

The byte-map approach:

- 256 distinct regions per call, indexed by 1-byte bytes.
- Granularity matches the natural structure of pointers (one byte per
  pointer slot, regardless of how they're packed into params/results).
- No metadata bits in the word — metadata moves to the per-method
  escape mask in the iface tab where it already lives.
- Reuses the existing injected-pointer slot from Phase G; no extra
  register reservation.

## ABI

Per-function single hidden pointer parameter (already in place from
Phase G), now interpreted as `*[N]byte` where N is the function's
total ptr-slot count. nil = "all heap" — same semantics as today's
nil outBuf.

For static call sites the compiler emits the byte array as an
immediate stack value:

```go
var rmap = [3]byte{0, 1, 0}
result := f(&rmap, args...)
```

For indirect calls the byte array is computed at runtime from the
caller's intended map AND the callee's published per-pointer
honour-mask:

```go
honour := iface.tab.regionHonourMask         // []byte, len N
intent := caller's static rmap               // []byte, len N
for i := range intent {
    if honour[i] == 0 {
        rmap[i] = 0   // callee won't honour this slot, force heap
    } else {
        rmap[i] = intent[i]
    }
}
```

Per-method honour mask is published in the same iface-tab area as the
existing escape-bits mask. Bit-for-pointer-slot encoding rather than
bit-for-param.

## Per-type plan

### Strings

```go
func Quote(s string) string {
    buf := make([]byte, ...)
    // ... fill buf ...
    return string(buf)
}
```

Becomes (with the rmap injection invisible at source level):

```go
func Quote(s string, rmap *[2]byte) string {
    // rmap[0] = caller's region for s.data (input lifetime)
    // rmap[1] = caller's region for the returned string.data
    buf := regionMakeByteSliceAt(rmap[1], ...)
    // ... fill buf ...
    return regionStringAt(rmap[1], buf)
}
```

Caller passes `rmap[1]=frameIdx` when result is local; rmap[1]=0
otherwise.

### Slices

`make([]T, n)` becomes `regionMakeSliceAt(rmap[k], T, n, n)` where k
is the canonical pointer index of the slice header's data ptr in the
function's signature.

### Maps

`make(map[K]V)` becomes `regionMapMakeAt(rmap[k], …)`. The map
runtime threads the same idx through bucket/overflow allocations.

### Receiver/param pointers

For *input* pointers, the byte tells the callee what region the
pointee lives in. The callee uses this in two places:

- When *retaining* the input (storing into a longer-lived structure),
  the write barrier compares regions and may promote.
- When *forwarding* the input to a sub-call, it passes the same byte
  in the sub-call's rmap so the sub-callee gets the same region info.

## Static analysis (Rust-light)

The compiler runs a per-function lifetime inference:

1. Build a constraint graph: each pointer slot has a region variable;
   constraints flow from "pointer is stored into longer-lived
   structure" → "stored region must outlive containing region".
2. Solve to a single region per slot (use unification).
3. Where solving fails (cyclic or under-constrained), pick the
   conservative heap region for the unsolved slots.
4. Emit the byte array at every call site based on the solved
   variables.

This is much narrower than full Rust borrow checking:

- No borrow vs owned distinction; everything is owned by some region.
- No reject-on-failure; failures degrade to heap.
- No lifetime annotations in source; analysis is purely flow-based.
- No subtyping/variance lattice; just region inclusion (parent of).

The escape solver already does ~half this work. Extending its
existing edge tracking from "escapes / doesn't escape" to "escapes
to which region" is ~+30% solver code.

## Phased rollout

**Phase R1 — runtime + ABI scaffold.** `runtime.Region`,
`regionAllocAt`, per-G region table, byte-map ABI plumbing.
Re-purpose Phase G's injected-pointer slot. No allocator changes yet
(everything still goes through `mallocgc`); this is the wiring pass.

**Phase R2 — strings.** Wire `concatstrings`, `slicebytetostring`,
`intstring` to consult `rmap`. Annotate `strconv.Quote`,
`net.hexString` for end-to-end validation.

**Phase R3 — slices.** Region-aware `makeslice`, `growslice`. Cover
`bytes.Buffer.Write*`, `strings.Builder.Write*` internals.

**Phase R4 — maps.** Region-aware `mapcreate`/`mapassign`.

**Phase R5 — write barrier extension.** Cross-region promotion in the
write barrier. Until this lands, R2-R4 must conservatively heap any
allocation that could outlive its source region (compiler enforces).

**Phase R6 — Rust-light solver.** Replace the conservative
"all-heap-unless-trivially-frame" inference with the flow-based
region-variable solver. Unlocks the harder cases (parsers, encoders,
builders that thread state through multiple frames).

**Phase R7 — programmer-facing arena API.** Expose `runtime.Arena`
(or an `arena` package) for explicit lifetime control.

## Open questions

1. **Per-G table size.** 255 active regions per goroutine feels large
   but the byte-pop/push cost is constant. If a typical call stack has
   ≤30 region-honouring frames each declaring 1-3 regions, the table
   stays well under 100. Fixed 256 entries × 8 B = 2 KB per G — a
   tax but not catastrophic.

2. **Pointer-slot canonical ordering.** The byte's position in the
   array must match a stable, compiler-and-runtime-agreed traversal
   order. Re-use `abi.Type.GcData` ptrmask order? Likely yes — both
   sides already share that scheme for GC.

3. **Functions with too many ptr-slots.** Cap at 255? Or fall through
   to a longer encoding when N>255? Practical max for stdlib is
   ~10-20; the cap question is theoretical.

4. **Reflection and unsafe.** `reflect.MakeSlice` etc. don't know
   the caller's intended region. They always heap-allocate. Same for
   `unsafe.Slice` of an existing pointer; the pointer's region is
   whatever it already had, the slice header is heap or frame as
   escape would have decided.

5. **Closures.** A closure value records the rmap that was in scope
   at creation. Captured pointers carry their region info; the
   closure body uses them as if it were the original frame.

6. **Goroutine boundaries.** A region passed into a closure that's
   then used as `go func()` would extend past the creator's frame
   lifetime. Conservative: any pointer captured by a goroutine-spawning
   closure must be promoted to heap at the `go` statement. This is
   the Phase G stance generalised.

## Non-goals

- Source-visible region/lifetime syntax. The scheme is invisible at
  source level except for opt-in pragmas (`//gd:noregion`,
  `//gd:region`).
- Compile-time rejection. Failure modes degrade to heap; Go's
  programming model is preserved.
- Inter-goroutine region passing. Always heap-promoted.

## Files that will change

- `src/runtime/region.go` (new) — `Region`, table, allocator helpers.
- `src/runtime/string.go`, `slice.go`, `map.go` — region-aware variants.
- `src/runtime/mbarrier.go` — write barrier with promotion.
- `src/runtime/mgc.go` — GC scan picks up active regions.
- `src/internal/abi/escape.go` — per-pointer-slot honour mask
  encoding (replaces / extends per-result mask).
- `src/cmd/compile/internal/escape/` — region-variable inference
  (Rust-light solver).
- `src/cmd/compile/internal/walk/phaseg_return.go` → renamed
  `phaseg_region.go`; emits the rmap at call sites instead of an
  outBuf.
- `src/cmd/compile/internal/ssagen/ssa.go` — lowering of
  `make`/`growslice`/string-concat to region-aware runtime calls.

Estimated diff size for the full design: ~3-4k LOC across runtime and
compiler. R1+R2 (strings only) is roughly 700-900 LOC.
