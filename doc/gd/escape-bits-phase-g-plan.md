# Escape-bits Phase G — return-value stack storage via `outBuf`

Runtime plumbing landed as `runtime.maybeInPlace(outBuf, typ)` (commit
`9120642bae`, hand-demo at `-85%` time / `-100%` allocs). This doc
pins the **compiler** piece so we can implement it confidently.

Design choice (locked in): the compiler performs a **universal IR-
level signature rewrite at typecheck**. Every pointer-returning
function / method / interface-method / function-type gets `outBufK *T`
parameters appended, one per pointer-typed result. Applied uniformly
so interface satisfaction works by construction — no thunks, no ABI
hidden-register trickery. Downstream passes (escape, SSA, regabi,
walk) see a normal signature with one more arg.

## Mental model

Phase D (arg side):

```
callee: func f(p *T)           caller: x := &local; f(x)
                               candidate x flows through dynamic-callee
                               arg; runtime maybeEscape*Arg decides
                               heap vs keep-stack based on the callee's
                               escape-mask bit.
```

Phase G (return side) is exactly the mirror:

```
callee: func f(outBuf *T) *T   caller: p := f(&buf)         // buf on stack when escape says safe
                               or      p := f(nil)          // heap when escape says escape
                               runtime.maybeInPlace(outBuf, typ)
                               picks storage at the allocation site.
```

Symmetry means: no new analyzer concepts, no new runtime helpers.

---

## Implementation phases

### Phase G.1 — escape analysis marks *return candidates*

Today's `escape/escape.go:finish` only propagates `EscCandidate` to
PAUTO ONAMEs (safety belt from the fmt TestScanInts fix). Return-
value work needs to additionally propagate it to alloc nodes (ONEW,
OPTRLIT, OSTRUCTLIT) whose only escape edge is a return statement.

**Changes:**

1. Extend `graph.go`'s `leakTo` — when `sink` is a `PPARAMOUT` loc
   and `l.curfn == sink.curfn`, set a new `attrReturnCandidate` bit
   on `l` alongside the existing `paramEsc.AddResult` tagging.
2. In `finish`: for any `n` whose loc has `attrReturnCandidate` AND
   NOT `attrEscapes` AND NOT routed elsewhere, set
   `n.SetEscCandidate(true)`. Works for ONEW, OPTRLIT, named-result
   assignments — anything that today becomes `EscHeap` solely
   because the value exits through a return slot.
3. Relax `isCandidateLoc` for this path: the ONAME-only restriction
   applies to the arg-side wrap (where fresh allocs into a closure
   arg are risky). Returns have a different lifetime shape; fresh-
   alloc candidates here are safe because the caller's outBuf (or
   heap fallback) bounds them.
4. Record per-result-index candidacy on `ir.Func`. A function
   returning `(*T, *U, error)` may have the first result candidate
   but not the second. Attach `ResultCandidates uint8` (bitmap of
   result indices) so G.3 can look up "which outBufK applies to this
   allocation".

**Observable effect alone:** none. `attrReturnCandidate` is set but
nothing consumes it yet; `EscCandidate` stays attached but the
walk-level rewrite hasn't been added.

### Phase G.2 — universal signature rewrite at typecheck

**Where:** typecheck, before escape analysis runs. A new
`typecheck/return_outbuf.go` (or a hook inside existing signature
construction paths) that processes *every* `*types.Type` representing
a callable:

- function declarations (`ir.Func.Nname.Type()`),
- method receivers' signatures,
- interface method signatures in `types.Interface`,
- function-value types (`func(...) *T` as expression type).

**Transform:**

```
func f(a, b int) (*T, *U, error)
          ↓
func f(a, b int, outBuf0 *T, outBuf1 *U) (*T, *U, error)

interface { Foo() *T; Bar(int) string }
          ↓
interface { Foo(outBuf0 *T) *T; Bar(int) string }

var fn func() *T
          ↓
var fn func(outBuf0 *T) *T   // at the type layer; user source unchanged
```

Only pointer-typed results trigger param synthesis (slice, map, chan,
iface, func results are skipped in this phase; revisit later). Results
whose pointee is `internal/abi.Type` or similar reflect-layer types
may need opt-out — revisit when tests surface issues.

**Implementation sketch:**

```go
// appendReturnOutBufs walks t's result list and returns (t', changed).
// t' has an outBufK *E param for every *E result in t.
func appendReturnOutBufs(t *types.Type) (*types.Type, bool) {
    if !t.IsFuncType() { return t, false }
    results := t.Results()
    params  := t.Params()
    var extra []*types.Field
    for k := 0; k < results.NumFields(); k++ {
        r := results.Field(k)
        if !r.Type.IsPtr() { continue }
        nameStr := fmt.Sprintf(".outBuf%d", k)
        f := types.NewField(r.Pos, typecheck.Lookup(nameStr), r.Type)
        extra = append(extra, f)
    }
    if len(extra) == 0 { return t, false }
    newParams := append(params.FieldSlice(), extra...)
    return types.NewSignature(t.Recv(), newParams, results.FieldSlice()), true
}
```

Apply at:
- `typecheck.tcFunc` — rewrite the function's own type.
- `typecheck.typecheckinter` (or wherever interface types finalize) —
  rewrite each method's signature inside the interface.
- Function-literal typecheck (OCLOSURE) — rewrite the closure's type.
- Every place that constructs a `*types.Type` from a
  `*syntax.FuncType`.

Iface satisfaction is naturally preserved: both sides go through the
same rewrite so method sets align.

**Caveats:**

- **`reflect`:** `reflect.Type.In()` would count outBufs as visible
  params. Add a check in `src/reflect/type.go`'s `(*rtype).In` and
  `NumIn` that strips trailing outBuf params (identified by name
  prefix `.outBuf`). `reflect.Value.Call` auto-passes `nil` for
  each stripped param. One helper, ~30 LoC.
- **Runtime ABI-discovery helpers** (`runtime.FuncForPC`, etc.) are
  per-PC metadata and unaffected.
- **CGo / syscall trampolines:** these go through
  `//go:linkname` and concrete type signatures; no iface dispatch.
  If any declares a pointer return, it gets outBufs. Typically
  safe; verify per case during rollout.
- **Reflected method calls via `reflect.Method.Func.Call`:** method
  Value's type is the receiver-plus-rewritten signature; reflect
  already constructs these via the rtype hooks, so the strip-helper
  applied to `Value.Call` covers them.

**Observable effect alone:** every pointer-returning function gains
an arg slot. Callers not yet rewritten pass nothing; SSA/regabi
default-zeros the reg, so `outBuf == nil` — callee's existing
`new(T)` path runs. Behavior unchanged.

### Phase G.3 — callee body rewrite

At each allocation site whose node has `EscCandidate` AND whose
escape path was attributed to result `k`, substitute the alloc with
`runtime.maybeInPlace(outBufK, typ)`.

Gated on per-allocation candidacy — we don't rewrite every `new(T)`
in every pointer-returning function, only the ones the analyzer
said were return-candidate. Non-candidate allocations continue to
heap-alloc regardless of outBuf.

**ONEW (`new(T)`):**
```
p := new(T)              →  p := (*T)(runtime.maybeInPlace(
                                unsafe.Pointer(outBufK),
                                typeOfT))
```

**OPTRLIT (`&T{…}`):**
```
p := &T{a: 1, b: x}      →  p := (*T)(runtime.maybeInPlace(
                                unsafe.Pointer(outBufK),
                                typeOfT))
                            p.a = 1
                            p.b = x
```

Composite-literal decomposition already exists in
`walk/complit.go:anylit`. We extend it: when the OPTRLIT node carries
`EscCandidate` AND its return-index is recorded, the emitted
address-of substitutes `maybeInPlace(outBufK, typ)` for the
`stackTempAddr` / `ONEW` fall-through.

**Walk placement:** `walkNew` at `walk/builtin.go:616` already has an
`EscCandidate` check. Extend the branching:

```go
if n.EscCandidate() {
    if outBufK, ok := returnOutBufFor(n); ok {
        return callMaybeInPlace(outBufK, t, init)
    }
    // existing stackTempAddr path (arg-side candidate)
    addr := stackTempAddr(init, t)
    addr.SetEscCandidate(true)
    return addr
}
```

`returnOutBufFor(n)` looks up the return-index of `n`'s allocation
in a map populated by G.1 (escape finalize tagging each allocation
with its target result index) and G.2 (post-rewrite signature knows
which param corresponds to which result slot).

**Observable effect alone:** callee bodies now conditionally use the
outBuf. All existing callers pass nil (nothing auto-fills it yet), so
`maybeInPlace` always falls through to `mallocgc`. Behavior unchanged.

### Phase G.4 — caller call-site rewrite

At every OCALLFUNC, consult the **caller's** escape analysis for
each pointer result received:

- Result `r_k` does not escape caller: reserve a stack buf, pass
  `&buf_k` as outBufK.
- Result `r_k` escapes caller: pass `nil` (explicit, so callee's
  `maybeInPlace` takes the heap path).

**Where:** walk, companion to `wrapEscapeCandidateArgs` in
`walk/escape_bits.go`. Run before the arg wrap so outBuf args look
like normal call args from the arg-wrap's perspective.

**Transform:**

```
p, q, err := f(x, y)                →  var buf0 T              // skipped if r0 escapes
                                       var buf1 U              // skipped if r1 escapes
                                       p, q, err := f(x, y, outBufFor(r0),
                                                              outBufFor(r1))
                                       // outBufFor(rK) is &bufK or nil(*T)
```

Pointer results aren't necessarily in the last positions, so we map
the K-th pointer result to `outBufK` by the order G.2 appended them
(i.e. walk results in order, skip non-pointers, number remaining
0..K).

**Observable effect alone:** the payoff turns on. For any call where
the result doesn't escape the caller's frame and the callee was
rewritten by G.3 to consult outBuf, zero allocation.

### Phase G.5 — reflect API surface

Strip synthesized `.outBufK` params from reflect's view:

- `reflect/type.go`: helper `isOutBufParam(f *rtype) bool` matches by
  name prefix. `In(i int)` and `NumIn()` skip these. `Type.In` etc.
  return the pre-rewrite view.
- `reflect/value.go`: `Value.Call` / `CallSlice` auto-appends nil
  outBufs to match the underlying ABI.

Keep the rewritten type shape internal — reflect pretends the stock
signature to user code. Users who intentionally poke at function
internals via `unsafe` still see the real ABI; that's the fork's
standing posture.

### Phase G.6 — dynamic callees (closures, iface methods)

Nothing extra needed here. Because G.2 is universal, closures and
iface methods already carry the outBuf params in their type. Walk's
existing OCALLFUNC / OCALLINTER codegen handles the call the same
way it would for any other arg. The caller's escape analysis decides
`&buf` vs `nil` per result slot without needing a runtime mask.

The one optional enhancement: for *indirect* callees where the
caller can't tell whether the target will actually use outBuf, we
could add a bit to the callee's escape mask analogous to the arg
bits so the caller avoids reserving a stack buf when unused. Defer
until measurements show it matters; passing nil is cheap.

### Phase G.7 — edge cases

| Case | Handling |
|------|----------|
| `return nil` | callee emits `nil` unchanged; outBuf unused. Correct. |
| Conditional allocs on multiple paths | every alloc site tagged `EscCandidate` gets substituted independently; joins at the return aggregate the outBuf-or-heap pointer. |
| Named results: `func f() (p *T) { p = new(T); return }` | the `p = new(T)` assignment follows the same pattern. |
| Pointer result stored elsewhere before return | escape analyzer sees the extra sink and doesn't set `attrReturnCandidate` → no rewrite. Safe. |
| Recursion | callee calls itself; the rewritten signature requires outBuf, caller's escape analyzer decides. No special case. |
| Result flows into closure capture | escape analyzer marks as heap; `attrReturnCandidate` not granted. |
| Non-pointer pointer-like results (slice, map, chan, iface, func) | skip in initial implementation. Each has its own storage shape; generalisation is future work. |
| `*T` where T has pointer fields | `maybeInPlace` already calls `typedmemclr` for pointer-containing types, GC-safe. |
| Large T | cap outBuf path at `ir.MaxImplicitStackVarSize` (65535 B); caller passes nil above that. |
| `//go:linkname` / assembly declarations | `//go:linkname` stubs map a Go-level signature onto an asm entry point. If the Go signature declares `*T` return, G.2 appends outBuf. The asm entry won't know to use it, but since caller passes nil (or a rewritten asm stub), the asm side stays identical. Audit: `runtime.linkname` usages with pointer returns. |
| Runtime-internal functions | skip G.2 for `internal/runtime/*` and `runtime` package itself to keep runtime's ABI stable. Gate: `!base.Flag.CompilingRuntime`. Matches existing Phase D posture. |

### Phase G.8 — tests and benchmarks

Tests (mirroring Phase D's structure):

1. `TestReturnOutBuf_SimpleReturn` — `func f() *T { return new(T) }` + caller `p := f()` local-scope. 0 allocs.
2. `TestReturnOutBuf_EscapingResult` — caller stores `p` in a global; 1 alloc (outBuf=nil path).
3. `TestReturnOutBuf_MultiResult` — `(*T, *U, error)` with mixed escape outcomes; verify per-result bit.
4. `TestReturnOutBuf_ConditionalAlloc` — both branches allocate; both get substituted.
5. `TestReturnOutBuf_IfacePath` — iface dispatch goes through rewritten method; same zero-alloc when caller proves locality.
6. `TestReturnOutBuf_ZeroedOnReuse` — caller loop reuses the stack buf; each call sees zeroed T.
7. `TestReturnOutBuf_PointerFieldsSurvive` — returned `*T` with pointer fields; forced GC verifies pointees stay live.
8. `TestReturnOutBuf_ReflectStripsOutBufs` — `reflect.Type.In()` on a rewritten function sees the user-facing signature.
9. `TestReturnOutBuf_ReflectCallAutofills` — `Value.Call` on a rewritten function with user-level args succeeds (reflect fills nil outBufs).

Benchmarks:

- Port the hand-written `escBitsMakeOutBuf` / `escBitsMakeStock` demo to compiler-driven tests. Expect same -85% / -100% deltas.
- Factory-heavy benchmark from `encoding/json` or `net/http` (lots of `New*` constructors).

### Phase G.9 — rollout

No activation gate beyond analyzer-granted candidacy. Land order:

1. **G.2** (universal signature rewrite) first — observable: every pointer-returning function gains a param slot, all callers pass nil, callees ignore it. Behaviour unchanged; compile/link/run all stdlib to shake out type-system issues. Skip runtime/asm boundaries.
2. **G.5** (reflect strip) alongside G.2 — without it, `reflect.Type.In` and `Value.Call` break immediately.
3. **G.1** (escape analyzer marks return candidates) — observable: `EscCandidate` tags on ONEW/OPTRLIT nodes. Still no behaviour change.
4. **G.3** (callee body rewrite) — observable: `maybeInPlace` invoked in the callee. Still all-heap because callers pass nil.
5. **G.4** (caller call-site rewrite) — observable: zero allocations on proven-local calls. The payoff.
6. **G.8** tests + benchmarks throughout.

If G.2+G.5 proves too invasive standalone (reflect tests, iface-type-assertion surprises), revisit the phasing — but nothing about the design requires it be shipped separately.

---

## Files that will change

- `src/cmd/compile/internal/escape/graph.go` — `attrReturnCandidate` bit, `leakTo` propagation.
- `src/cmd/compile/internal/escape/escape.go` — `finish` relaxation; record per-result candidacy.
- `src/cmd/compile/internal/ir/func.go` — `ResultCandidates uint8` field.
- `src/cmd/compile/internal/typecheck/return_outbuf.go` — new file; universal signature rewrite.
- `src/cmd/compile/internal/typecheck/typecheck.go` / `func.go` / `iface.go` — call sites for the rewrite.
- `src/cmd/compile/internal/walk/return_outbuf.go` — new file; callee body rewrite (G.3) and call-site outBuf filling (G.4).
- `src/cmd/compile/internal/walk/builtin.go:walkNew` — route `EscCandidate` ONEW to `maybeInPlace` when return-indexed.
- `src/cmd/compile/internal/walk/complit.go:anylit` — same for OPTRLIT / OSTRUCTLIT.
- `src/cmd/compile/internal/walk/expr.go` — OCALLFUNC dispatch to outBuf-filling before arg-wrap.
- `src/reflect/type.go` — strip `.outBufK` from `In` / `NumIn`.
- `src/reflect/value.go` — auto-fill nil outBufs in `Call` / `CallSlice`.
- `src/cmd/compile/internal/test/escape_bits_outbuf_test.go` — convert hand demos to compiler-driven.

Existing helpers to reuse:

- `runtime.maybeInPlace` (landed).
- Need to add `ir.Syms.MaybeInPlace` if not present (check `cmd/compile/internal/typecheck/_builtin/runtime.go`).
- `reflectdata.TypePtrAt` for the `*abi.Type` arg.
- `typecheck.ConvNop` for the unsafe.Pointer conversions.
- `stackTempAddr` pattern for reserving the caller's stack buf.

## Non-goals

- Stock-Go ABI compatibility. Fork-only changes; itab layout, signature layout, regabi reg allocation all fork-specific.
- Auto-synthesis of compute fns for dynamic callees (Phase F item).
- Per-field return escape (analogous to the deferred per-field arg tracking).
- Non-pointer results (slice, map, iface, struct-return). Different shape; future phase.
- Runtime-package functions. Runtime keeps stock ABI to avoid destabilising asm routines. Gate via `!base.Flag.CompilingRuntime`.
