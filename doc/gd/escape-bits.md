# Dynamic escape bits for closures and interface methods (gd fork)

Goal: eliminate the heap allocations that stock Go forces on pointer
arguments to indirect calls (closure invocations and interface method
dispatch). The gd fork already computes per-parameter escape info per
function; we just need to ship that info across the indirect-call
boundary so the caller can keep args on the stack when the specific
callee doesn't let them escape. README optimization goal #3.

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

### Phase E — Measure and tune

Targets (allocation-sensitive hot paths in graphics workloads):

- Closure passed to `slices.SortFunc`, `slices.IndexFunc`, `iter.Pull`,
  and custom iterator methods.
- `io.Reader` / `io.Writer` wrappers (small buffered reader passed by
  iface).
- `fmt.Stringer` on concrete types >16 B (doesn't fit inline iface).
- JSON encoder with struct encoders registered by iface method.

Baseline against pre-Phase-A to isolate the effect of this work from
the SSO/fat-iface wins already measured.

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
