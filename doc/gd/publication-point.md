# The publication point (design 2026-11-23, phase 1 built)

A data race must never be memory-unsafe: racing goroutines may see stale
values, never torn ones. This document asks whether the guards that give
that guarantee (`fat-iface.md`, `sso-string.md`, `slice16.md`, `maps.md`)
can be skipped until the first point where another goroutine could reach
an object, and what that would be worth.

**Phase 1 is built: §13 says what was built, what ran where and what it
costs.** The rest is the design as reviewed. §1 to §4 were measured at
c7a0e1fde2, on amd64, before anything was built.

## 0. Summary

**The idea is sound, and the general version is not worth building.**

- **There was a real hole.** `(*state).unpublished` skipped the guards in
  a new object and issued no barrier. On a weakly ordered target a racing
  reader could get a string or interface made of some of its words (§3).
  **Reproduced on an Apple M4** (§13.2), with torn strings and interfaces
  and closures with no code pointer.
- **It is closed for little, with no new analysis.** One store barrier
  follows the last plain store of each straight-line fill (§5). The barrier
  does not have to be at X, only between the fill and X. On the M4 that
  costs about 1.1% of a compile of `go/types`, against about 3.4% for
  removing the skip (§13.3). amd64 and s390x text does not change.
- **A real X analysis buys almost nothing more.** The guards cost 3% to
  20% of all instructions. A flow-sensitive analysis across blocks and
  non-keeping calls removes **0.00% to 0.51%**. Adding fresh-result
  contracts brings the best case to **0.89%** (§4).
- **The reason is that the cost is in the loads.** 83% of guarded accesses
  are loads. Between 51% and 81% of them do hit an object not yet
  published, but through a pointer loaded from memory, about which a
  compiler can prove nothing: the analysis proves **0.1% to 2.6%** of them.
- **Maps: do not build it.** It would skip one compare-and-swap on 22% of
  map writes, about 0.15% of the cycles of a compile (an estimate, §8).

Phase 1 of §11 is what was built, and nothing else is recommended for
now. §6 to §8 keep the full design, because goroutine-local arenas need the
same definition of X, and so that the question need not be reopened from
scratch.

## 1. Semantics

For an object O, **X(O)** is the first program point at which a pointer to
O could reach another goroutine.

- **Before X** the allocating goroutine is the only one that can reach O.
  It may load and store O with plain instructions.
- **At X**, on a weakly ordered CPU, every plain store made to O (and to
  what O reaches) is visible to all CPUs before the pointer is. One store
  barrier does this.
- **After X**, O is loaded and stored with the guards, for ever. Nothing
  marks O at run time: the compiler just stops skipping.
- **Local for ever** (the future goroutine-local arenas) is the case where
  X never happens. The rule that an arena object never becomes shared later
  is then the statement that a program point which would be X for it is an
  error or forces a copy. It is not a transition.

X is **not** the next synchronization. A racy store publishes with no
synchronization at all, and safety must hold for racy programs:

```go
p := new(T); p.s = a; p.i = x
G = p // a plain store; the reader has no lock either
```

### What a reader relies on

A reader that loaded `G` and then loads `G.s` has an address dependency,
which every target Go supports honors. So if the writer ordered the fill
before the store of `G`, the reader sees the whole fill. Its sequence check
passes, correctly: nobody holds the stripe, and no store to those words can
follow until after X, when stores are guarded.

## 2. What counts as X

A point is X for O if, after it, a goroutine other than the allocating one
may hold a pointer into O or into anything reachable from O.

| Event | X? | Why |
| --- | --- | --- |
| Store of the pointer to a global | **Yes** | Any goroutine can load it. |
| Store into a heap object that is past its own X, or not known to be new | **Yes** | That object is reachable by others. |
| Store into a fresh object | No: the child's X becomes the parent's | Reachable only through the parent (§7.3). |
| Store into a stack slot | No, if the slot is tracked | But the slot now holds it: see below. |
| Atomic store, swap, CAS of the pointer | **Yes** | Its purpose. It has release ordering, so it is its own barrier. |
| `return` of the pointer | **Yes**, unless the function has a fresh-result contract (§7.2) | The caller may store it racily with no barrier. |
| Argument to a call whose tag says it may be kept (`Heap == 0`) | **Yes** | The callee may store it anywhere. |
| Argument whose tag says its *contents* leak (`Heap > 0`) | **Yes for what it points to**; O itself stays before X | `G = p.child`. |
| Argument whose tag says it flows to a callee (`Callee >= 0`) | **Yes** | A closure that is called runs code that may publish what it captured. |
| Interface or closure call, any argument or receiver | **Yes** | The callee is unknown. |
| `go f(p)`, or a closure capturing p given to `go` | **Yes** | Another goroutine runs it. |
| Channel send, `select` send | **Yes** | The receiver gets it. Barrier: the channel's lock. |
| Closure capturing p | The closure holds it, as a parent does | X when the closure is X, or **when it is called**. |
| `defer` of a closure capturing p | **Yes, at the `defer`** | A panic may run it at any later call, on a path with no visible X. |
| `panic(p)` | **Yes** | A deferred function may publish the value. |
| Named result holding p, in a function with a `defer` | **Yes, at the store** | `recover` returns to the caller through a path that does not pass the `return`. |
| Conversion to `uintptr` | **Yes** | It can no longer be followed. |
| Conversion to `unsafe.Pointer` | Tracked while it stays a pointer; any arithmetic other than `add` is **X** | `unsafe.Add` keeps the base; a round trip through `uintptr` does not. |
| Conversion to an interface | No, by itself | The data word carries it. A boxed copy holds p as a child. |
| `reflect.*`, `internal/reflectlite` | **Yes** | See below: their tags are not usable. |
| `runtime.SetFinalizer`, `AddCleanup` | **Yes** | The finalizer goroutine gets it. |
| `weak.Make`, `unique.Make` | **Yes** | Another goroutine can get a strong pointer. |
| `sync.Pool.Put` | **Yes** | Another P may `Get` it. |
| `sync/atomic`, `internal/sync` | **Yes** | As atomics, or as containers shared by design. |
| cgo call, assembly with no `//go:noescape` | **Yes** | Unknown. |
| Map insert of p as key or element | **Yes**, unless the map is itself fresh | As a store. |
| `copy`, `append` into memory that is not fresh | **Yes for what is copied** | As a store. This one is easy to miss: see the stack case below. |
| Address of a stack slot holding p, passed to a call or stored | **Yes** for what the slot holds | The callee reads it through the slot, or writes another pointer there. |

Two of these rows caught the first version of the measuring analysis, and
the run-time cross-check of §4.3 is what found them:

- **Copied out of a stack array.** `out.Append(jt)` builds a variadic
  `[]Node{jt}` on the stack and `append` copies it with `typedslicecopy`.
  The pointer reaches the heap in a runtime call whose arguments are the
  address of a stack array. A stack variable must be tracked as a holder of
  what is stored in it, through slices and phis.
- **A stack slot written by a callee.** With `var f *Field; dotpath(..., &f)`
  a load of `f` is not the pointer the function last stored there.

### Escape tags are not publication facts

Tags describe the **lifetime** of a pointer. Three things make them too
weak to use as they are:

- `//go:noescape` on a function whose body is elsewhere says nothing about
  what the argument *points to*. `reflect.chansend0`, `reflect.mapassign0`
  and `reflect.typedmemmove` are all declared so, and they move the
  contents of the argument into a channel or a map (`reflect/value.go`).
- `abi.NoEscape` hides a flow on purpose (`reflect`, `strings.Builder`,
  `internal/sync`, `unique`).
- A tag with a flow to a result says the result *may* be the argument, not
  that it is.

So a call may keep a window open only if the callee has a Go body, the tag
shows no flow to the heap at any depth and none to a callee, and the
callee is outside `runtime`, `reflect`, `internal/reflectlite`,
`sync/atomic`, `internal/runtime/*`, `internal/sync`, `syscall`, `unique`
and `weak`. That is the rule the measurements used.

## 3. How big the hole is today

This section describes the code **before phase 1**. §13 says what changed.

### The mechanism, as compiled

For `p := &R{ID: 1, V: x}; P = p`, arm64 (`go tool compile -S`):

```
STP   (R3, R4), (R0)        // ID, type word
FSTPD (F0, F1), 24(R0)      // the interface's payload
...                         // write barrier
MOVD  R2, 16(R0)            // the data word
MOVD  R0, c.P(SB)           // published: no barrier
```

A racing reader may see `P` before any of the three stores before it. A
string field is no better, **even on a CPU with LSE2**: the store is not
16-byte there, since `rawStoreType` writes the scalar word first and the
pointer word after the write barrier's branch, so `p.s = a` is two
separate `MOVD`s. A string with a nil pointer and a real length follows,
and `s[i]` reads address `i`. With `[]byte` in an interface the same
sequence can *write* near an absolute address.

### Audit

Default build, arm64. "Compiled" means I read the generated code; "read"
means it comes from reading the source, and was not exercised.

| Path | Torn on arm64? | Why | Basis |
| --- | --- | --- | --- |
| `&T{}`, `new(T)` + fields, `[]T{}`, inlined constructors | **Yes, all CPUs** | `unpublished`: word-by-word stores, no barrier. | compiled |
| Closures and method values | **Yes, all CPUs** | The same path. A reader may also see a nil code pointer, which faults. | compiled |
| `growslice`, `makeslicecopy`, `moveSlice*`, from a **stack** source | **Yes** on LSE2 CPUs | Plain `memmove`, then a header `STP` with no barrier (`runtime/slice.go:138,346,516,550`). Without LSE2 the header's stripe orders it. | read |
| The same from a **heap** source | **Yes** (this row said no until phase 1) | `shareCopyFrom` reads each value whole, but writes the new array with plain stores, and nothing ordered them before the slice. | read |
| Static part of a literal (`*p = stat`) | No | `shareMove` ignores `unpublished`. | read |
| `convT*` boxes | No, in themselves | A guarded interface store has a `DMB ISHST` after its CAS, which orders the box first. The *interface* holding the box may tear, as in row 1. | read |
| String conversions, `concatstrings` | Header no; **stale bytes yes** | `needzero=false`, a byte fill, a header store: a reader can see the old bytes of a freed object. Not a pointer/length tear (decision 3). | reproduced, M4 |
| `s.f = append(s.f, x)`, elements without pointers | **Stale bytes, on amd64 too** | The slice was stored before its new elements, which `growslice` had not cleared. Found by the litmus test; this document had said they were zero. | reproduced, amd64 and M4 |
| `materializeToHeap` (escape bits) | **Yes** | A plain copy from the stack to a new object, whose pointer the callee may store. Found while auditing phase 1. | read |
| `appendGrouped` | Unreachable | It needs `!ifaceOnly` and `!SlicePacked()`; packed is true on every 64-bit target but wasm, so it is dead even with `GOEXPERIMENT=sharing`. | read |
| Elements stored by `append` | No | Guarded: `growslice` is not a recognized allocation. | read |
| `reflect` Set, New, Append, Copy, Zero | No | `shareMove`; `unsafe_New` is not in `unpublished`'s list. | read |
| Channels, `select` | No | Under `c.lock`. | read |
| Maps | No | Barriers before each publication (`maps.md` §3). | read |
| `sync.Pool` | No | Slot store, then an atomic that releases it. | read |
| `atomic.Value` | No | Guarded store, then an atomic. | read |
| `go` closures, defer records | No | Run queue with release; defers stay on one goroutine. | read |
| `unique`, HashTrieMap, itabs, type caches, finalizers, cleanups, weak handles | No | Published by atomics or under locks. Only the publication points were checked. | read, partial |
| `makechan` | Crash only | Fields filled with no final barrier: a racy reader may fault near nil. No user value tears. | read |

The hole is therefore **ordinary code**: any new object with a string,
slice or interface field whose pointer then reaches another goroutine
through a racy store. The first two rows are also the most common shapes in
Go programs.

**Outside this hole, found on the way.** `reflect.Value` holds a type word,
a pointer, an inline payload and flags, and no string, slice or interface,
so it is not guarded at all. A race on a variable of type `reflect.Value`
pairs the type of one value with the pointer of another: type confusion.
**This one is reproduced, on amd64.** With a writer cycling one variable
through a `*[1000]int`, a `*[1]int` and an `int`, 7.5M of 134M racy reads
had a kind flag that disagreed with the type, and 116,267 had the type
`*[1000]int` with the pointer to the `[1]int`, so that `Elem().Index(999)`
would read past the object. Unlike the rest of this section it does not
depend on weak ordering, and the seal does not touch it. Phase 1 fixes it
separately (§13.1).

## 4. Measurements

amd64, `perf stat`, user-mode instructions, three runs, `GOGC=off` so that
the collector's pacing does not move the counts (the spread between runs
was 0.00% to 0.02%). `go/types` is the difference between 12 and 2
iterations, which cancels the `go list` it starts with, and runs with the
collector on (spread up to 0.5%). The machine is shared: **cycle counts
moved by up to 4% between builds with nearly equal instruction counts, so
they are not used.** `http ClientServer` runs on 4 Ps: treat differences
under about 1% there as noise.

### 4.1 What each option does to instructions

Against today's build:

| Program | No guards (`-d=notear`) | Skip removed | Strings, slices whole in new objects | Window: one function | + fresh-result contracts |
| --- | ---: | ---: | ---: | ---: | ---: |
| gofmt | -17.87% | +0.40% | +0.08% | -0.19% | -0.15% |
| json encode | -14.39% | -0.10% | -0.09% | -0.00% | -0.00% |
| json decode | -14.76% | -0.04% | -0.01% | -0.02% | -0.02% |
| go/types Check | -8.75% | +4.74% | +0.83% | -0.39% | -0.89% |
| compile go/types | -20.14% | +2.60% | +0.15% | -0.51% | -0.52% |
| http FakeConn | -3.20% | +2.61% | +0.52% | -0.09% | -0.09% |
| http ClientServer | -13.07% | +6.95% | -0.79% | -0.01% | -0.17% |

- **No guards** is the ceiling: everything that could ever be saved. (The
  runtime's own typed copies stay guarded in that build.)
- **Skip removed** is `unpublished` always false: what today's small window
  is worth, and what closing the hole by dropping it would cost.
- **Strings, slices whole** keeps the skip for interfaces only (§7.4).
- **Window** builds leave out the guard at each site the analysis proves,
  with no barrier, which on amd64 is the design's real code. 96.6% of the
  sites lined up between the build that classified and the build that
  skipped (the rest are numbered differently), so these two columns are
  approximate. They are small either way.

Text size, against today:

| Binary | No guards | Skip removed | Window: one function |
| --- | ---: | ---: | ---: |
| cmd/compile | -32.1% | +2.1% | -1.6% |
| gofmt | -16.1% | +1.8% | -1.2% |
| json.test | -21.0% | +3.5% | -2.7% |
| http.test | -21.1% | +4.3% | -2.9% |

For `cmd/compile` on arm64: skip removed +2.25%, strings and slices whole
+0.47%.

### 4.2 What fraction of accesses are before X

Counted per value (a struct with two strings is two), over all seven
workloads, 610 million accesses:

| Access | Count | Object not yet published (dynamic upper bound) | Proved today | + one function | + contracts |
| --- | ---: | ---: | ---: | ---: | ---: |
| interface store | 19.0M | 82.9% | 27.0% | 29.8% | 29.8% |
| interface load | 79.3M | 53.7% | 0.0% | 0.1% | 0.1% |
| string store | 13.0M | 63.6% | 14.6% | 30.2% | 30.2% |
| string load | 46.4M | 50.7% | 0.0% | 2.6% | 2.6% |
| slice store | 62.6M | 80.5% | 3.9% | 6.6% | 6.6% |
| slice load | 382.2M | 80.5% | 0.0% | 1.4% | 1.4% |
| map write | 7.9M | 90.1% | 0.0% | 21.6% | 21.7% |

**Dynamic upper bound.** Built with `GOEXPERIMENT=sharing`, `-d=nolocal`
and `-d=notear`, with a counter at each site that asks the run-time shared
bit whether the object has been published. This differs from true X:

- The bit is set when a pointer *does* reach shared memory. X is where it
  *could*. So the bit is set later, and the figure is an upper bound.
- Most of these programs have one goroutine doing the work, so much of
  their data is never published at all (`go/types`: 100% of interface
  stores). That is a fact about the run, not something a compiler can see.

**Static classes.** A throwaway forward must-analysis over the SSA of each
function, with the rules of §2 and §7:

- *today*: what `unpublished` proves.
- *one function*: across blocks, joins and loops, and across calls that
  keep nothing.
- *contracts*: also the results of functions that return a new,
  unpublished object, to a fixed point over all compiled functions (1395
  of 9459 pointer results qualify).

### 4.3 The cross-check

For every site the analysis called fresh, the run-time bit must say the
object is unpublished. In the final run, **no hit out of 610 million
violated it.** Earlier runs did, and each violation was a real hole in the
analysis (the two of §2, and a pointer loaded from a fresh object, which is
why §7.3 does not trust one) or the map's shared zero value, which is
harmless. This shows the analysis is right on these runs, and no more. It
also does not model open-coded defers: the rule of §2 is in the design and
was not in the classifier, so its shares may be slightly high.

### 4.4 Barriers

If each straight-line fill gets one barrier (§5), the number executed is
the number of allocations with at least one plain multi-word store:

| Program | Barriers | Per 1000 instructions |
| --- | ---: | ---: |
| gofmt | 199,518 | 0.08 |
| http ClientServer | 154,462 | 0.07 |
| compile go/types | 1,550,485 | 0.05 |
| http FakeConn | 60,322 | 0.05 |
| json encode, decode | ~240 | 0.00 |

If a barrier cost 30 cycles, 0.08 per 1000 instructions is about 2.4 cycles
per 1000 instructions: under 0.5% at any plausible IPC. **The 30 cycles was
an assumption.** Measured on an M4, the whole seal costs more than that
estimate in some programs (§13.3).

### 4.5 What the numbers decide

- Today's small window is worth keeping: up to 6.95% of instructions.
- Extending it is not worth a compiler pass: at best 0.89%, and usually
  under 0.5%.
- Fresh-result contracts add nothing measurable except in `go/types`. The
  inliner has already done most of that work.
- The 3% to 20% that the guards cost is mostly out of reach of this idea.
  Anything that lowers it has to make loads cheaper, or know something about
  a pointer **loaded from memory**, which a window cannot do.

## 5. Recommended design: seal the fill

Keep `unpublished` as it is. Add one store barrier **after the last plain
store of each straight-line fill**, wherever X turns out to be.

```go
p := new(T)
p.s = a       // plain
p.i = x       // plain
              // <- PubBarrier, on weakly ordered targets
G = p         // or a call, a return, anything
```

**Why it needs no analysis of X.** The barrier only has to come between
the plain stores and X, and every X comes after the fill ends, since
`unpublished` proves that nothing but stores into the object and the stack
has happened. So "the fill ended" is a safe stand-in for "X may follow".

**Why it is simpler than a barrier at X:**

- No control flow. `unpublished` walks one memory chain and stops at a
  block boundary, so a fill is always inside one block.
- No question about calls, returns or contracts: a function that returns a
  new object has already sealed it.
- Almost no panic case: a bounds or division check ends the block, so the
  barrier comes before its branch.

**One exception: functions with a `defer`, on weakly ordered targets.** A
nil dereference while computing a later field (`p.b = q.x`) faults in the
middle of a fill, with no block boundary, and the panic may run a deferred
closure that publishes `p`. The object reaches the closure through a stack
variable, which is a stack store to `unpublished` (compiled on arm64, the
first string is stored plain):

```go
var h *M
defer func() { recover(); G = h }()
p := new(M)
h = p
p.S = a   // plain
p.T = q.V // may fault
```

**On those targets `unpublished` must return false in a function with a
`defer`** (`s.hasdefer`). amd64 and s390x keep the window: their stores
stay in order and a fault cannot land between the words of one value, so
what the closure publishes is whole. What the rule costs on arm64 was not
measured separately.

**What it gives up.** An object that is never published pays for a barrier
it did not need. §4.4 bounds that.

### As built

1. `tearGuardStore` is `tearGuard` for a store. When it skips the guard
   because the object is new, it records the object (`s.fills`). Stores
   are not marked one by one.
2. `sealFills` runs at the end of `buildssa`, after `insertPhis` and before
   `ssa.Compile`. It walks each block's memory chain. A run starts at a
   store into a recorded object, and goes on across any store **into the
   same object** (guarded or not) and any store to the stack: `&T{S: a, N:
   n, I: x}` stores `N` between two guarded fields and is one fill. One
   `OpPubBarrier` follows the run's last store into the object.
3. It runs that early because `memcombine` and `dse` may merge or remove
   the stores. Afterwards the barrier is a memory operation that consumes
   the store before it. `dse` treats that as a read, so nothing before it
   is shadowed by a store after it. `memcombine` stops at anything that is
   not a store. The write barrier pass ends its sequence of stores there.
   A load after the fill may keep the store's memory state, so that it is
   still forwarded from the store.
4. Targets: arm64, ppc64, riscv64, loong64, mips64. `OpPubBarrier` is
   already lowered on all of them. 32-bit `arm` and `mips` are weakly
   ordered too and **are not sealed**: they were outside this phase.

In the runtime, `publicationBarrier()` follows the copy in `growslice`,
`makeslicecopy`, `moveSlice`, `moveSliceNoCap` and `materializeToHeap`, from
any source, behind the constant `storesOrdered`: on amd64 it is a call to an
empty function, and the compiled code must not change.

### Why the alternatives lose

| Alternative | Verdict |
| --- | --- |
| Drop the skip on weak targets | Correct and one line, but costs up to 6.95% instructions as measured on amd64, and probably more on arm64, where each interface store becomes a CAS, a `DMB` and an `STLR`. The fallback if the seal turns out wrong. |
| Barrier at the real X | Needs all of §6 and §7 to find the same point in the common case. |
| `STLR` as the publishing store | Only when X is one pointer store in the same block; no help for calls and returns. |
| Strings and slices always whole | True but not sufficient (§7.4). |
| Full window analysis | §4: under 1%. |

## 6. Where an analysis would live, if one is ever built

This section and the next describe the general design, for arenas or for
a later change in the numbers. **They are not a recommendation to build.**

**(A) Extending the memory-chain walk in `ssagen`.** It runs while the SSA
is being built. Memory in a new block is a `FwdRef` until `insertPhis`, so
the walk cannot pass a block boundary. Guards are expanded inline into
several blocks during construction, so **the first guarded store ends every
later window in the function**. Good for straight-line fills, which is why
phase 1 stays here, and a dead end beyond them.

**(B) Facts from escape analysis at statement level.** `escape/shares.go`
already computes `UnsharedStorage` and `UnsharedPointee`, but they are
flow-insensitive: "every pointer this variable ever holds". They can say
*local for ever* and cannot say *before this statement*, which is the whole
idea. They exist only under `GOEXPERIMENT=sharing`, and they would inherit
the problems with tags in §2. Right for arenas' "X never"; wrong for a
window.

**(C) High-level guarded ops, expanded late**, as `writebarrier.go` does
with `IsNewObject` and `computeZeroMap`. `LoadWhole` and `StoreWhole` would
stay single ops through the early passes. A forward analysis runs on them,
and a pass expands those it cannot prove.

- It must run after `expand calls` and `decompose builtin`, so that values
  are words, and before `slice codec` and `writebarrier`, since the
  interface store holds its lock across the write barrier (`ShareLocked`).
- Every rule and pass that handles `Load` and `Store` (`dse`, `memcombine`,
  store-to-load forwarding, `nilcheckelim`) must learn the new ops or lose
  optimizations.
- The analysis itself is small: the throwaway version is about 800 lines
  and ran at the end of `buildssa`, on a build with the guards turned off,
  because with them expanded the control flow hides the accesses.

**If one were built, (C).** It is the only option that can see across
blocks. It may also be worth doing **for its own sake**: guards are 16% to
32% of text, and keeping them as ops until late could let the optimizer
remove redundant ones. That is a different project, and this document did
not measure it.

## 7. The analysis, if built

### 7.1 Control flow

Two analyses, because the two questions differ:

- **Fresh** (must): on every path to here, the object is before X. It
  decides whether an access may be plain. Meet is intersection.
- **Dirty** (may): on some path to here, a plain store to the object has
  not been followed by a barrier. It decides whether an X needs one. Meet
  is union.

**Joins.** A phi of pointers is a new tracked object. It is fresh at block
entry if each input is fresh at the end of its predecessor. Publishing the
phi publishes all its inputs, and publishing an input publishes the phi.

**Loops.** The danger is a variable holding the *previous* iteration's
object:

```go
var q *T
for {
    p := new(T)     // the same SSA value each iteration
    if q != nil {
        q.s = s     // the last p: published below
    }
    p.s = s
    G = p
    q = p
}
```

An allocation makes its own value fresh again, but `q` is a phi, a distinct
object, and it is fresh only if the value reaching it along the back edge
was fresh **at the end of the latch**. Here it was not. So identity is per
SSA value, and a phi never inherits the freshness of a new instance.

**Panics and `recover`.** Handled by the rules of §2: a `defer` that
captures the object is X at the `defer`, and so is a store to a named result
in a function with a `defer`. A panic with no deferred function that can
reach the object is not X.

### 7.2 Across calls

A call keeps the window open under the rule at the end of §2. A callee that
stores *into* the object (`Mutator`) is not X, but it does mean a later load
from the object may yield a pointer the caller knows nothing about (§7.3).

**A fresh-result contract** says of a result: on every return it is a new
object, not yet published, and everything it reaches was stored by this
function or is itself fresh. It would be computed bottom-up and exported
with the escape tags.

**A function that returns an object without such a contract must seal it
before it returns**, since the caller may store it racily with no barrier.
With a contract the seal moves to the caller's X.

The numbers say the contract is not worth its export-data format: at most
0.5 points more in `go/types` and nothing elsewhere. `runtime.maybeInPlace`
(the out-buffer of escape-bits phase G) already gives a caller the result
storage in the common case. `unpublished` does not recognize it, so a
constructor that goes through it is guarded today. Adding it there is a
one-line change, if anyone wants one.

### 7.3 Objects reachable from the object

- **A fresh child stored in a fresh parent:** the child's X is the parent's.
  The links are flow-insensitive: if any store puts c in p, publishing p
  publishes c.
- **A content leak** (`Heap > 0`) publishes the children and not the parent.
- **A pointer loaded back out of a fresh object** is tracked for what it
  publishes, and **never proves anything fresh.** It may be the child
  stored there, or a pointer stored on another path, or by a callee. So
  `G = p.child` publishes the child, and `p.child.x = 1` stays guarded.

### 7.4 The barrier

| Target | Barrier | Note |
| --- | --- | --- |
| amd64 | none | Stores are ordered. Today's `unpublished` is sound here. |
| s390x | none | Stores are ordered (`publicationBarrier` is empty). |
| arm64 | `DMB ST` (what `PubBarrier` lowers to) | `DMB ISHST` is enough for threads, and is what `shareStoreIface` uses. |
| ppc64 | `LWSYNC` | |
| riscv64 | `FENCE W, W` | |
| loong64 | `DBAR 0x1A` | |
| mips64 | `SYNC` | A full barrier. |

**`DMB ISHST` or `STLR`, when X is a store?** `STLR` orders all earlier
accesses before itself, with no separate instruction, so it is the better
choice *when X is one pointer store to a known address*. It takes no offset
addressing, it does not exist on the other targets, and it does nothing for
X at a call or a return. There a `DMB ISHST` goes **before** the call or
return. An X that is a guarded interface store needs nothing extra: its
`DMB ISHST` after the CAS already orders everything before it. An atomic
store is its own release.

**Merging.** One barrier covers every plain store before it, of any object,
so a barrier is needed at an X only if the dirty set of what it publishes
is not empty, and it empties the whole set.

**The proposed simplification:** on CPUs with single-copy-atomic 16-byte
stores, always store strings and slices whole, even in new objects, with no
barrier. **True for tearing**: a reader then sees zero or the whole value,
and what the value points to is safe by its own guarantee. But:

- It does not help **interfaces**, which are 32 bytes on every target and
  are where the hole is worst.
- It holds only with LSE2. arm64 cores before Armv8.4 (Cortex-A72, say)
  would take the stripe path for each store, or need the seal anyway.
- It needs a load and a branch on `runtime.stringAtomic`, unless the target
  level implies it.
- It costs up to +0.83% instructions and +0.5% to +3.0% text (§4.1), to
  solve a part of what one barrier per fill solves entirely.

**Not recommended.**

### 7.5 The collector

- **Nothing in the collector reads an interface's type word.** No file of
  `mgc*`, `mbitmap*`, `mwbbuf`, `stack.go`, `mfinal`, `mcleanup` or
  `malloc*` refers to `IsSpreadIface`, `IsInlineIface` or their flags.
  Scanning goes by the pointer bitmap, word by word. The data word is always
  a pointer word: a heap pointer, nil, or an inline string's tagged word,
  which `spanOf` rejects (`sso-string.md` §2). The other readers of the
  forms (`alg.go`, `error.go`, `arena.go`, `cgocall.go`) read a value their
  own goroutine holds.
- **So a plain multi-word store is safe against concurrent scanning.** The
  scanner may see any mix of old and new words, and each is a valid pointer
  word or is ignored.
- **Write barriers stay** on plain stores, as `needwb` decides them today.
  The window changes the lowering of the store, not what the collector is
  told.
- **Stack copying** does not touch heap objects, and a pointer to one in a
  frame is adjusted or not as usual. Stack objects are never guarded.
- **Asynchronous preemption** may land between plain stores. Nothing else
  can reach the object, so nothing observes it.
- **Goroutine migration.** If a goroutine moves between its plain stores and
  its barrier, the barrier runs on another CPU. The scheduler's handoff
  orders the first CPU's stores before the second CPU's later ones. This
  relies on multi-copy atomicity on arm64 (which ARMv8 has) and on the
  scheduler's atomics being full barriers on ppc64, **which I did not
  check**. The seal of §5 has the same exposure, though only if preemption
  lands inside a fill.

## 8. Maps

If `make(map)` followed by a fill skipped the compare-and-swap until X:

- **Entry points.** There are six assign and four delete variants, and
  about 40 uses of the lock. A `local bool` on the common paths is the least code;
  duplicated entry points are the fastest. A flag in the map header is ruled
  out, since nothing may be marked at run time.
- **Growth** within the window needs no lock either. Its existing store
  barriers before each table is installed stay, and are cheap next to a
  growth.
- **The barrier at X** must cover the header, directory, tables, groups and
  control words. Since one barrier covers every earlier store, it does.
- **A later reader** finds `seq` even and unchanged, and reads a map whose
  stores were all visible before its pointer was. It sees a consistent map.
- **Writers after X** take the compare-and-swap as now, starting from that
  `seq`.

**Not worth it.** 21.6% of map writes are provably in a window. In the
compile, 27% of 5.3M writes is 1.45M compare-and-swaps; at about 20 cycles
each that is some 29M of roughly 20G cycles, **about 0.15%** (an estimate).
A write already costs far more than its lock.

## 9. Soundness of the seal

Claim: with §5, no goroutine can load a torn string, slice or interface
from an object filled through `unpublished`.

1. On a weakly ordered target, `unpublished(addr)` holds only in a function
   with no `defer`, and only if the memory chain from here back to the
   allocation holds nothing but stores into that object and into the stack.
   So no pointer to the object has been stored outside the stack, and no
   call has been made since the allocation. **No other goroutine can hold a
   pointer to it.** A pointer in the stack cannot be read by another
   goroutine.
2. The barrier follows the last plain store of the run, and precedes the
   next memory operation of any other kind and the end of the block. Any
   store that could make the pointer visible is such an operation, or is in
   a later block, or is in a callee. A fault inside the run leaves the
   function only through a deferred call or by ending the goroutine's
   program, and the function has no `defer`; a caller's deferred function
   cannot reach the object, whose pointer never left this frame. So **every
   plain store is ordered before every store that could publish the
   pointer**, on every path.
3. After the run, `unpublished` is false for any further access unless the
   chain is still only stores into the object, in which case the run simply
   continues and the barrier moves after it. Otherwise the access is guarded.
4. A reader gets the pointer from a store covered by (2), and reaches the
   fields through an address dependency, so it sees all the plain stores. It
   then sees either those values or a later *guarded* store, which is whole.
5. The allocator's own `publicationBarrier` orders the zeroing before the
   fill, so a reader never sees memory older than the zeroed object.

This argument does not cover the bytes behind a string or slice pointer
from `needzero=false` allocations, or the `reflect.Value` race of §3. Phase
1 closes both by other means (§13.1).

A closure, or any object, with **no** string, slice or interface among its
fields is not sealed. A racing reader may see one of its words still zero:
a nil code pointer or a nil pointer, which panics. That is a stale value,
as in stock Go, and not a tear.

## 10. What is not verified

- The litmus tests ran on one weakly ordered CPU, an Apple M4. **ppc64,
  riscv64, loong64 and mips64 were compiled, and their barrier is checked in
  the assembly, but nothing ran on them.** A pass on the M4 is evidence
  about the M4's reordering, not a proof.
- The audit of §3 is mostly from reading the source (see its last column),
  and the rows marked partial checked only the publication points.
- `materializeToHeap`'s barrier has no litmus test of its own.
- Window savings (§4) are from a prototype whose sites lined up at 96.6%,
  and it did not model open-coded defers.
- Multi-copy atomicity on ppc64 across a goroutine migration (§7.5).
- Whether keeping guards as high-level ops would shrink code (§6).
- The list in §13.5 is from reading the source; no race on those types was
  run.

## 11. Build plan

**Phase 1: close the hole. Built (§13).** Small, and useful alone.

1. A litmus test first: a writer that fills `&T{s, i}` and stores it racily
   in a loop, a reader that checks each string and interface it loads. It
   should **fail on arm64 hardware before the change**, or the premise of
   this document needs another look.
2. The seal of §5, on the five weakly ordered 64-bit targets, and on those
   targets no window in functions with a `defer`.
3. `publicationBarrier()` after the stack-source copies in `growslice`,
   `makeslicecopy`, `moveSlice` and `moveSliceNoCap`.
4. A compiler test that the arm64 output has one barrier per fill, between
   the last store and the publishing store, and that amd64 output is
   unchanged.
5. Measure on arm64: today, seal, and skip removed. **If the seal costs more
   than removing the skip, remove the skip.**

**Phase 2: the stale bytes of `needzero=false` allocations (decision 3).
Decided, and built with phase 1 (§13.1).**

**Phase 3, not planned.** The analysis of §6 and §7 only if one of these
becomes true: arm64 measurements show barriers or stores costing far more
than amd64's counts suggest, or goroutine-local arenas need X as a checked
property.

**Separately:** `reflect.Value` (§3). **Built (§13.1).**

## 12. Decisions

Taken: the seal and no X analysis (1); it was proved on an Apple M4 (2);
stale bytes are a safety violation and are closed (3); `reflect.Value` is in
scope (4).

Still open:

- **Guards as high-level ops** (the old decision 5). They are 16% to 32% of
  text. That is the larger prize this work turned up, and it is not about
  publication.
- **32-bit `arm` and `mips`** have the hole of §3 and are not sealed.
- **The types of §13.5** are not fixed.
- **`fmt` pays for `reflect.Value`** when it prints a structure (§13.3).
  The cost was not attributed. `internal/fmtsort`, which holds a map's keys
  and elements as a `[]KeyValue` of Values, is the first place to look.

## 13. Phase 1 as built

Branch `publication-point`: `5641b410e0` (the seal), `5c7a35755a` (stale
bytes), `e7a8e1c45e` (`reflect.Value`).

### 13.1 What changed

**The seal** is §5, as built there.

**Stale bytes.** Memory from `mallocgc(..., needzero=false)` holds what a
freed object left. Each path that can reach user code:

| Path | Who fills it | What was done |
| --- | --- | --- |
| `concatstrings`, `concatbytes`, `slicebytetostring`, `stringtoslicebyte`, `stringtoslicerune`, `slicerunetostring`, `intstring`, `gostring`, `gostringn`, `gobytes`, `stringDataHeap`, `cgoStringNormalize`, arena clone | The runtime | Barrier after the fill, unless the result is in the caller's stack buffer. |
| `makeslicecopy`, `moveSliceNoScan`, `moveSliceNoCapNoScan` | The runtime | Barrier after the copy and the clear of the tail. |
| `make` + `copy`, inlined by `walk` as `mallocgc` + `memmove` | Compiled code | On weakly ordered targets it calls `makeslicecopy` instead. |
| `growslice`, the capacity tail | The runtime | Cleared as before. A barrier now orders that before the slice. |
| `growslice`, the new elements `[oldLen, newLen)` | **The caller, after it returns** | A barrier cannot help. Weakly ordered targets, and targets whose slices are not packed, clear them too. |
| `s.f = append(s.f, x)` | Compiled code | The slice is now stored **after** its elements, on every 64-bit target. This is what fixes amd64, at no cost: it removes a spill. |
| `bytealg.MakeNoZero` (`strings.Builder`, `bytes`) | **The caller, after it returns** | Cleared on weakly ordered targets. On amd64 and s390x, callers store the slice after filling what it shows, and none exposes its capacity. |
| `convT*` boxes | The runtime | Nothing. Every store of an interface orders the box first: its lock's barrier, or the seal. |

A racing reader of a slice may therefore see zeros where elements are yet
to arrive, and never another object's bytes.

**`reflect.Value`** goes as an interface does. `types.IsWholeStruct` and
the runtime's `isWholeStruct` recognize it, by package path and name, and
it counts as holding an interface (`HasIface`, `tearKind`). So structs,
arrays, slices, maps and `typedmemmove` follow from the existing paths.
Compiled code loads it under the sequence lock of the block where it starts
and stores it holding that lock, with the write barrier inside, as
`shareStoreIface` does. Values in the stack are not guarded.
`internal/reflectlite.Value` is not covered (§13.5).

### 13.2 Litmus tests

`runtime/publication_test.go` and `reflect/value_whole_test.go`. They run
100 ms each with `-short`, 1 s otherwise, and `GD_PUB_LITMUS=60s` sets the
time. Before is `105a31a6ac` with the tests added. The M4 ran native arm64
binaries on all its cores, with five writer and reader pairs (`Value`: one
writer, three readers). Threads were not pinned to a core type.

| Test | Before, on the M4 | After, on the M4 |
| --- | --- | --- |
| Fill, one or two lines | 30 s: 176 torn strings, 1,627 torn interfaces, about 4,300 unfilled fields, of 4.3G reads | 60 s: none |
| Fill, several lines | 30 s: 562 torn strings, 1,168 torn interfaces, about 219,000 unfilled fields, of 4.8G | 60 s: none |
| Closure, method value | 30 s: 5,277 torn strings, 98,713 panics, about 141,000 unfilled, of 6.7G | 60 s: none |
| Grown from the stack | 40 s: 1,080 torn strings, 20,454 unfilled, of 10.3G | 60 s: none |
| Stale: `slicebytetostring` | 30 s: 6,029 | 60 s: none |
| Stale: concatenation | 30 s: 3,357 | 60 s: none |
| Stale: `[]byte(s)` | 30 s: 12,500 | 60 s: none |
| Stale: `[]rune(s)` | 30 s: 560 | 60 s: none |
| Stale: `string(runes)` | 30 s: **1** | 60 s + 300 s: none |
| Stale: `strings.Repeat` | 30 s: 119 | 60 s: none |
| Stale: `strings.Builder` | 30 s: 1,174 | 60 s: none |
| Stale: `bytes.Repeat` | 30 s: 2,728 | 60 s: none |
| Stale: `append` | 30 s: 11,691 | 60 s: none |
| Stale: append in place | 30 s: 76,682 | 60 s: none |
| `Value`: global, field, struct, array, slice, copy, reflect | 15 s each: 12M to 226M torn per test | 15 s each: none |
| `Value`: large struct, clear, map element, map key | 15 s each: 10K to 166M torn or panics per test | 15 s each: none |
| `Value`: channel | 15 s: **passes before too** (the channel's lock) | 15 s: none |

On amd64 (32 threads, this machine): the fill, closure and grow tests pass
before, as they should. The append in place had 2,823 stale reads of 729M in
20 s before, and none of 467M after. The `Value` tests failed before, except
the channel's, and pass after.

A pass is evidence, and weakest for `string(runes)`, which failed once in 30
seconds before. That is why it ran for 300 more.

### 13.3 Cost

**On the M4**, as times of benchmarks and of `compile -c=1` of `go/types`,
`GOMAXPROCS=4`, not pinned. Each round runs every variant once. The figure
is the median of the per-round ratio to today's build, with quartiles in
brackets. Every build started from an empty cache. macOS has no `perf`, and
two campaigns of the same builds differed by up to 0.7 points on http and 2
points on json encode, so **differences under about 1% (2% for json) are not
resolved.** `Sprintf` of an int and `ManyArgs` varied by more than 10% between
rounds on the M4 and are left out; their instruction counts are below.

Seal against removing the skip (20 rounds):

| Workload | Seal | Skip removed |
| --- | ---: | ---: |
| compile `go/types` | +1.07% [+0.80, +1.59] | +3.39% [+3.06, +3.89] |
| http ClientServer | +0.67% [+0.33, +1.37] | +3.51% [+3.14, +4.24] |
| go/parser | +1.37% [+1.24, +1.73] | +2.64% [+2.06, +3.30] |
| json encode | +0.89% [-0.09, +1.86] | -0.26% [-1.01, +1.27] |
| json decode | -0.67% [-1.32, -0.19] | +0.82% [+0.53, +1.22] |
| go/printer | -0.34% [-1.15, +0.49] | -0.80% [-1.13, -0.21] |
| size of `compile` | +0.16% | +1.28% |

**The seal costs less than removing the skip wherever the difference is
resolved, so the seal stays.**

All three, cumulative (15 rounds, a separate campaign):

| Workload | Seal | + stale bytes | + `Value` |
| --- | ---: | ---: | ---: |
| compile `go/types` | +1.09% [+0.92, +1.77] | +1.79% [+1.39, +1.92] | +1.70% [+1.25, +1.93] |
| http ClientServer | +1.38% [+0.53, +1.58] | +1.48% [+1.13, +2.63] | +1.21% [+0.88, +1.35] |
| go/parser | +1.62% [+1.18, +1.89] | +2.08% [+1.56, +2.78] | +2.03% [+1.45, +2.32] |
| json encode | -1.08% [-2.04, -0.04] | +0.22% [-2.71, +1.06] | -1.20% [-2.68, -0.30] |
| json decode | -1.03% [-1.66, -0.28] | +0.27% [-0.73, +0.70] | +0.95% [+0.55, +1.70] |
| go/printer | -0.44% [-0.92, +0.51] | -0.32% [-0.73, +0.16] | -0.01% [-0.49, +0.72] |
| fmt `SprintfStructure` | -0.56% [-0.86, +0.67] | +0.74% [-1.32, +1.48] | **+7.07%** [+5.98, +8.01] |

**On amd64**, user-mode instructions, `perf stat -r 3`, `GOGC=off`, each
build from an empty cache. The seal is zero here (§13.4). The largest spread
between runs was 0.09%.

| Workload | Stale bytes, against today | `Value`, against stale bytes |
| --- | ---: | ---: |
| gofmt | -0.24% | -0.01% |
| json encode | -0.15% | +0.01% |
| json decode | -0.03% | +0.08% |
| compile `go/types` | -0.37% | +0.11% |
| http FakeConn | -0.04% | +0.26% |
| http ClientServer | -1.05% | -0.30% |
| fmt `Sprintf` of scalars and strings | -0.97% | -0.02% |
| fmt `SprintfStructure` | -0.54% | **+8.55%** |
| fmt `ManyArgs` | +0.10% | -0.06% |

The stale-byte change makes amd64 slightly **faster**: storing the slice
after its elements removes a spill in every append in place. `Value` costs
nothing measurable in these workloads except one: `fmt` printing a slice
that holds a struct and a map with `%#v` pays 8.5% in instructions on amd64
and about 7% in time on the M4.

### 13.4 amd64 and s390x text under the seal

`gofmt`, `vet`, `link`, `asm`, `cover`, `json.test` and `http.test`, for
amd64 and for s390x, built with `-a -trimpath` by `105a31a6ac` and by
`5641b410e0`: the `.text` sections of all 14 binaries are **byte for byte
identical.** This covers the seal only. The stale-byte and `Value` commits do
change amd64 code, as §13.3 measures.

### 13.5 Other types that depend on words staying together

From reading the source. **None is fixed, and no race on them was run.**

| Type | Words | What a tear does |
| --- | --- | --- |
| `log/slog.Value` (and `Attr`, and `Record`, which holds them) | `num` is the length of the `groupptr` in `any` | `Group()` makes a slice with the pointer of one value and the length of another: reads out of bounds. Public API. |
| `reflect.MapIter` (`maps.Iter`) | Table, group, indices, key and element pointers | Racing calls of `Next` on one iterator can combine the state of two positions. Needs its own check. |
| `internal/reflectlite.Value` | As `reflect.Value` | Type confusion, if one were ever raced on. It is internal, and I found it only in locals and arguments (`errors`). |
| `internal/trace.Value` | `kind`, `pointer`, `scalar` | `String` makes a string from the pointer and the scalar. Internal. |
| `sync.Pool` | `local` and `localSize` | Published in order with atomics, so sound in use. A racy **copy** of a Pool (which `vet` rejects) could pair them, and `indexLocal` does not check. |

### 13.6 What ran where

| Suite | Machine | Result |
| --- | --- | --- |
| `./run.bash --no-rebuild` | linux/amd64 | ALL TESTS PASSED |
| `go test -short std cmd/internal/testdir` | linux/amd64 | pass |
| `runtime`, `reflect`, `sync`, `internal/runtime/maps`, `-short`, native | M4 | pass |
| `Test/codegen/{publication,slices,append}.go`, native | M4 | pass |
| `runtime`, `reflect`, `sync`, `internal/runtime/maps`, `GOEXPERIMENT=sharing`, `-short` | linux/amd64 | pass |
| `go build std` for 386, arm, mips, mipsle, mips64, mips64le, ppc64, ppc64le, riscv64, loong64, s390x, js/wasm (also `GOWASM=gc,split`), windows | cross | builds |
| `Test/codegen` with `-all_codegen` | linux/amd64 | **49 assertions fail** (js/wasm 32, ppc64 9, ppc64le 3, s390x 5), the same 49 at `105a31a6ac`. CI does not run this mode. Not fixed. |

Not run: `std` on the M4; the CI jobs for linux/arm64, windows and js/wasm;
anything on real ppc64, riscv64, loong64 or mips64 hardware.

`cmd/go/internal/work` fails if `GOTMPDIR` is set and differs from `TMPDIR`,
at `105a31a6ac` too: its tests take their directory from one and its check
from the other. `run.bash` above ran with both set to the same directory.
