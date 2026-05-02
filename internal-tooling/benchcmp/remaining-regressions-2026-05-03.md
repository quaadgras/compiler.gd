# Remaining benchmark regressions (verification + clusters) — 2026-05-03

Re-checked the "Top 30 regressions" table from
`internal-tooling/benchcmp/bench-report.md` (snapshot 2026-05-02 05:17) against
HEAD (commit 3d63576571 + the four named pre-fixes: 92152d0c5f, e96d0ede23,
8f475a0763, 2315d39447). Spot-checks ran with
`-benchtime=300ms -count=2` on each side, gd via the local toolchain,
stock via `/usr/lib/go/bin/go` against `/usr/lib/go/src`.

Two of the four pre-fixes hold up under fresh measurement:
`expvar.BenchmarkStringSet` is 0.33 ns/op (vs the 947.5 ns/op in the stale
table — confirmed fixed) and `sync.BenchmarkMapCompareAndSwapMostlyMisses/*sync_test.DeepCopyMap`
is 2.92 ns/op (vs 818.4 ns/op stale — fixed). Everything else in the Top‑30
is still regressed.

## Cluster A — `s = s[k:]` byte-scan loops (loopheapify bails on OSLICESTR)

Affected by `cmd/compile/internal/loopheapify/loopheapify.go:43-44, 257-262`
(OSLICESTR is in the bail set), and the pass also defaults off
(line 84-86, gated on `GOAUTOHEAPIFY=1`). Heap-rep strings keep being
re-decoded inside the loop body.

| benchmark | gd ns | stock ns | delta | site |
|---|---:|---:|---:|---|
| strings.BenchmarkTrimASCII/4096:1 | 5998 | 873 | +587% | `src/strings/strings.go:1019` (`trimLeftASCII`) and `:1064` (`trimRightASCII`) |
| strings.BenchmarkTrimASCII/256:1 | 384 | 62 | +519% | same |
| unicode/utf8.BenchmarkValidString100KASCIIChars | 42660 | 1513 | +2719% | `src/unicode/utf8/utf8.go:524-543` (`ValidString`, several `s = s[k:]`) |
| unicode/utf8.BenchmarkValidStringLongMostlyASCII | 46931 | 3627 | +1194% | same |
| path.BenchmarkMatch/"a*b*c*d*e*/f"_"axbxcxdxe/f" | ~290 | ~58 | +400% | `src/path/match.go:147-188` (`matchChunk` `s = s[1:]` / `chunk = chunk[1:]`) |
| path/filepath.BenchmarkMatch/* | ~290–377 | ~58–78 | +400% | `src/path/filepath/match.go` mirrors the same pattern |

Suggested fix complexity: **compiler-pass extension**. Either:
- Drop the `OSLICESTR` bail in `loopheapify.go` and add an "alias-aware"
  refresh of `__heap` when the loop body re-assigns `s` from
  `OSLICESTR(s, …)` (the pass already needs to re-decode after every
  `s = s[k:]`); or
- Add a peephole that recognizes `for len(s) > 0 { … s = s[k:] }` as the
  canonical byte-scan idiom and lowers it to a pointer cursor + length
  count, keeping the SSO bytes pointer hot in a register.
  Either route is roughly the same scope (~hundreds of lines).
- Default the pass on (one-line in `enabled`) once it's safe.

## Cluster B — `reflect.Value` in tight loops (fat-iface unpack)

`reflect.ValueOf` boxes the empty interface, then for inline-rep types it
copies the 16-B inline payload into `Value.inline`
(`src/reflect/value.go:248-267`). Every `Value.Field` / `Value.Index` /
`Value.Type` returns a fresh fat-iface header, and most short ops (
`v.Bool()`, `v.Int()`) go through the same path. Stock uses a 16-B
`(typ, ptr, flag)` Value with a single pointer — far cheaper.

| benchmark | gd ns | stock ns | delta | site |
|---|---:|---:|---:|---|
| reflect.BenchmarkDeepEqual/bool | 77.9 | 14.8 | +426% | `src/reflect/deepequal.go:229` (`DeepEqual` → `ValueOf` × 2) |
| reflect.BenchmarkDeepEqual/int | 72.2 | 15.2 | +375% | same |
| reflect.BenchmarkDeepEqual/string | 77.3 | 16.2 | +377% | same |
| reflect.BenchmarkDeepEqual/[6]uint8 | 536.8 | 81.4 | +560% | array path; per-elem `v.Index(i)` returns fat-iface |
| reflect.BenchmarkIsZero/StructIncomparable | 124 | 25.4 | +388% | `src/reflect/value.go:2040` (`IsZero` walks fields) |
| reflect.BenchmarkIsZero/ArrayIncomparable | 572 | 111 | +415% | same |
| encoding/binary.BenchmarkAppendSlice1000Structs | 721000 | 144000 | +400% | `src/encoding/binary/binary.go:914` (`encoder.value` recursive Slice→Struct→primitive) |
| fmt.BenchmarkScanInts | 191800 | 135600 | +41% | reflect-driven scan in `src/fmt/scan.go` |
| fmt.BenchmarkScanRecursiveInt | 23.4 ms | 18.8 ms | +25% | same |
| errors.BenchmarkAs | 391 | 234 | +67% | `src/errors/wrap.go:121-149` uses `reflectlite.ValueOf` per Unwrap step |

Suggested fix complexity: **fundamental design change** — this is the
known fat-iface reflect alloc/cost regression
(`project_fat_iface_alloc_regression.md`). Until `reflect.Value` either
shrinks back to 16 B (via a layout change) or grows a fast-path that
doesn't touch the fat-iface inline copy on every `Field/Index`, this
cluster stays put. Realistically 200-1000 lines plus a soak.

Note: `BenchmarkAs` also adds **+1 alloc, +80 B/op** in gd
(`stock 2 alloc / 40 B → gd 3 alloc / 120 B`). The extra alloc is the
24-B SSO header (`errorT.s string`) being boxed into the 32-B fat-iface
when stored back into `target.Elem().Set(reflectlite.ValueOf(err))`
(`src/errors/wrap.go:124`).

## Cluster C — short-string `==` in tight loops (SSO 3-word fast path tax)

`runtime.streqfast` (`src/runtime/alg.go:354-378`) decodes the SSO
header on every `==`, doing a 3-word compare before falling through to
`memequal`. Stock's compare is one length+pointer compare. For
≤8-char keys this is a measurable hit when the loop iterates a lot.

| benchmark | gd ns | stock ns | delta | site |
|---|---:|---:|---:|---|
| net/http.BenchmarkFindChild/n=32/rep=linear | 146.8 | 26.6 | +452% | `src/net/http/mapping_test.go:147-154` (`findChildLinear` walks `[]entry` and compares `key == e.key`) |
| net/http.BenchmarkFindChild/n=16/rep=linear | 76.3 | 11.2 | +581% | same |
| net/http.BenchmarkFindChild/n=8/rep=linear | 37.3 | 6.2 | +501% | same |

Suggested fix complexity: **small refactor** in
`cmd/compile/internal/ssagen/ssa.go` (the `==` lowering for strings) —
emit a single fused branch that handles "both inline + same length"
in one cmp, falling through to `memequal` only on a length match. Or
inline the streqfast body so the SSO-bit test stays in a register.
~50–150 lines.

## Cluster D — heap-rep string conversion in encode/decode hot paths

| benchmark | gd ns | stock ns | delta | extra alloc/B |
|---|---:|---:|---:|---|
| encoding/gob.BenchmarkEndToEndSliceByteBuffer | 10780 | 8634 | +25% | -100 alloc / -1.6K B (good!) but ns up |
| encoding/gob.BenchmarkEncodeInterfaceSlice | 16190 | 13200 | +23% | mostly ns regression |
| encoding/json.BenchmarkCodeUnmarshal | 2459 µs | 1973 µs | +25% | +180 KB / -1900 alloc (mixed) |
| encoding/json.BenchmarkCodeUnmarshalReuse | 2130 µs | 1731 µs | +23% | +90 KB / -1100 alloc |
| net/url.BenchmarkEncodeQuery/oe=utf8&q=puppies | 192 | 149 | +29% | +1 alloc / +48 B |
| fmt.BenchmarkScanRecursiveIntReaderWrapper | 28100 | 22090 | +27% | reflect-related (Cluster B) |

EncodeQuery's +1 alloc/call (`src/net/url/url.go:1009-1016`): each
`QueryEscape` returns a SSO-rep `string` (24-B header) which is boxed
into an `iface{}` for `buf.WriteString`. The argument-stack widening
forces a copy of the SSO inline bytes that wasn't needed before.

JSON decode bytes-up: heap-rep string headers are 24 B (vs 16 B stock),
so caches/maps containing strings grow 1.5× per entry.

Suggested fix complexity: **small refactor** for EncodeQuery (have
`escape()` write directly into a caller-provided `*strings.Builder`
instead of round-tripping through `string`); **fundamental** for the
JSON/gob ones (heap-rep header size is what it is until SSO Phase D
trims it).

## Cluster E — encoding/json type-fields cache

| benchmark | gd | stock | delta |
|---|---:|---:|---:|
| encoding/json.BenchmarkTypeFieldsCache/MissTypes100000 | 21.2 ms / 117 MB / 1438768 alloc | 17.9 ms / 95.9 MB / 1438766 alloc | +18.8% ns / +22% B / +2 alloc |

Cache value type contains string keys; SSO 24-B header per cached entry
multiplies into the +22% B. Two extra allocs are ambient noise.

Suggested fix complexity: **fundamental** — same 24-vs-16-B header
issue as Cluster D.

## Already-fixed regressions (no longer in regression list)

Verified via fresh runs:

- `expvar.BenchmarkStringSet` → 0.33 ns/op (was 947 ns/op stale).
- `encoding/gob.BenchmarkEncodeInterfaceSlice` → 16.19 µs / 211 B / 0
  alloc (was 1.16 ms / 3.1 KB stale; commit 8f475a0763 seqlock).
  Still ~+22% vs stock 12.7 µs but no longer the 7900% headline.
- `sync.BenchmarkMapCompareAndSwapMostlyMisses/*sync_test.DeepCopyMap`
  → 2.92 ns/op (was 818 ns/op stale; commit e96d0ede23 short-circuit
  Store).

## Top easy wins (<50 LOC each, mostly compiler/runtime peephole)

1. **String `==` short-circuit for SSO fast path** (Cluster C):
   inline a single fused length+inline-bit branch in
   `cmd/compile/internal/ssagen/ssa.go`'s string-eq lowering. Targets
   net/http.FindChild and any short-key map-iteration code.
2. **EncodeQuery in-place escape** (Cluster D, single benchmark):
   add an internal `appendEscape(dst []byte, s string, mode encoding) []byte`
   helper in `src/net/url/url.go`, route `Values.Encode` through it.
   Removes the per-call boxing + temporary string. ~30 LOC.
3. **Default `loopheapify` to on for `s = s[k:]` byte-scan loops**
   once the OSLICESTR alias-tracking lands (Cluster A): ~10 LOC in
   `loopheapify.go:84-95` plus a recipe to widen the bail set into
   "refresh `__heap` after every reassignment from `OSLICESTR(s, …)`"
   (~150 LOC alias logic; possibly out of "easy" range, but
   high-leverage — fixes 6+ benchmarks).

## Top deep ones (need design discussion)

1. **`reflect.Value` fat-iface tax** (Cluster B): every reflect
   benchmark, fmt.Scan, errors.As, encoding/binary slow path, and
   anything that calls into reflect from a hot loop is degraded
   ~3–6×. Tracking issue:
   `~/.claude/projects/-home-quentin-git-go/memory/project_fat_iface_alloc_regression.md`.
   Either revert reflect.Value to 16 B (and unpack inline-rep types
   into a heap-allocated shadow on demand) or land a fast-path family
   `Value.IndexInline / FieldInline` that the reflect-heavy stdlib
   callers can opt into.
2. **24-B SSO heap-rep header** (Clusters D, E): every long-string
   container (cache, slice, map value) is 1.5× wider than stock.
   Phase D (12-B header) is the rumored answer.
3. **`encoding/binary` reflect fallback** (Cluster B, single
   benchmark but 5× regression): the existing fast-path covers basic
   scalars + slices, not `[]Struct`. Adding a struct-of-fixed-size
   fast-path next to `encodeFast`
   (`src/encoding/binary/binary.go:491-`) would erase the
   AppendSlice1000Structs regression without touching reflect at all.
   ~200 LOC and a code generator.
