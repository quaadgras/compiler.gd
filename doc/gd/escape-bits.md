# Dynamic escape bits for closures and interface methods (gd fork)

Goal: eliminate the heap allocations that stock Go forces on pointer
arguments to indirect calls (closure invocations and interface method
dispatch). The gd fork already computes per-parameter escape info per
function; we just need to ship that info across the indirect-call
boundary so the caller can keep args on the stack when the specific
callee doesn't let them escape. README optimization goal #3.

> **Status:** Phase A–D are active fork-wide as of commit 9120642bae
> (2026-04-23). Phase F and G runtime helpers landed in the same
> commit; their compile-time automation is still in-progress. This
> top section describes the landed state; the phased plan below is
> preserved as design history. See §Landed state for the
> as-implemented summary.

## Landed state

The optimisation runs unconditionally: `shouldEnableEscapeCandidate`
in `cmd/compile/internal/escape/escape.go` returns `true` for every
function. Safety is enforced by three surgical filters, all narrower
than the original plan:

**1. `isCandidateLoc` only admits ONAME PAUTOs with Addrtaken.**
Fresh-allocation expressions (ONEW, OPTRLIT, OCONVIFACE, composite
literals) never become candidates. This is narrower than the plan's
original "all allocation ops" — it was narrowed because recursive
allocation graphs (fmt's RecursiveInt chain) and struct-field-stored
pointers inside composite literals were being over-eagerly stack-
promoted. The current rule captures the bread-and-butter win
(`&localvar` to a dynamic callee) without the aliasing hazards.

**2. `candidateEligibleParamType` gates `tagHole`.** Dynamic callees
only route pointer-shaped args (*T, unsafe.Pointer, chan) through
`candidateHole`. Struct-by-value, slice, map, interface, and func
args fall back to `heapHole`, matching stock Go's behaviour exactly.
This keeps `&x` nested inside a struct value arg (`f(P{p: &x})`)
from being candidate-routed despite the struct's methods escaping
the field. Per-field escape tracking to reclaim the struct case is
planned — see `memory/project_escape_bits_field_escape_roadmap.md`.

**3. Interface-method receivers are excluded from candidate-eligible
pointers.** Go synthesises iface-method receivers as `*struct{}` —
an opaque pointer to empty-struct standing in for the iface's data
word. A naïve pointer-kind check would treat them as real pointer
args; we reject `*T` where `T.Kind()==TSTRUCT && T.NumFields()==0`
so the iface receiver chain (e.g. `Handler → TextHandler →
commonHandler.w → &buf`) isn't pulled into candidate propagation.

### Box promotion via Heapaddr

For every PAUTO local tagged EscCandidate, walk's
`promoteEscapeCandidates` (in `cmd/compile/internal/walk/
escape_bits.go`) emits:

```
var <name>_backing T         // PAUTO, SetEsc(EscHeap) so
                             // liveness/shouldTrack skips it
var &<name> *T               // PAUTO, stack-allocated
&<name> = &<name>_backing    // prologue, top of fn body
<name>.Heapaddr = &<name>    // SSA gen routes accesses through *Heapaddr
```

All reads/writes of the promoted name go through `*Heapaddr`; every
`&name` evaluates to the value of the `&name` pointer var. At a
dynamic call site, `wrapEscapeCandidateArgs` emits
`&name = runtime.maybeEscape{Closure,Iface}Arg(carrier, argIdx,
&name, typ)` so the pointer gets re-homed to a heap copy iff the
callee's mask bit fires. Post-call reads through `*Heapaddr` see the
migrated storage.

For `p := new(T); f(p)` patterns the wrap additionally updates the
ONAME's value in place (via `updateNameViaWrap`) so subsequent
`*p` reads stay consistent.

### Idempotent runtime materialize

`runtime.materializeToHeap` (`src/runtime/escape_bits.go`) checks
the src pointer against the current goroutine's stack range up
front: if src is already off-stack, it returns src unchanged. Hot
loops amortise any migration to zero allocs/run across iterations,
and repeat calls can't break caller-visible pointer identity
(e.g. `strings.Builder.copyCheck` panicking on `b.addr != b`).

### Phase F: dynamic compute-fn masks

`runtime.resolveMask(rawMask, carrier, heapMask, depth) uint64`
handles the static vs dynamic split:

- Bit 0 clear → rawMask is itself the static mask, return as-is.
- Bit 0 set → upper bits are a `computeMaskFn` pointer; invoke with
  the carrier + heapMask hint + decremented depth.

`computeMaskFn` signature: `func(carrier unsafe.Pointer, heapMask
uint64, depth int) uint64`. The heapMask lets the compute fn express
inter-argument relationships ("arg A escapes iff arg B is on heap").
Depth cap (`maxComputeMaskDepth = 8`) with
`conservativeAllEscapeMask` fallback prevents infinite recursion on
cyclic wrapper chains. Bit 0 stripped from the return to keep the
discriminator unambiguous.

#### Phase F1: per-call-site mask resolution (landed)

Walk now open-codes the bit-0 fast path at each indirect call site
that has at least one EscCandidate arg, instead of routing every
candidate arg through a per-arg helper that re-resolved the mask
in isolation. The emitted shape:

```
raw  := *(*uint64)(carrier + maskOffset)
mask := raw                                       // static fast path
if raw & 1 != 0 {
    hm := uint64(0)
    if isOnHeap(box_0) { hm |= 1 << 1 }
    if isOnHeap(box_1) { hm |= 1 << 2 }
    ...
    mask = runtime.resolveMaskSlow(raw, computeFnCarrier, hm, 8)
}
// per-arg materialise
if mask & (1 << (i+1)) != 0 {
    box_i = runtime.materializeToHeap(box_i, &T_i)
}
```

Two payoffs:

1. **Static-mask path is call-free.** The dominant case (every
   stdlib indirect-call site, today) is one load + one and-test +
   branch-not-taken before the per-arg materialise checks; no
   trip into the runtime for resolution.
2. **heapMask is precise.** Each candidate box's storage location
   is stack-range-checked once per call site, so a relational
   compute fn ("arg A escapes iff arg B is on heap") sees the
   right bits — the per-arg helpers used to pass `heapMask=0`
   unconditionally.

Carrier choice differs by dispatch shape:
- **Closure call**: carrier = closure value (mask lives at offset
  `PtrSize` of the closure header; compute fn reads captures from
  the same value).
- **Iface method call**: carrier = itab (for the mask load), but
  the **compute-fn carrier is the receiver data pointer** (so a
  method-receiver compute fn can reach `recv.field.itab.mask` of
  a delegated-to iface — symmetric to the closure case).

Implementation files:
- `cmd/compile/internal/walk/escape_bits.go` — `emitMaskResolve`,
  `emitMaybeMaterialize`, `rawLoadU64At`, `wrapClosureCallCandidates`,
  `wrapIfaceCallCandidates`.
- `runtime/escape_bits.go` — `resolveMaskSlow` (slow path split out
  so walk's bit-0 test stays open-coded), `isOnHeap` (per-box
  stack-range check).

The per-arg `maybeEscapeClosureArg` / `maybeEscapeIfaceArg`
runtime helpers are removed; only `maybeEscapeArg` (mask-already-
resolved form) remains for tests.

> **Note (Phase F2 removed)**: an earlier iteration shipped a
> `//gd:escmask <ident>` pragma so users could pair functions with
> hand-written compute fns. F4's auto-detector + synthesizer cover
> the dominant trivial-forwarder case automatically, so the pragma
> was removed to keep the surface area small. If a future use case
> needs user-supplied compute fns (relational masks, non-trivial
> forwarders, closure-capture cases), the install machinery in
> reflectdata/staticdata is parameterised on
> `ir.Func.GdForwarder.SyntheticComputeFn` and could accept a
> user-named alternative with minimal extra plumbing.

#### Phase F3: trivial-forwarder detector (landed)

`escape.DetectForwarders` runs in `escape.Batch` after
`batch.finish` (so leaks are populated). It tags trivial-forwarder
functions with `ir.Func.GdForwarder` for F4's compute-fn synthesis
to consume.

Accepted body shapes:

1. **Single-return-of-call**: `return inner.M(args)` with the
   call's CallExpr directly in `ReturnStmt.Results[0]`.
2. **Multi-result return**: typecheck splits the multi-result
   call into autotmps via `ir.InitExpr`; the AssignListStmt sits
   in `Results[0].Init()`. The detector unwraps and extracts the
   inner call.
3. **Two-stmt assign-then-return**: `tmp1, tmpN = inner.M(args)`
   followed by `return tmp1, tmpN`. Same shape, different
   physical encoding.
4. **Void call**: body is just the inner CallExpr.

Eligibility filters:
- Op is `OCALLINTER` (concrete-method dispatch is already precise
  at the static-mask level).
- `sel.X` is a load through the receiver's field, where the
  field's type is a non-empty interface. Closure-capture
  forwarders are deferred.
- Args are exactly the wrapper's params (excluding receiver) in
  source order; no reordering, slicing, or extras.
- Function isn't a generic shape instance (dictionary-routed
  dispatch not handled).

Stdlib match set as of landing: 197 autogenerated embedded-iface
wrappers (e.g. `(*loggingConn).RemoteAddr`,
`(*onlyValuesCtx).Done`) + 79 user-written forwarders
(e.g. `(*Rand).Uint64`, `(*HMAC).Write`, `(*checksumReader).Close`,
`stringWriter.WriteString`). Run `-gcflags=all=-d=gdforwarder=1`
to dump matches; `=2` also prints rejection reasons.

#### Phase F4: synthesised compute fns + install (partial)

For each forwarder F3's detector tagged with
`ir.Func.GdForwarder`, F4's `escape.SynthesizeForwarderComputeFns`
emits a tiny synthetic compute fn:

```
func .gdfwd.<wrapperLinkName>(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
    return runtime.resolveForwardedRecvFieldMask(
        carrier,
        <FieldOffset>,    // typed uintptr literal
        <MethodIdx>,      // typed int literal
        heapMask,
        depth,
    )
}
```

The runtime helper:
- Reinterprets `carrier` as `*WrapperType` and loads the iface
  header at `carrier + FieldOffset`.
- Reads the inner itab's per-method mask slot for `MethodIdx`.
- Tail-calls `resolveMask` with the inner itab as the new
  carrier and the same heapMask.

Synthesis adds the cfn to `typecheck.Target.Funcs` (via
`typecheck.DeclFunc`'s implicit append) so walk compiles it
through the normal pipeline.

**Install** (`reflectdata.FinalizeItabMasks` and
`staticdata.WriteFuncSyms`) writes
`objw.SymPtr(slot, off, computeFnFuncsym, 1)` into the wrapper's
mask carrier — same machinery as F2.

**Status (default-on)**: detector, synthesizer, and install all run
unconditionally. The previous parallel-compile race that surfaced as
a `defframe → partLiveArgs[n]` nil-deref under all.bash load was
chased to stale tooling (an older `pkg/tool/.../compile` binary that
still referenced removed runtime helpers `maybeEscapeClosureArg` /
`maybeEscapeIfaceArg`); a clean `rebuild-tools.sh` cycle resolves it.
A debug knob `-d=gdforwarderdisable=1` is retained as a measurement
escape hatch — useful for A/B comparing static-mask-only against the
dynamic-mask install path.

**The linker dedup fix**: when F4 install adds a SymPtr
relocation to the per-package synth funcsym, the itab's content
hash (`cmd/internal/obj.objfile.contentHash`) is salted with the
current package path for `PkgIdxSelf` references. The same
logical itab compiled from two packages then produces two
distinct hashes, defeating the linker's hashed-def dedup. The
two surviving itabs each carry their own `*_type` reference
chain; type assertions where the asserter and the iface-creator
end up on different chains panic with "T from different scopes".

`writeITab` resolves this by switching itabs to plain DUPOK
name-based dedup (skip `AttrContentAddressable`) when F4 install
is active. The cost: itabs no longer dedup across compilation
units that happen to produce identical content (rare in practice
because itabs are typically emitted by exactly one package — the
one doing the iface conversion). Funcsyms aren't affected because
they're already DUPOK rodata + non-content-addressable.

The detector + synthesizer + install all run by default;
`-d=gdforwarderdisable=1` opts the install (and the synthesizer
work that backs it) off for measurement.

### Phase G: return-value outbuf

`runtime.maybeInPlace(outBuf, typ) unsafe.Pointer` picks storage for
a callee's about-to-be-returned pointer-typed result:

- `outBuf != nil` → zero outBuf (via `typedmemclr` if it has pointer
  fields, so GC never sees stale words) and return it. Zero allocs.
- `outBuf == nil` → `mallocgc`, same cost as stock `new(T)`.

Symmetric to materializeToHeap on the return side. A hand-
transformed demo (`src/cmd/compile/internal/test/
escape_bits_outbuf_test.go`) shows −85% time and −100% allocs
against stock `return new(T)`; the nil-fallback path is within noise
of stock. Compile-time rewrite to automate the signature extension +
callee body rewrite + caller-side stack buffer is pending — see
`memory/project_escape_bits_phase_g_roadmap.md`.

#### Builtin/runtime sig parity (note for future map/chan changes)

`PhaseGApplies` checks `r.Type.IsPtr()`, which is true only for TPTR.
TMAP, TCHAN, TUNSAFEPTR, TINTER all return false, so functions whose
results are those kinds are **not** extended. The compiler's view of
runtime helpers comes from `cmd/compile/internal/typecheck/_builtin/
runtime.go`; the runtime's view comes from the actual `*.go` impl.
makemap*/makechan* are an asymmetric pair: builtin TMAP/TCHAN, runtime
*maps.Map / *hchan. The compiler emits stock-arity calls (no outBuf
register set), the runtime impl gets an extra outBuf register that's
dead in the body. Benign register-wise on amd64 — none of the make*
bodies match `phaseGReturnRewrite`'s `return new(T)` shape, so the
outBuf is never read. We tried switching the runtime returns to
`unsafe.Pointer` to make the sigs symmetric, but that broke
in-process compilation of the runtime package itself: the runtime
source's `func makemap(...) unsafe.Pointer` overrides
`InitRuntime`'s built-in TMAP-result Sym, so subsequent `make(...)`
emissions inside runtime get a TUNSAFEPTR-result callee that fails
to assign to a TMAP destination. Upstream uses *maps.Map / *hchan and
the asymmetry has been benign for years; we keep that here too.

### Tests in the tree

- `src/cmd/compile/internal/test/escape_bits_test.go` — 8 end-to-end
  `TestEscapeBits*` alloc tests and 6 `BenchmarkEscapeBits*`
  benchmarks demonstrating −71% geomean speedup and 100% alloc
  reduction on the canonical patterns.
- `src/cmd/compile/internal/test/escape_bits_dynmask_test.go` — 3
  Phase F dynamic-mask tests (forced escape override, call
  integrity, heapMask-driven relational decisions).
- `src/cmd/compile/internal/test/escape_bits_outbuf_test.go` — 4
  Phase G correctness tests + 3 benchmarks.
- `src/runtime/escape_bits_test.go` — 17 runtime-side tests covering
  materializeToHeap, maybeEscapeArg, resolveMask (static passthrough,
  bit-0 clamp, cycles, depth, heapMask), maybeInPlace.

### Pre-existing stdlib failures (not caused by escape-bits)

Verified by running with the gate off: `encoding/pem/TestFuzz`,
`go/doc/TestClassifyExamples`,
`go/doc/comment/TestTestdata/crash1.txt`, `log/syslog/TestFlap`,
`log/syslog/TestConcurrentReconnect`,
`log/syslog/TestWithSimulated`. Not regressions introduced by this
work.

---

# Design history (phased plan, preserved as-written)

## 0. Premise check

Escape analysis in `cmd/compile/internal/escape/` already produces
precise per-param escape decisions for every function. Each parameter
carries an 8-byte `leaks` encoding (heap derefs, mutator, callee flow,
per-result flow — `leaks.go:13–25`) persisted into `types.Field.Note`
as a `"esc:<leaks>"` string and exported through type signatures. When
a caller sees a statically-known callee, `tagHole` in `call.go:368`
reads `param.Note` and computes precise escape per arg. When the
callee is statically unknown — i.e. indirect through a closure value
or iface method — `call.go:371` returns `heapHole()` unconditionally:

```go
if fn == nil {
    return e.heapHole()    // blanket pessimism
}
```

Every indirect-call pointer arg escapes to heap as a matter of policy,
not because any specific callee demands it. The per-function profile
is already on disk. The gap is a runtime-accessible carrier for the
bits at the call site.

## 1. Mask layout

One bit per pointer-bearing parameter, in source order, packed into a
single `uint64` (`EscMask`). Bit *i* set ⇔ parameter *i* (by pointer-
bearing index, skipping scalar params) may reach the heap. Signatures
with more than 64 pointer-bearing params fall back to "all ones" from
bit 63 on — no correctness hazard, mild pessimism; such functions are
rare enough that the encoding shouldn't grow.

Derivation from `leaks`: `bit_i = (esc_i.Heap() >= 0)`. That's the
single question "does this param's content reach heap after enough
derefs?", which is also what the caller's allocator needs to answer.
We drop the richer mutator/callee/result-flow axes because the caller
doesn't need them to decide *its own* allocation; only whether to
materialize to heap before the call.

## 2. Carrier sites

### 2.1. Closures

Today's closure struct (`cmd/compile/internal/typecheck/func.go`,
`ClosureType`):

```go
struct{F uintptr; X0 <capture>; X1 <capture>; ...}
```

Grow the header by one word:

```go
struct{F uintptr; EscMask uint64; X0 <capture>; X1 <capture>; ...}
```

Cost: +8 B per closure instance. A typical closure is 16–32 B; ~5 %
average overhead. Closures with no captures today live in a single
shared rodata slot — they grow to 16 B rodata; trivial.

The call path (`OpClosureCall` → arch `CALLclosure`) today loads the
fnPtr from offset 0 of the closure value. The load-sequence update is
minimal: the caller-side check reads `8(closure)` before emitting the
call.

### 2.2. Interface methods

Today's `ITab.Fun` (`src/internal/abi/iface.go:20`):

```go
Fun [1]uintptr  // variable sized
```

Grow each slot to a 2-word entry:

```go
type ITabMethod struct { Fn uintptr; EscMask uint64 }
Fun [1]ITabMethod
```

Size: `sizeof(ITab{}) + (nmethods-1) * 16` instead of `* 8`. The fork
already grew the header with `Inline uint8 + [3]byte` padding, so
struct alignment is unchanged. Allocation path
(`src/runtime/iface.go:77`, `persistentalloc`) needs the sizeof arithmetic
updated; the method-pointer writer (`itabInit` at `iface.go:204` via
`rtyp.textOff(t.Ifn)`) grows to also write the mask, sourced from the
concrete method's `types.Field.Note`.

### 2.3. Direct calls and `//go:noescape`

No change. The compiler already has the callee's `param.Note` at its
fingertips; direct-call escape stays precise as today. `//go:noescape`
extern functions become "all-zero mask" at callers of indirect values
bound to them (rare — externs are typically called directly).

## 3. Call-site codegen

For `OCALLFUNC` on a closure and `OCALLINTER` on an iface, when
*any* pointer arg is an escape-candidate (see §4), emit:

```
// Pseudo-code, N pointer args:
mask := *(closure.EscMask) or *(itab.Fun[k].EscMask)
for i in 0..N {
    if mask & (1 << i) != 0 && args[i].homedOnStack() {
        args[i] = runtime.materializeToHeap(&stackCopy[i], rtype[i])
    }
}
call(fn, args...)
```

The `materializeToHeap` runtime helper:

```go
//go:nosplit
//go:noescape
func materializeToHeap(src unsafe.Pointer, typ *abi.Type) unsafe.Pointer {
    dst := mallocgc(typ.Size_, typ, true)
    typedmemmove(typ, dst, src)
    return dst
}
```

Args that were already on the heap before the call (pulled from a map,
returned from a function that allocated, loaded from a heap-reachable
container) skip the materialize — the compiler knows from the arg's
escape state upstream. A stack-homed arg that hits an escape edge
unrelated to the indirect call (e.g. also captured by a goroutine
spawn) has been heap-forced before we get here, so the check is a
no-op.

## 4. Escape analysis integration

Introduce a third escape state `ir.EscCandidate` alongside `EscNone`
and `EscHeap`. Semantically: "allocated on stack; one or more indirect
calls in this function *might* force materialization at runtime; the
decision is deferred to the call site."

Change `call.go:371`:

```go
if fn == nil {
    return e.escapeCandidateHole(param)
}
```

The new `escapeCandidateHole` records the candidate edge on the
parameter and on any object that flows into this call-site hole.

Allocation emission (`walk/complit.go:356`, `walk/convert.go:286+`
today:

```go
if n.Esc() == ir.EscNone {
    a = initStackTemp(...)
} else {
    a = ir.NewUnaryExpr(..., ir.ONEW, ...)
}
```

Becomes three-way:

| `n.EscState()` | Emission |
|----------------|----------|
| `EscNone`      | `initStackTemp` |
| `EscCandidate` | `initStackTemp` + link into the call-site's materialize list |
| `EscHeap`      | `runtime.newobject` (as today) |

`EscCandidate` is subsumed by `EscHeap`: if *any* non-candidate edge
forces heap (global assignment, chan send, goroutine start, closure
that escapes outwards), the value stays on `EscHeap`. Only objects
whose *sole* outward escape path is through an indirect call become
`EscCandidate`. That keeps the "callee mutates via heap, caller uses
stale stack" hazard out of scope: concurrent readers of the same
object must have reached it through a non-candidate edge, which would
have bumped it to `EscHeap` upstream.

## 5. Correctness

The invariant: after `materializeToHeap` runs, the callee operates on
a distinct heap copy. The caller retains its stack copy. They diverge
iff both are reachable afterwards. Mechanically there are two ways
that could happen:

1. **Caller uses its stack copy after the call.** The callee's heap
   mutations don't propagate. But the caller's escape analysis already
   classifies "callee mutates and caller reads the mutation" as the
   param escaping — which sets the bit, which heap-allocates up front
   — so the materialize path is never entered. Safe.
2. **Another observer holds the stack copy while the call runs.** This
   requires the object to reach a goroutine / channel / global before
   the indirect call. Those transfers are escape edges in the existing
   analyzer, and they force `EscHeap`, not `EscCandidate`. Safe by
   construction.

The runtime helper is `go:nosplit` because call-site insertion
happens in argument evaluation: an inopportune stack grow would
invalidate the stack pointers we're about to memmove from.

## 6. Phased implementation

Each phase lands independently; only Phase D changes program
behavior.

### Phase A — Compute and emit masks (no behavior change)

Goal: the mask is computed and carried, but nothing consumes it.

1. Add `ir.Func.EscMask uint64`, populated at the end of
   `escape.Batch.finish` from each parameter's `leaks`.
2. Extend `ClosureType` (`typecheck/func.go`) with the second
   `uintptr`-typed field. `walk/closure.go:94–144` threads the mask
   into the composite literal.
3. Grow `ITab.Fun` to `[1]ITabMethod` in `internal/abi/iface.go`.
   Update `runtime.itabInit` (`iface.go:204`), reflect's itab reader
   (`reflect/type.go`), and every reader of `Fun[k]` (grep
   `\.Fun\[`). Concretely: expand-calls itab ABI
   (`ssa/expand_calls.go:610`), `getClosureAndRcvr`
   (`ssagen/ssa.go:5210`), EqInter
   (`ssa/_gen/generic.rules:766`), linker itab emission.
4. Grow `persistentalloc` call in `runtime.getitab` to reflect the
   new size math. Document the 16-bit alignment.
5. Compile and run the standard test suite — behaviour unchanged
   since nothing reads `EscMask` yet.

### Phase B — Runtime helper

1. Add `runtime.materializeToHeap(src, typ) unsafe.Pointer`.
2. Unit test: alloc size matches `typ.Size_`, bytes preserved via
   `typedmemmove`, GC traces new heap object under `GOGC=1`.
3. Decide ptrmask behaviour for `typ`s with internal pointers: the
   helper trusts `typ` to have the right `gcdata`; the source stack
   slot also has `gcdata` via the caller's frame descriptor.

### Phase C — Call-site insertion (inert)

1. At each `OCALLFUNC` / `OCALLINTER` lowering, emit the mask-load +
   per-arg conditional materialize. Gate emission on a new `ir` flag
   `Arg.EscCandidate`, which at this phase is never set — so the
   code is dead but present.
2. Keep the stack-backing of escape-candidate args alive past the
   call via an SSA use-chain edge (`OpVarUse` on the stack temp just
   after the call).

### Phase D — Escape-candidate propagation (behaviour change)

1. Flip `call.go:371` to return `escapeCandidateHole`.
2. Extend `walk/complit.go`, `walk/convert.go`, `walk/builtin.go` to
   recognize `EscCandidate` and emit the 3-way allocation pattern.
3. Set the `Arg.EscCandidate` flag on arguments reaching an indirect
   call whose parent object is `EscCandidate`.

### Phase E — Trivial wrappers (static half)

A forwarding closure like `func(x *T) { inner(x) }` with `inner` a
known function currently gets an all-ones mask because `x` "flows
into an indirect call." But the indirect call resolves to a known
target during escape analysis; the mask should transitively inherit
`inner.EscMask`. Extend `tagHole` at `call.go:368` to peek through
the call's function-value expression:

- `ir.ONAME` resolving to a package-level func → substitute that
  func's mask, recompute.
- Local variable bound to a single-assignment closure literal →
  substitute the literal's mask.
- Anything else (captured param, struct field load, reflect) →
  treat as genuinely dynamic, keep the runtime-mask path.

Catches the bulk of trivial wrappers at compile time, zero runtime
cost. Landable after Phase D; strictly reduces the number of
`EscCandidate`-tagged args, so only improves things.

### Phase F — Dynamic mask functions (future, design pinned)

Some wrappers can't be resolved statically:

```go
func wrap(f func(*T)) func(*T) {
    return func(x *T) { f(x) }   // f is captured, not known until call
}
```

Here the wrapper's effective mask *is* `f`'s mask, computable only
at the wrapper's call time. Encoding: repurpose bit 0 of the mask
word as a discriminator. Fn pointers are ≥ 4-byte aligned on every
Go target, so bit 0 of a real fn pointer is always 0.

```
mask word:
  bit 0 = 0: bits 1..63 are the static mask (left-shifted by 1)
  bit 0 = 1: word & ~1 is a pointer to a mask-computing fn
```

Call-site reader:

```go
mask := *maskSlot
if mask & 1 != 0 {
    mask = (*func(unsafe.Pointer) uint64)(unsafe.Pointer(mask & ^1))(recv)
}
// per-arg bit test as usual
```

The mask-computing fn is compiler-synthesized. For a pure forwarder
it's `func(c) uint64 { return c.f.EscMask }` (one load). For a
branching wrapper (`if cond { a(x) } else { b(x) }`) it's the union:
`a.EscMask | b.EscMask`.

Cost of dynamic masks: one test + (rare) one call + tiny fn body.
On every closure that isn't a forwarder, bit 0 stays clear and the
static-mask fast path is unchanged.

**Pinned now**: the mask slot is `uint64`, not `uint32`, and bit 0
is reserved. Phase A's static encoding must left-shift bits by 1
(or equivalent) so Phase F can drop in without re-plumbing carriers.

Alternative representations we considered and rejected:
- **Two slots** (`static_mask uint64, dynamic_fn uintptr`): +16 B
  per closure, doubles itab method slot to 3 words. Too expensive
  for the non-forwarder common case.
- **Cycle-tolerant recursive mask fns**: a mask fn that queries
  another closure's mask fn. Defer indefinitely; compiler can break
  cycles by conservatively returning all-ones.

### Phase G — Measure and tune (was Phase E pre-wrapper)

Targets (allocation-sensitive hot paths in graphics workloads):

- Closure passed to `slices.SortFunc`, `slices.IndexFunc`, `iter.Pull`,
  and custom iterator methods.
- `io.Reader` / `io.Writer` wrappers (small buffered reader passed by
  iface).
- `fmt.Stringer` on concrete types >16 B (doesn't fit inline iface).
- JSON encoder with struct encoders registered by iface method.

Baseline against pre-Phase-A to isolate the effect of this work from
the SSO/fat-iface wins already measured.

## 6a. Activation blocker: uniform func-value layout

Phase D's infrastructure (analyzer → `candidateLoc`, walk wrap,
runtime helpers, carriers) is all landed. Flipping
`escape/call.go:371` from `heapHole()` to `candidateHole()` is a
one-line toggle. It isn't toggled today because of a single
correctness hole:

`maybeEscapeClosureArg` reads the mask at offset `PtrSize` of its
func-value argument. That's correct when the value is a true
closure struct (captures + emitted by `walkClosure`) or a method
value (`walkMethodValue`). It is **not** correct for captureless
function literals:

```go
var fn = func(x *int) { ... }
```

`walkClosure` short-circuits this at `walk/closure.go:99` and
returns the bare `Nname`. The rodata closure entry the linker
emits for the callee is 8 B (`{F}`), so reading offset `PtrSize`
lands in adjacent rodata — garbage mask.

Two paths to unblock:

1. **Uniform `{F, M}` rodata entries.** Extend the linker to
   always emit 16 B for function-value symbols. Walk continues
   to short-circuit captureless literals to `Nname`, but every
   `Nname`-originated func value now has a valid `M` at offset
   8. Blast radius: the `.f` symbol emission in
   `cmd/link/internal/ld/*` and matching size math in
   `cmd/compile/internal/reflectdata/reflect.go`. No runtime
   changes needed — the indirect-call path already only reads
   offset 0 and our wrap reads offset 8.

2. **Static classification at walk time.** Detect at the call
   site whether the func value is a real closure (has `{F, M}`)
   or a bare function pointer (has only `{F}`), and only emit
   the wrap in the former case. Falls back to the old heap-
   alloc path otherwise, which means captureless-literal
   targets lose the optimization. Static classification requires
   tracking the provenance of each func value — feasible for
   direct calls and single-assignment locals, opaque for map
   lookups, parameters, etc. Smaller blast radius but narrower
   win surface.

Path 1 is the cleaner long-term fix; path 2 is landable without
touching the linker. Either way, with the chosen path in place,
flip `tagHole` and the two `*NonEscape` tests go green.

## 7. Open questions / risks

1. **Mask granularity.** 1 bit per pointer param is cheapest and
   captures "reach heap or not." A 2-bit encoding (`none | mutates |
   escapes`) would let us propagate escape-candidate status
   transitively through chained indirect calls (e.g. a closure that
   invokes an iface method on one of its args). Start at 1 bit;
   revisit if benchmarks show the transitive case matters.

2. **Method-value closures.** `obj.method` creates a bound method
   closure. Its escape profile is the method's profile ∪ any captures.
   Ensure `walk/closure.go` threads the method's `leaks` through to
   the synthesized closure's `EscMask`.

3. **Shape types / generics.** Dictionary-based generic dispatch goes
   through an indirect call on a dictionary-loaded fnPtr. The
   dictionary entries need masks too. Treat as closures for the
   encoding; gate on `typ.HasShape()`.

4. **`reflect.Value.Call`.** Today heap-allocates a `[]Value` for args
   unconditionally. The reflect rtype carries mask info by extension;
   reflect could skip the heap alloc for non-escaping args. Out of
   scope for this landing.

5. **Cgo / `//go:noescape`.** Extern functions without annotation get
   all-ones mask (conservative). Annotated functions get all-zero.
   Linknamed funcs that the fork doesn't own: mask defaults to
   all-ones; packages that want the optimization must annotate.

6. **Linker itab emission.** `moduledata.itablinks` walks compile-
   time itabs at init (`iface.go:259`, `itabsinit`). The new 16-byte
   slot needs `EscMask` filled at link time from the concrete method's
   signature. Requires a linker extension symbol table entry per
   method, similar to how `Ifn` offsets are emitted today.

7. **ABI break.** Growing `ITab.Fun` slots is a link-time ABI change.
   The fork already broke iface ABI for fat-iface, so this is
   additive, not novel. Stale `.a` files in anyone's build cache will
   produce a link-time symbol-size mismatch rather than runtime
   corruption (good failure mode).

8. **Materialize races.** `mallocgc` inside `materializeToHeap` may
   trigger GC. The source stack pointer stays live across the alloc
   (the helper is nosplit, so no preemption), and `typedmemmove`
   handles pointer stores via the write barrier. The pointer we
   return is the only post-call reference the callee sees; no other
   reference can exist yet because the call hasn't started.

## Critical files

- `src/cmd/compile/internal/escape/leaks.go:13–25` — `leaks` encoding
  and helpers. Source of the `Heap()`/`Mutator()`/`Callee()` bit.
- `src/cmd/compile/internal/escape/call.go:368–372` — inflection for
  dynamic callees. Single-line change in Phase D.
- `src/cmd/compile/internal/escape/escape.go:294` —
  `param.Note = b.paramTag(...)`. Emission of the `"esc:<leaks>"`
  export-data tag. Per-param mask derives from here.
- `src/cmd/compile/internal/typecheck/func.go` (`ClosureType`) —
  closure struct synthesis. One-field growth.
- `src/cmd/compile/internal/walk/closure.go:94–144` — closure
  literal codegen. Thread the mask into the composite literal.
- `src/internal/abi/iface.go:14–29` — `ITab` + dispatch mode
  constants. Grow `Fun` slot.
- `src/runtime/iface.go:77, 204–217` — itab allocation
  (`persistentalloc` sizing) and `itabInit` method writer. Write
  mask alongside `Fn`.
- `src/cmd/compile/internal/ssa/expand_calls.go:610` — itab ABI
  decomposition; needs 2-word slot awareness.
- `src/cmd/compile/internal/ssagen/ssa.go:5210` —
  `getClosureAndRcvr`. Reads `Fun[k]`; needs to also read the mask
  when emitting the iface-method call.
- `src/cmd/compile/internal/walk/complit.go:356–360` — first-
  allocation-choice site for composite literals. Three-way emission.
- `src/cmd/compile/internal/walk/convert.go:286, 317, 338, 366, 390`
  — convert-site allocations (boxing into iface, etc.). Three-way
  emission.
- `src/cmd/link/internal/ld/*` — itab emission at link time; needs
  per-method mask slot.
