# Small-string optimization plan (gd fork)

Goal: expand Go's `string` header from 16 B to 24 B, inline up to 15 B of
character data directly in the header. Upper 4 bits of **word 2** are a
length-tag: `0` = heap string, `1..15` = inline string of that many
bytes. For heap-rep strings the otherwise-unused **word 1** is reserved
as a cached string hash slot (Phase A leaves it zero; a later phase
populates it lazily for map integration). Second optimization in the
gd fork; ABI break is expected and acceptable. Phase 2 integrates with
fat-iface so `string`-valued interfaces never box.

## 0. Premise check

GC stays clean because **word 0 is always either a live heap pointer
or `nil`** — never arbitrary bytes. Inline-rep strings explicitly zero
word 0; all 15 inline bytes live in words 1 and 2. The ptrmask for
TSTRING keeps its single pointer bit on word 0 (unchanged from stock);
`scanobject` sees a valid pointer or nil, same as today. No
false-retention, no conservative-interpretation quirk.

This is the same discipline that made fat-iface's GC story work: the
ptrmask-pointer slot is never allowed to hold non-pointer bits.
Strings pay for it with 8 bytes of inline cap lost (word 0 unused) but
inline cap is still 15 bytes because words 1 and 2 (minus the 4-bit tag)
cover 15.5 bytes of payload.

Files that assume `sizeof(string) == 16` (pre-audit, not exhaustive):
- `src/runtime/string.go:290` (`stringStruct`), `:296` (`stringStructDWARF`).
- `src/internal/runtime/maps/runtime_faststr.go:93` — shadow `stringStruct`.
- `src/reflect/value.go:2800` — deprecated `StringHeader { Data uintptr; Len int }`.
- `src/cmd/compile/internal/ir/expr.go:642` — `StringHeaderExpr`.
- `src/cmd/compile/internal/types/universe.go:54` — `StringSize`.
- `src/cmd/compile/internal/types/size.go:259,392` — TSTRING size, `intRegs = 2`.
- `src/cmd/compile/internal/typebits/typebits.go:42` — TSTRING ptrmask.
- `src/cmd/compile/internal/ssa/_gen/genericOps.go:334,530–532` —
  `ConstString`, `StringMake`, `StringPtr`, `StringLen`.
- `src/cmd/compile/internal/ssagen/ssa.go:4574` (TSTRING const lowering),
  `:2043,3815` (OSPTR).
- `src/cmd/compile/internal/reflectdata/*`, `src/cmd/compile/internal/walk/*`
  (string construction / concat / compare paths).
- Runtime asm (`asm_*.s`) — any frame size that mentions a string arg.

## 1. Layout (24 B on 64-bit; 8-byte aligned)

```
offset  0: word0 uint64   // heap: data ptr; inline: nil (always zero)
offset  8: word1 uint64   // heap: cached string hash (0 = uncomputed)
                          // inline: bytes[0:8]
offset 16: word2 uint64   // tag<<60 | payload
                          //   tag == 0 : heap; payload = len (low 60 bits)
                          //   tag != 0 : inline; payload = bytes[8:15]
                          //              packed in low 56 bits (4 bits pad)
```

Word 2 layout in bits:
- bits  0..55: heap: low 56 of len. inline: bytes[8:15] packed little-endian.
- bits 56..59: heap: high 4 of len. inline: 4 bits padding (zero).
- bits 60..63: tag. 0 = heap; 1..15 = inline length.

Total inline cap: word1 (8 B) + word2 low 7 B = **15 bytes**, contiguous
in memory at offsets 8..22 on little-endian architectures. The tag+pad
byte lives at offset 23 (high byte of word2), outside the byte range.
`&s.word1` is therefore a valid pointer to the first inline byte, and
the 15 bytes can be read as a contiguous span.

Invariants:
- `tag := word2 >> 60`
- `isInline(s) = word0 == 0 && tag != 0`
- `len(s)` — cmov form:
  ```
  w2  := word2
  tag := w2 >> 60
  if tag != 0 { return int(tag) }
  return int(w2 & lenMask)          // tag==0 ⇒ w2 low 60 = heap len
  ```
  Lowers to load + shift + cmov. One word2 load; stock loads word1
  instead. Same load count, extra shift + cmov.
- `ptr(s) = word0 != 0 ? unsafe.Pointer(word0) : unsafe.Pointer(&s.word1)`
  — one nil-compare, cmov-able. For heap-rep empty string (`{0,0,0}`),
  both branches return harmless addresses (no valid `s[0]` anyway).
- `s[i]` / slice / range — contiguous byte access at `ptr(s) + i`,
  no tag-byte skip.
- Empty string: `{0, 0, 0}` (heap rep, nil ptr, len 0, hash uncomputed).
  Inline strings of length 0 are not representable by construction — any
  `rawstring(0)` / `""` stays heap rep with nil ptr, matching stock.
- 32-bit: use `uint64` (not `uintptr`) for all three words so the 15-byte
  inline cap is preserved; `StringSize = 24` everywhere, both arches.

Why nil in word 0: §0 — preserves stock GC ptrmask semantics. Word 0 is
always a valid pointer-or-nil, never arbitrary bytes. Costs 8 B of
potential inline cap but 15 B still fits easily in words 1 and 2.

Why the tag in word 2 (not word 1): frees word 1 in the heap rep for
use as a **cached string hash slot** (see §1a). Word 1 is only unused
for heap strings in the tag-in-word1 variant, so moving the tag to
word 2 lets us repurpose word 1 for hash caching without losing any
inline cap (we'd have had to split bytes 7+8 across word1/word2 in the
tag-in-word1 variant, making inline bytes non-contiguous).

Heap `len` is capped at 2^60. `maxAlloc` is already well below that, so
this is not a practical restriction.

### 1a. Cached string hash (heap rep only)

For heap-rep strings, word 1 caches a 64-bit hash of the string bytes
under a **fixed global seed**. The gd fork drops stock Go's per-map
hash seed randomization (no DoS protection — fork is targeted at
local/graphics workloads where map keys are trusted), so every caller
that hashes a string — maps, `maphash.Hash`, set dedup, etc. — uses
the same global seed. One hash value per string; reusable everywhere.

Sentinel `0` means "not computed yet". Probability that a string's
actual hash is exactly 0 is 2^−64, effectively zero; such strings
simply re-compute on each access. No separate "computed" bit needed.

Inline-rep strings have no hash slot (word 1 holds bytes). Hashing an
inline string computes over its ≤15 bytes each time — cheap enough
that caching isn't worth the layout complexity.

**Seed source.** A fixed compile-time-known constant (e.g. a random
value baked into the toolchain at fork-release time), exposed as
`runtime.stringHashSeed`. Deterministic across runs, which lets the
compiler pre-compute literal hashes at compile time and emit them
directly into rodata string headers.

**Compiler integration.** String literals (`ir.OLITERAL` of TSTRING)
emit headers with word 1 = `hash(literal_bytes, stringHashSeed)`.
Dynamic heap strings start with word 1 = 0 and lazy-fill on first
hash access.

**Runtime integration.** `runtime.strhash(s)` reads word 1; if zero,
compute, cache, return. Map, set, and `maphash.Hash` fast paths read
the cached hash directly.

Phase A leaves word 1 zero for all heap strings; no one reads it yet.
Phase H lights up literal pre-hashing, lazy cache, and map integration.

Eligibility for inline rep: decided at compile time for string literals,
at runtime for dynamic strings. Any string of length 1..15 bytes is a
candidate; length 0 and ≥ 16 are always heap rep.

## 2. Eligibility / construction sites

Every site that builds a string must decide inline vs heap:

- **String literals** (`ir.OLITERAL` of TSTRING) — known at compile time.
  Inline-rep literals emit as three `ConstInt64` words; heap-rep as today
  (rodata pointer + len).
- **`string([]byte)` / `string([]rune)`** — `runtime.slicebytetostring`
  (`runtime/string.go`), `slicerunetostring`. Branch on result len.
- **`strconv.Itoa` and friends** — mostly build into a `[]byte` then
  convert. Inherits from slicebytetostring.
- **`rawstring`** (`runtime/string.go`) — returns `(s string, b []byte)`
  where caller fills `b`. Split into `rawstringHeap` (today's code) and
  `rawstringInline` (returns `s` with inline slot addressable via
  `*stringStruct`). The `b` slice must point to the inline slot — but
  that slot's lifetime == the stack frame of the caller. Any caller that
  lets `b` escape must force heap rep. Mark `rawstringInline` as
  `//go:nosplit` and have callers that may escape use the heap path.
- **Concatenation** (`concatstrings`, `runtime/string.go`) — branch on
  total length. Concatenations `≤ 15` return inline.
- **`strings.Builder.String`** — calls `unsafe.String` on its internal
  buffer, so result is heap-rep (buffer is heap-allocated). No change.
- **`unsafe.String(ptr, len)`** — always heap rep. Even if `len ≤ 15`,
  the user-supplied pointer is the source of truth; we cannot copy bytes
  into an inline slot without changing lifetime semantics. Document.
- **`unsafe.StringData(s)`** — must return `&s.word1` for inline rep,
  `word0` for heap rep. This returns a pointer into the string header
  itself for inline — only valid while `s` is live. Stock semantics
  already warn this is unsafe.

## 3. GC scanner

No ptrmask change. `typebits.set` TSTRING case at
`src/cmd/compile/internal/typebits/typebits.go:42` already marks only
word 0 as pointer; the new words 1 and 2 are non-pointer by default.
`types.PtrDataSize(TSTRING)` stays at `PtrSize` (offset 0 + 8), so the
trailing 16 B of inline bytes are outside the ptrmask suffix — matches
`ptrBytes = PtrSize` at `size.go:400` unchanged.

Because word 0 is always a live heap pointer or nil (§0, §1), `scanobject`
sees exactly what it sees today: a pointer it can trace or skip. No
false retention, no special case. The only requirement is that every
string-construction site that builds an inline rep must zero word 0
(and the compiler's `StringInlineMake` lowering must emit a zero, not
leave word 0 undefined).

## 4. Runtime

All changes behind `//go:build gd`.

| Site | Change |
|---|---|
| `runtime/string.go:290` `stringStruct` | Add `extra uint64`. Change `len int` to carry tag bits; introduce `func (s *stringStruct) length() int { return int(s.extra & lenMask) }` (where `lenMask = 1<<60 - 1`) and `func (s *stringStruct) inline() bool { return s.extra>>60 != 0 }`. Keep `str unsafe.Pointer; len int` names for the heap case but treat `len` as word 1 (unused for heap — always 0). |
| `runtime/string.go:296` `stringStructDWARF` | Mirror. Used by DWARF emission — keep `str *byte; len int` layout but extend. |
| `internal/runtime/maps/runtime_faststr.go:93` | Shadow `stringStruct` — grow in lockstep; use a `//go:build gd` variant. |
| `runtime/string.go` `rawstring` | Split into `rawstringHeap` (today) and `rawstringInline`. Callers that escape `b` must use Heap. |
| `runtime/string.go` `concatstrings` | Total length ≤ 15 → build inline; otherwise heap as today. |
| `runtime/string.go` `slicebytetostring`, `slicerunetostring` | Same branch. |
| `runtime/string.go` `stringtoslicebyte`, `stringtoslicerune` | Read via `ptr(s)` helper (conditional on `inline()`). |
| `runtime/alg.go` `strhash`, `strequal` | Hash/compare reads `ptr(s)` + `length()`. For inline-vs-inline with equal length, memcmp directly on the two headers' inline bytes. |

Runtime sanity: add `unsafe.Sizeof("") == 24` assertion in `runtime2.go`
next to the existing iface size assertion (from fat-iface phase).

## 5. Compiler

### 5.1 Size and ABI

- `types/universe.go:54`: `StringSize = RoundUp(3*PtrSize, PtrSize)` under
  gd. Or simpler: unconditionally `3*PtrSize`.
- `types/size.go:392` TSTRING case: `intRegs = 3` (was 2). `align`,
  `ptrBytes = PtrSize` unchanged. `alg` stays `ASTRING`.
- `cmd/compile/internal/abi/abiutils.go` — `synthString`-equivalent: if
  a synthetic decomposition exists for TSTRING (mirroring `synthIface`),
  grow to 3 fields. If none, `ABIAnalyze` handles via size.

### 5.2 SSA ops — `cmd/compile/internal/ssa/_gen/genericOps.go`

Current:
```
{name: "ConstString", aux: "String"}
{name: "StringMake", argLength: 2}   // (ptr, len)
{name: "StringPtr",  argLength: 1}
{name: "StringLen",  argLength: 1}
```

Proposed:
```
// Existing ops keep semantics — 3 arguments / 3-word result under gd.
{name: "StringMake",   argLength: 3}                 // (word0, word1, word2)
{name: "StringPtr",    argLength: 1, typ: "BytePtr"} // compiles to conditional
{name: "StringLen",    argLength: 1, typ: "Int"}     // compiles to tag?tag:word2 cmov
{name: "StringInline", argLength: 1, typ: "Bool"}    // word0 == 0 && len > 0

// Helpers for cheap inline-literal construction:
{name: "StringInlineMake", argLength: 2}             // explicit inline rep
                                                     // (word1=bytes[0:8], word2=tag<<60|bytes[8:15]packed)
                                                     // word0 implicit zero
```

`StringMake` arity change ripples through `rewrite.go` and every
architecture-specific `_gen/*.rules` file that mentions it.
`expand_calls.go` must decompose TSTRING args into 3 slots (parallel to
the TINTER 4-slot change in fat-iface).

### 5.3 `len(s)` / `s[i]` / `&s[0]` lowering — `ssagen/ssa.go`

- `len(s)` — emit `tag := word2 >> 60; return tag != 0 ? int(tag) : int(word2 & lenMask)`.
  Lowers to load + shift + cmov on amd64, csel on arm64: one word2
  load, one shift, one and, one compare, one conditional move.
  Stock cost was one word1 load. Net delta: one shift + cmov per
  `len(s)` call; measure the impact on tight loops.
- `s[i]` / slice — inline bytes are contiguous at offsets 8..22, so a
  single nil-compare on word0 suffices:
  ```
  base := word0 != 0 ? word0 : AddrOf(s.word1)
  return *(base + i)
  ```
  `AddrOf(s.word1)` requires `s` to be addressable. For register-resident
  strings, spill to stack first. Cmov/csel lowering; measure.
- **Escape of inline pointer.** `&s[0]` or any expression that takes
  address into inline storage may not outlive `s`. Escape analysis must
  treat `StringPtr(s)` as causing `s` to escape when the result escapes.
  Parallels fat-iface 5.3 `getClosureAndRcvr`.

### 5.4 String literal lowering — `ssagen/ssa.go:4574`

TSTRING const: if `len(lit) ≤ 15`, emit `StringInlineMake(word1, word2)`
with the two pre-computed uint64 words (word0 implicit zero). Else emit
today's `StringMake(ptr, len)` against the rodata symbol (plus a
synthetic `word1 = 0`, `word2 = len`).

The compiler tables in `cmd/compile/internal/staticdata` that emit
string constants for `reflect.*` (type names, method names, …) must
likewise emit 24 B rather than 16 B.

### 5.5 String header manipulation sites

- `ir/expr.go:642` `StringHeaderExpr` — currently `{Ptr, Len}`; grow to
  `{Word0, Word1, Word2}` (or add accessor methods and keep two-field
  public shape for compatibility with backends).
- `walk/builtin.go` — `len`, `cap`, `copy` on strings.
- `walk/compare.go`, `compare/compare.go` — string equality short-circuit
  on inline-inline case.
- `walk/convert.go` — `string(b)`, `[]byte(s)`, `string(r rune)`,
  `string([]rune)`.
- `reflectdata/alg.go` — `ASTRING` equal/hash generation.

## 6. ABI

Pass by value in up to 3 integer registers; spill to 24 B stack slot
otherwise.

- AMD64 (9 int arg regs): fits; one more string param before spill than
  today (was 2 per string, now 3).
- ARM64 (16 int arg regs): plenty.
- RISC-V/MIPS/LoongArch (8 int arg regs): fits.
- 386: no register ABI; stack footprint grows by 50%.

No "pass by pointer" fallback — same reasoning as fat-iface.

## 7. reflect

- `reflect.StringHeader` (`reflect/value.go:2800`) is already deprecated
  but still exported. Grow to three fields; `Data` keeps meaning "word 0",
  `Len` becomes "word 2 (low 60 bits)". Third-party constructions of
  `reflect.StringHeader{Data: p, Len: n}` cast to `string` still work
  for non-empty heap-rep strings (Data = pointer, word1 = 0, word2 = n,
  tag = 0). **Backwards-compatible for the common heap-rep case.**
  Third-party code that *reads* `.Data` on a string produced by gd will
  get nil for inline-rep strings — visible-empty but safe. Document as
  a soft ABI break; recommend `unsafe.StringData` for new code.
- `reflect.Value.String()` — reads header; works if it calls `len(s)` /
  `unsafe.StringData`. Audit.
- `reflect.Value.Bytes()` on a `[]byte` built from a string — no change.
- `reflect.Value` itself does **not** need to grow for strings (unlike
  fat-iface). `reflect.Value.ptr` is a pointer to a storage slot; for a
  String value it points to a `stringStruct` (now 24 B). All `flagIndir`
  cases work.
- `unsafe.String(ptr, len)` — see §2: always heap rep.
- `unsafe.StringData(s)` — returns `ptr(s)`. For inline rep, returns
  `&s.word0` (address into caller's string variable). Lifetime is the
  string's lifetime, as documented.

## 8. Phasing

### Phase A — layout plumbing
Grow `stringStruct` / `stringStructDWARF` / `StringHeader` /
`StringHeaderExpr` / `StringSize` / `synthString` (if any) / TSTRING
`intRegs` to 3. Grow SSA `StringMake` to arity 3. Update
`expand_calls.go` TSTRING. All sites still emit heap rep (word 1 = 0,
word 2 = len, tag = 0). Stock-compatible modulo size. No inline
construction yet — pure scaffolding.

Sub-phases:
- **A-scaffold:** bump `StringSize`; add `lenMask`, `tagShift` consts;
  add helper methods on `stringStruct`; no wire-format change.
- **A-layout:** grow the struct itself; update every shadow (`maps`,
  `reflect.StringHeader`, compiler's `StringHeaderExpr`); grow SSA op
  arity; fix every `string` literal emission site. Expect asm audit
  (parallel to fat-iface's `asm_amd64.s` `$16-16` finding).
- Runtime sanity assertion.

### Phase B — `len` / `ptr` lowering
Change `StringLen` to emit `(word2 & lenMask)`. Change `StringPtr` to
emit the conditional. Gate behind `-d=gdsso=1` for A/B. Measure branch
cost on microbenchmarks and representative workloads before proceeding.

### Phase C — inline construction
`string([]byte)`, `concatstrings`, string literal lowering, `rawstring`
fast path. After C, `len ≤ 15` strings no longer allocate.

### Phase D — runtime hash/equality
`strhash` / `strequal` fast path for inline-inline via direct header
memcmp.

### Phase E — reflect
Audit `reflect.Value.String`, ensure helpers go through `unsafe.String`/
`StringData`.

### Phase F — fat-iface integration (§9).

## 9. Phase 2 — string in fat-iface without boxing

A fat-iface is 32 B: `{tab, data, inline[0], inline[1]}`. A string is
24 B: `{word0, word1, word2}`. To store a string in an iface without
boxing, align the string's word 0 with the iface's `data` slot:

```
iface-holding-string, non-boxed:
  offset  0: tab        // *itab for stringItab (singleton)
  offset  8: word0      // string's word0 — heap ptr (heap-rep) OR nil (inline-rep)
  offset 16: word1      // string's word1 — unused (heap) OR bytes[0:8] (inline)
  offset 24: word2      // string's word2 — len (heap) OR tag | bytes[8:15] (inline)
```

The iface `data` slot *is* the string's word 0; the 16 B inline slot
covers word 1 + word 2. Exact 24 B fit.

**GC.** Fat-iface ptrmask is `[tab=no, data=yes, inline=no, inline=no]`.
For string-holding ifaces the `data` slot is the string's word 0 —
either a live heap pointer or nil (§0, §1). The scanner sees a
valid pointer or nil, unchanged from the fat-iface boxed case.
**No new ptrmask shape needed; no false-retention concern.** The
nil-word-0 discipline extends naturally through the iface.

**Discriminator.** String gets a reserved `TFlagInlineIfaceString` in
addition to `TFlagInlineIface`, OR string is the only type that uses a
fatter-than-16B inline payload and is recognized by a dedicated bit on
itab. Proposed: extend `abi.ITab.Inline` from `uint8` to `uint8` with
values `{0 = boxed, 1 = inline≤16B, 2 = inline-string-24B}`. Compiler
switches on it.

**Compiler.** `OCONVIFACE` from `string` → `any` under gd emits:
```
eface{tab: stringItab, word0: s.word0, word1: s.word1, word2: s.word2}
```
No `convTstring` call. `convTstring` at `iface.go:419` stays for slow
paths and reflect use (linkname-compatible).

**Dispatch.** No interface methods on `string`. The only iface ops on
string-holding ifaces are:
- `any → string` type assertion: 3-word memcpy from iface words 1..3 into
  the string dest (modulo tag check against `stringItab`).
- `string == string` through `any`: `ifaceeq` grows a case: when
  `itab.Inline == 2`, memcmp words 1..3.
- `reflect.Value` round-trip: `unpackEface` when itab is `stringItab`
  reconstructs the string from inline words.

**Eligibility.** The only concrete type with Inline == 2 is `string`
itself. Not generalized to other 24 B types.

**Phase ordering.** Phase 2 gated on fat-iface phase H (type switches)
being stable, since string-in-iface exercises the `Inline != 0`
assertion path heavily.

## 10. Risks / open questions

1. **`len(s)` cmov cost.** Stock lowers to one load. Under gd, `len(s)`
   becomes load-word2 + shift + test + cmov (≈3–4 extra cycles). On
   tight loops (`for i := 0; i < len(s); i++`) LICM should hoist. Needs
   microbench; if it regresses hot loops meaningfully, consider caching
   len in word 1 for heap strings too (wastes 8 B but saves the cmov).
2. **Zero-word-0 invariant.** Every inline-construction site must zero
   word 0 explicitly; leaving it undefined would leak register garbage
   into a slot the GC treats as a pointer. Fuzzing candidate: construct
   via every path (`string([]byte)`, `concatstrings`, literal,
   `unsafe.String`, reflect, runtime internal) and assert
   `(*stringStruct)(unsafe.Pointer(&s)).str == nil` whenever
   `isInline(s)`.
3. **Escape of `&s[0]` into inline.** Parallel to fat-iface dispatch
   escape. Needs EA support — treat `StringPtr(s)` as escaping-input when
   result escapes.
4. **`unsafe.Pointer(&s)` + pointer arithmetic.** Third-party code that
   pokes at string bytes via `(*reflect.StringHeader)(unsafe.Pointer(&s))
   .Data` reads word 0. Under gd:
   - Heap-rep strings: Data = word 0 = data pointer — unchanged. Len via
     `(*reflect.StringHeader).Len` reads word 1, which under gd holds
     the cached hash (0 initially), **not the length**. This is a break
     for any reader that uses `reflect.StringHeader.Len`.
   - Inline-rep strings: Data = nil (word 0 is zero for inline). Readers
     see an empty string.
   Strictly worse than stock for any external reader of
   `reflect.StringHeader`. Document; recommend `unsafe.StringData`
   (returns correct pointer for both reps) and `len(s)` (returns correct
   length for both reps) for new code.
5. **`//go:linkname` string symbols.** `runtime.concatstrings`, `slicebytetostring`,
   `rawstring` are linknamed externally. Signature preserved, return
   representation may be inline. External caller that passes the result
   to stock-compiled code fails (size mismatch). Gd toolchain is
   already an ABI break across the board, so this is covered.
6. **`strings.Builder`.** Returns `unsafe.String(b, n)` where b is its
   heap buffer. Never inline-rep. Not a bug but a missed optimization;
   consider a fast path in `Builder.String()` that copies ≤ 15 B into
   an inline string.
7. **`reflect.StringHeader` misuse.** See §7. Legacy code using
   `StringHeader{Data, Len}` constructs heap-rep strings with extra=0.
   Works for heap reads; breaks if the string was inline (Data is bytes,
   not a pointer). The deprecated-ness and gd's overall ABI break give
   cover, but flag it prominently.
8. **Debuggers.** delve renders `string` via its layout. Every delve
   version in the wild mis-renders gd strings (shows a 24 B struct with
   meaningless fields for inline case). Document; upstream a patch.
9. **`sync.Pool`-style shadow structs.** Like `sync.eface` was for fat-
   iface: are there `sync.stringStruct`-equivalent shadows? Grep
   pending — at least `internal/runtime/maps/runtime_faststr.go:93`.
10. **Channels of string.** Size-driven copy; `chan` element = 24 B under
    gd. Should work; confirm element ptrmask reflects new layout.
11. **`panic(s)` where `s` is string.** `_panic.arg` is `any`. Post
    phase-2 fat-iface integration this stays in 32 B iface slot
    unchanged. Pre phase 2, `convTstring` boxes.

## Critical files

- `src/runtime/string.go:290–299` — `stringStruct` / `stringStructDWARF`.
- `src/internal/runtime/maps/runtime_faststr.go:93` — shadow.
- `src/reflect/value.go:2800` — `StringHeader`.
- `src/cmd/compile/internal/ir/expr.go:642` — `StringHeaderExpr`.
- `src/cmd/compile/internal/types/universe.go:54` — `StringSize`.
- `src/cmd/compile/internal/types/size.go:392` — TSTRING size + intRegs.
- `src/cmd/compile/internal/typebits/typebits.go:42` — TSTRING ptrmask.
- `src/cmd/compile/internal/ssa/_gen/genericOps.go:334,530–532` — SSA ops.
- `src/cmd/compile/internal/ssagen/ssa.go:4574` — TSTRING const lowering.
- `src/cmd/compile/internal/walk/convert.go` — `string(b)`, `[]byte(s)`.
- `src/cmd/compile/internal/compare/compare.go` — string eq fast path.
- `src/runtime/alg.go` — `strhash`, `strequal`.
