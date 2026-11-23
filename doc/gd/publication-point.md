# The publication point (design, 2026-11-23)

A data race must never be memory-unsafe: racing goroutines may see stale
values, never torn ones. This document asks whether the guards that give
that guarantee (`fat-iface.md`, `sso-string.md`, `slice16.md`, `maps.md`)
can be skipped until the first point where another goroutine could reach
an object, and what that would be worth. Nothing here is implemented.
Measured at c7a0e1fde2, on amd64.

## 0. Summary

**The idea is sound, and the general version is not worth building.**

- **There is a real hole today.** `(*state).unpublished` skips the guards
  in a new object and issues no barrier. On every weakly ordered target a
  racing reader can get a string or interface made of some of its words
  (§3). The arm64 code shows it; it has **not been reproduced** on
  hardware.
- **It can be closed for almost nothing, with no new analysis.** Keep
  today's window, and put one store barrier after the last plain store of
  each straight-line fill (§5). The barrier does not have to be at X, only
  between the fill and X. In the measured programs that is at most **0.08
  barriers per 1000 instructions**, and none on amd64 or s390x. Removing
  the skip instead costs up to **+6.95%** instructions on amd64.
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

The recommendation is phase 1 of §11 and nothing else for now. §6 to §8
keep the full design, because goroutine-local arenas need the same
definition of X, and so that the question need not be reopened from
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
| The same from a **heap** source | No | `shareCopyFrom` copies whole, or under stripes. | read |
| Static part of a literal (`*p = stat`) | No | `shareMove` ignores `unpublished`. | read |
| `convT*` boxes | No, in themselves | A guarded interface store has a `DMB ISHST` after its CAS, which orders the box first. The *interface* holding the box may tear, as in row 1. | read |
| String conversions, `concatstrings` | Header no; **stale bytes yes** | `needzero=false`, a byte fill, a header store: a reader can see the old bytes of a freed object. Not a pointer/length tear (decision 3). | read |
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
can pair the type of one value with the pointer of another: type confusion.
The shape is read from `reflect/value.go`; I did not build a reproducer.

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
per 1000 instructions: under 0.5% at any plausible IPC. **The 30 cycles is
an assumption. Nothing here ran on arm64.**

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

**One exception: functions with a `defer`.** A nil dereference while
computing a later field (`p.b = q.x`) faults in the middle of a fill, with
no block boundary, and the panic may run a deferred closure that publishes
`p`. An open-coded `defer` leaves only stack stores in the chain, so
`unpublished` does not see it. **`unpublished` must return false in a
function with a `defer`** (`s.hasdefer`). What that costs was not measured
separately; it is part of the "skip removed" column, for those functions
only.

**What it gives up.** An object that is never published pays for a barrier
it did not need. §4.4 bounds that.

### Implementation sketch

1. `unpublished` stores are marked as `dstLocal` stores are (`Store`'s
   auxint), with a value of their own.
2. At the end of `buildssa`, after `insertPhis` and before `ssa.Compile`, a
   small function walks each block's memory chain. It inserts one
   `OpPubBarrier` before the first memory operation after a marked store
   that is neither another marked store nor a store to the stack, and at
   the end of the block if the run reaches it.
3. It runs that early because `memcombine` and `dse` may merge or remove
   marked stores. Once inserted, the barrier is an ordinary memory
   operation, and no pass moves a store across it.
4. Targets: arm64, ppc64, riscv64, loong64, mips64. `OpPubBarrier` is
   already lowered on all of them. Nothing on amd64 and s390x, whose stores
   are ordered, so their code does not change at all.

Also needed, in the runtime: a `publicationBarrier()` after the plain
`memmove` from a stack source in `growslice`, `makeslicecopy`, `moveSlice`
and `moveSliceNoCap`. That closes the third row of the audit.

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

1. `unpublished(addr)` holds only in a function with no `defer`, and only if the memory chain from here back to the
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

What this argument does **not** cover: bytes behind a string or slice
pointer from `needzero=false` allocations (decision 3), and the
`reflect.Value` race of §3.

## 10. What I could not verify

- **Nothing ran on arm64 or any weakly ordered CPU.** The hole is shown by
  generated code and has not been reproduced. The barrier's cost is an
  assumption.
- The audit is mostly from reading the source (see its last column), and
  the rows marked partial checked only the publication points.
- Window savings are from a prototype whose sites lined up at 96.6%, and it
  did not model open-coded defers.
- Multi-copy atomicity on ppc64 across a goroutine migration (§7.5).
- Whether keeping guards as high-level ops would shrink code (§6).

## 11. Build plan

**Phase 1: close the hole.** Small, and useful alone.

1. A litmus test first: a writer that fills `&T{s, i}` and stores it racily
   in a loop, a reader that checks each string and interface it loads. It
   should **fail on arm64 hardware before the change**, or the premise of
   this document needs another look.
2. The seal of §5, on the five weakly ordered 64-bit targets, and no window
   in functions with a `defer`.
3. `publicationBarrier()` after the stack-source copies in `growslice`,
   `makeslicecopy`, `moveSlice` and `moveSliceNoCap`.
4. A compiler test that the arm64 output has one barrier per fill, between
   the last store and the publishing store, and that amd64 output is
   unchanged.
5. Measure on arm64: today, seal, and skip removed. **If the seal costs more
   than removing the skip, remove the skip.**

**Phase 2, only if decided:** the stale-bytes barrier for `needzero=false`
allocations (decision 3).

**Phase 3, not planned.** The analysis of §6 and §7 only if one of these
becomes true: arm64 measurements show barriers or stores costing far more
than amd64's counts suggest, or goroutine-local arenas need X as a checked
property.

**Separately:** `reflect.Value` (§3).

## 12. Decisions for the user

1. **Accept the seal as the fix, and stop there?** The alternative is the
   full X analysis, which measures under 1%.
2. **Where should phase 1 be proved?** It needs arm64 hardware, for the
   litmus test and the cost of the barrier. Nothing has been run on it.
3. **Are stale bytes a safety violation?** A racing reader of a new string
   can see the previous contents of freed memory. It is not a torn value and
   breaks no pointer, but it can expose another object's data. Closing it is
   one barrier per such allocation.
4. **Is `reflect.Value` in scope** for "a data race is memory safe"? As it
   stands, a race on one can confuse types.
5. **Should guards as high-level ops be looked at on their own?** They are
   16% to 32% of text. That is the larger prize this work turned up, and it
   is not about publication.
