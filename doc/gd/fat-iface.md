# Fat interface implementation plan (gd fork)

Goal: expand Go's `iface`/`eface` from 16 B to 32 B, with 16 B of inline
storage for pointer-free value types ≤ 16 B. Boxed representation used
for everything else. First optimization in the gd fork; ABI break is
expected and acceptable.

## 0. Premise check

GC does **not** need a per-iface "is-inline" discriminator. In stock Go,
`src/cmd/compile/internal/typebits/typebits.go:49` emits the ptrmask
for `TINTER` with the data word marked as a pointer and the tab/_type
word not (tab lives in rodata/persistentalloc). Under gd, the ptrmask
becomes `[tab=no, data=yes-if-boxed, pad=no, pad=no]` — but because the
eligibility rule forbids inline for pointer-containing types, the data
word can always be marked pointer: inline types store non-pointer bits
there (or nil), boxed types store a live heap pointer there. Scanner
sees what it always saw.

Files known to assume 2-word iface that need gd variants:
- `src/runtime/iface.go:673` (`reflect_ifaceE2I`)
- `src/cmd/compile/internal/ssa/_gen/generic.rules:766` (iface eq)
- `src/cmd/compile/internal/abi/abiutils.go:598` (`synthIface`)
- `src/cmd/compile/internal/ssa/expand_calls.go` cases at TINTER (3 sites:
  arg-decompose, rewriteSelectOrArg, rewriteWideSelectToStores) — must
  produce/consume 4 slots per iface or regalloc ICEs ("index out of range").
- `src/sync/poolqueue.go` declares its own `eface` shadow that mirrors
  runtime.eface — must be grown in lockstep, otherwise `*(*any)(unsafe.
  Pointer(&slot))` reads/writes past the slot and corrupts sync.Pool.
- `src/runtime/asm_amd64.s` `debugCallPanicked` declares frame size
  `$16-16` assuming `val interface{}` is 16 B; needs `$32-32` and 4-word
  copy (likely mirrored in every `asm_*.s`).
- `src/cmd/compile/internal/types/size.go` TINTER case (w, intRegs, align,
  ptrBytes). Keep `ptrBytes = 2*PtrSize` (only data is in the ptr prefix).

## 1. Layout (32 B on 64-bit, 24 B on 32-bit; 8-byte aligned)

```
offset  0: tab  *itab       (iface)   OR   _type *_type   (eface)   [never scanned — rodata]
offset  8: data unsafe.Pointer                                       [scanned]
offset 16: inline[0] uint64                                          [never scanned]
offset 24: inline[1] uint64                                          [never scanned]
```

The inline field is `[2]uint64`, not `[2]uintptr`, so the inline slot is a
fixed 16 bytes regardless of pointer size. On 32-bit `uintptr` would
collapse the slot to 8 bytes and kill the optimization; `uint64` preserves
it and forces 8-byte struct alignment (which the tab+data prefix satisfies
naturally: 4+4 on 32-bit, 8+8 on 64-bit).

- **Inline rep:** `data == nil`, payload lives at bytes 16..31.
- **Boxed rep:** `data` is a heap pointer (today's behavior), inline slot ignored.
- **Nil iface:** `tab == nil && data == nil && inline == 0`.
- **Discriminator:** a bit on `_type.TFlag`, cached on `itab.Inline`.
  Inline-ness is a property of the concrete type, not the iface value.

Add `abi.TFlagInlineIface TFlag = 1 << 6` in `src/internal/abi/type.go`
(after `TFlagDirectIface` at line 126). Populate in
`src/cmd/compile/internal/reflectdata/reflect.go:498`. Mirror in
`src/reflect/type.go` at 1889/1970/2601/2770 and `src/reflect/map.go:61`.

Cache: add `Inline uint8` to `abi.ITab` at `src/internal/abi/iface.go:14`
so the hot method-dispatch path doesn't chase `.Type.TFlag`. Populated
by `runtime.itabInit` at `src/runtime/iface.go:204`.

## 2. Eligibility

```
T.Size_ <= 16  &&  T.PtrBytes == 0  &&  T.Align_ <= 8
```

Strictly broader than `TFlagDirectIface` (which is "exactly one pointer").
Direct-iface types stay boxed under the new rule (they contain a pointer,
`PtrBytes > 0`) — preserves their existing GC behavior without special-
casing.

Applied in three places:
1. `internal/abi.Type.IsInlineIface()` in `src/internal/abi/type.go:205`.
2. `cmd/compile/internal/types.IsInlineIface(t)` in
   `src/cmd/compile/internal/types/type.go:1844`.
3. `src/reflect/type.go` alongside `TFlagDirectIface` setters.

## 3. GC scanner

One change: `typebits.set` TINTER case (`src/cmd/compile/internal/typebits/typebits.go:49`)
walks 4 words instead of 2, sets the pointer bit only on word 1 (data).
`scanobject`/`scanblock`/`pointerMask`/`fillptrmask` are ptrmask-driven
and inherit automatically. Stack maps (`src/cmd/compile/internal/liveness/plive.go`)
reuse `typebits.set` and must size bitmaps from `t.Size()`, not hardcoded
`2*PtrSize` — verify.

`types.PtrDataSize(TINTER)` should still return 16 (offset of data + PtrSize),
so the trailing inline words are outside the ptrmask suffix.

`src/runtime/iface.go:697` (`staticuint64s` fast path) becomes dead for
`convT16/32/64` under gd — drop from hot path, keep symbol live for reflect.

## 4. Runtime (`src/runtime/iface.go`)

All behind `//go:build gd`. Functions and lines:

| Function | Line | Change |
|---|---|---|
| `iface`, `eface` | `runtime2.go:184,189` | Grow to 32B, add `inline [2]uintptr`. |
| `itab` | via `abi/iface.go:14` | Add `Inline uint8`. |
| `itabInit` | `iface.go:204` | Populate `m.Inline`. |
| `convT`, `convTnoptr` | `iface.go:334,348` | Slow/boxed fallback, unchanged. |
| `convT16/32/64` | `iface.go:365,378,400` | Dead from compiler side; keep as linkname stubs for third-party. |
| `convTstring/slice` | `iface.go:419,438` | Unchanged (both contain pointers, stay boxed). |
| `assertE2I{,2}` | `iface.go:449,457` | Unchanged. Inline-ness on itab. |
| `typeAssert` | `iface.go:467` | Unchanged; SSA caller handles inline copy. |
| `reflect_ifaceE2I`, `reflectlite_ifaceE2I` | `iface.go:673,678` | Copy 32B; update literals. |

Optional helper: `ifaceUnbox(i) *byte` → returns `&i.inline[0]` if inline
else `i.data`. Used by method dispatch so the compiler doesn't re-emit
the branch.

## 5. Compiler

### 5.1 OCONVIFACE — `src/cmd/compile/internal/walk/convert.go`

- `dataWord` (line 129) → split: when `types.IsInlineIface(fromType)`,
  emit in-place assignment (zero data, memmove 16B into inline[0..1]).
  Skip `convT*`/`convTnoptr`.
- `dataWordFuncName` (line 366) only called for non-inline.
- Revisit `staticuint64s` branch (lines 161–177) and zero-sized shortcut
  (line 156) — drop for inline path.

### 5.2 SSA ops

- `OpIMake` keeps arity 2 for boxed; add `OpIMakeInline` (tab + 2-word
  payload). `OpIData` → add `OpIInlineAddr` + `OpIInlineLoad`. Consumers
  pick.
- `EqInter`/`NeqInter` at `generic.rules:766` — always dispatch to
  `runtime.ifaceeq` under gd; peephole only when both sides statically
  boxed. `ifaceeq` must memcmp inline when `Inline` set.
- `isFixedLoad` rewrites (lines 2187, 2191) — update offsets.
- `expand_calls.go` — teach that TINTER has 4 fields.
- `src/cmd/compile/internal/ssagen/ssa.go:3618–3632` — `OITAB`/`OIDATA`/
  `OMAKEFACE` handle inline vs boxed.

### 5.3 Method dispatch — `ssa.go:5210` (`getClosureAndRcvr`)

Receiver for inline type = `&iface.inline`; for boxed = `iface.data`.
Emit SSA select on `itab.Inline`. Single load + select, predicts well.

Escape caveat: `&iface.inline` can't outlive the iface. Treat iface
method calls as causing the iface itself to escape (already mostly
true in practice). Revisit after benchmarks.

### 5.4 Type assertion — `ssa.go:6502` (`types.IsDirectIface` switch)

Three-way: direct / inline / indirect. Inline → memmove from
`iface.inline` to destination. Type switches (`interfaceSwitch`) don't
care about inline-ness at switch time, only at payload materialization.

### 5.5 reflectdata — `src/cmd/compile/internal/reflectdata/reflect.go`

- Line 498: set `TFlagInlineIface` alongside `TFlagDirectIface`.
- `writeITab` (line 1019): add `Inline` byte.
- `rttype.ITab.OffsetOf("Fun")` auto-picks up layout change — verify.

### 5.6 ABI — `src/cmd/compile/internal/abi/abiutils.go:598`

`synthIface` grows from 2 to 4 fields. `ABIAnalyze`/`tryAllocRegs`
handle automatically. Must match `src/internal/abi/iface.go`
exactly — add a runtime `unsafe.Sizeof(eface{}) == 32` assertion.

## 6. ABI

Pass by value in up to 4 integer registers; spill to 32B stack slot
otherwise. No "pass by pointer" fallback — liveness would need GC roots
on the stack anyway.

- AMD64 (9 int arg regs): fits; earlier spill with multiple iface params.
- ARM64 (16 int arg regs): plenty of room.
- RISC-V/MIPS/LoongArch (8 int arg regs): fits; earlier spill.
- 386: no register ABI; stack footprint doubles. No code change.

Floats in inline payload round-trip via integer regs (same as today's
`any`-of-float).

## 7. reflect

`reflect.Value{typ_, ptr, flag}` grows from 3 words to 5 to carry the
inline payload. Breaking ABI — accept it.

- `packEface` (value.go:123) — build 32B EmptyInterface, copy payload
  to Inline when inline, leave Data nil.
- `packEfaceData` (line 132) → `packEfacePayload`, returns 16B blob.
- `unpackEface` (line 158) — read 32B, decide inline from
  `t.IsInlineIface()`, set `flagIndir` only when boxed. Inline values
  behave like direct from Value's perspective: `v.ptr` conceptually
  points into the Value struct.
- Audit: `Value.pointer` (112), `Elem` (1225), `Field` (1276), `Index`
  (1416), `Interface` (1494), `Addr` (271), `Bytes` (310), scalar
  accessors. Pattern: today's `flagIndir ? load : bits` gains a third
  arm for inline payload.
- `src/reflect/badlinkname.go:28` (`unusedIfaceIndir`) — preserve
  stock semantics for external linkname users.

## 8. Phasing

### Phase A — layout plumbing (invasive, biggest)
Grow iface/eface/EmptyInterface/NonEmptyInterface/CommonInterface/synthIface
to 32B/4 fields. Add `TFlagInlineIface`, `IsInlineIface`, itab `Inline`.
Update `typebits.set` TINTER. Grow `reflect.Value` to 5 words. Update
`reflect_ifaceE2I` etc. Runtime sanity assertions.

All sites still emit boxed. Inline payload always zero. Stock-compatible
modulo size.

ABI register decomposition lands here (not optional — synthIface grew).

Sub-phases, refined after a first attempt:

- **A-scaffold (safe, buildable):** flag bit + `IsInlineIface` helpers +
  `ITab.Inline` field + reflectdata setters. No layout change; stock
  tests stay green. Verified to build clean by itself — land it first.
- **A-layout (the hard part):**
  1. Grow runtime `iface`/`eface` + `abi.EmptyInterface`/
     `NonEmptyInterface`/`CommonInterface` + `sync.eface` shadow.
  2. Grow compile-side `synthIface` to 4 fields + update
     `types.size.go` TINTER (w, intRegs, align) in lockstep.
  3. Update `expand_calls.go` TINTER cases (3 of them) to decompose
     into 4 slots. Phase A emits boxed only, so inline slots can be
     `OpConst64 0` on the caller side and discarded on the callee
     side — no new SSA ops needed.
  4. Grow `reflect.Value` to 5 words; audit ~35 positional `Value{…}`
     literal sites (in `reflect/value.go`, `reflect/makefunc.go`,
     `reflect/map.go`).
  5. Audit every `asm_*.s` for hardcoded iface frame sizes — known
     site: `debugCallPanicked` in `asm_amd64.s` (`$16-16` → `$32-32`).
  6. Runtime size assertion (const underflow idiom in `runtime2.go`).

First attempt landed steps 1–3 but go_bootstrap SEGVs at runtime
(`sync.(*poolDequeue).popTail`, `addr=0x2000`) — toolchain2 install
fails. Build is green, so the bug is in generated code, not the
compiler's static checks. Next session: bisect by reverting each
sub-step and/or cut a minimal repro (small program + toolchain1's
compile, inspect the -S output for the iface hot path).

### Phase C — GC verification
Before any code writes into inline, verify `typebits.set` / `plive.go` /
stack maps skip inline words. Run `GODEBUG=gctrace=1 clobberfree=1`
plus runtime ptrmask tests.

### Phase D — compiler inline emission (OCONVIFACE)
New `OpIMakeInline`, walk-level switch in `dataWord`. Gated behind
`-d=gdinline=1` debug flag for A/B.

### Phase E — dispatch + assertion reads
`getClosureAndRcvr` select, assertion materialization branch. After E,
phase D can stop shadowing inline payload into `data`.

### Phase F — runtime convT* pruning
Compiler never emits `convT16/32/64/convTnoptr` for small pointer-free
types. Keep as linkname stubs. `assertE2I*` trivial no-op.

### Phase G — reflect
`packEface`, `unpackEface`, flagIndir accessors. Validates after E so
interface round-trip tests exercise real inline path.

### Phase H — type switches + comma-ok + I2I
`typeAssert`, `interfaceSwitch`, `walkConvInterface` (convert.go:104).

### Phase I — EqInter / ifaceeq
Always route through `runtime.ifaceeq` under gd; extend to memcmp inline.

## 9. Risks / open questions

1. **Value vs pointer receivers on inline types.** Methods on `*T`
   where `T` is inline: no meaningful `*T` outside the iface. Either
   box those types (losing optimization) or disallow. Needs prototype.
2. **Escape of inline receivers.** Callee receiving `&iface.inline`
   that captures. Either mark iface itself as escaping on method calls,
   or EA must copy at escaping sites. Needs new EA support.
3. **Struct literal sites.** `iface{tab, data}` in runtime — audit via
   grep; `reflect_ifaceE2I` at `iface.go:673` is one of many.
4. **cgo and assembly.** Runtime asm pokes eface/iface (reflectcall,
   panic/defer, finalizers). Audit `src/runtime/asm_*.s`.
5. **Linker-built itabs.** `itabsinit` at `iface.go:259` walks
   `moduledata.itablinks` — compile-time itabs need `Inline` populated
   at link time. Check `src/cmd/link/internal/ld/*`.
6. **Sonic etc.** `convT64`, `convTstring`, `convTslice`, `getitab`,
   `reflect_ifaceE2I` are linknamed externally (comments at
   `iface.go:42, 391, 410, 429, 663`). Signatures preserved, internals
   diverge. Link-time reject of `//go:linkname` from non-gd packages to
   gd runtime is safer than runtime corruption.
7. **Generics / shape types.** `walkConvInterface` (line 49) lowers
   shape-to-interface. Verify `HasShape` threads `TFlagInlineIface`
   through dictionary lookup.
8. **Panic values.** `_panic.arg` is `any`. Panic/recover rebuild
   ifaces across goroutines. Audit `src/runtime/panic.go` + defer asm.
9. **Channels.** `chan.go` copies elements by size; TINTER elements =
   32B under gd. Size-driven, should work; confirm GC element ptrmask
   reflects new layout.
10. **Debuggers.** delve reads iface layout to pretty-print `any`.
    Every delve version in the wild mis-renders gd ifaces until
    patched. Document; upstream a delve patch.
11. **Outstanding bug (from first A-layout attempt).** With steps 1–3
    of A-layout applied the toolchain builds and runs short runtime/
    reflect tests green, but go_bootstrap SEGVs in
    `sync.(*poolDequeue).popTail` with `addr=0x2000` while scanning
    packages. `sync.eface` and runtime `eface` were grown in lockstep
    so the 32 B layout is consistent. Hypotheses: (a) compiler-emitted
    load/store of `*(*any)` still moves only 16 B (dec.rules covers
    2 words; the inline tail is untouched), so if a slot's inline
    words were non-zero from a prior use the SSA value carries garbage
    — but nothing reads inline yet, so this alone shouldn't crash;
    (b) some runtime asm site copies only 16 B for a 32 B iface arg
    (debugCallPanicked found, others TBD); (c) a ptrmask drift that
    only matters under GC pressure. Workflow to bisect: feed
    toolchain1's compile a tiny program that exercises sync.Pool +
    `any`, compare `-S` output before/after A-layout, look for any
    16 B vs 32 B disagreement on iface transit.

## Critical files

- `src/runtime/runtime2.go:184–192` — iface/eface layout.
- `src/internal/abi/iface.go` — ITab + interface header layouts.
- `src/cmd/compile/internal/typebits/typebits.go:49` — GC correctness hinge.
- `src/cmd/compile/internal/walk/convert.go:129–245` — `dataWord`,
  `dataWordFuncName`.
- `src/cmd/compile/internal/ssagen/ssa.go:3618–3632` (OITAB/OIDATA/
  OMAKEFACE), 5210 (`getClosureAndRcvr`), 6502 (type assertion).
