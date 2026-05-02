# Remaining allocations: where they come from and which are eliminable

Snapshot 2026-05-02 05:17 (`src/gd.txt`); 7,758 benchmarks compared. The
fork's allocation distribution:

- 6,281 benchmarks at 0 allocs/op (already optimal in gd)
- 1,477 benchmarks with >0 allocs/op
  - 591 at exactly 1 alloc/op
  - 149 at exactly 2 alloc/op
  - 79  at exactly 3 alloc/op
  - 206 at 4-10 alloc/op
  - 450 at >10 alloc/op (per-iteration scaling tests)

Top per-package absolute allocation totals come from input-scaled benches
(json's TypeFieldsCache, archive/zip ManyShallowFiles, text/tabwriter); the
"per call" allocations of interest live mostly in the 1-3 alloc/op tier.

## Recurring patterns

### Pattern 1: 24-B SSO heap-rep header inflates strings stored in maps/slices

The fork's heap-rep `string` is 24 B (vs 16 B stock). Any container of
strings — map values, slice elements, struct fields — pays the 1.5x bytes
tax. Where strings are short-lived (function-local), this rarely costs
extra allocs; where they are stored in long-lived structures, B/op rises
proportionally with no compensating cycle gain.

| benchmark | gd (a, B) | site |
|---|---|---|
| `archive/zip.BenchmarkReaderManyShallowFiles` | 932082, 132 MB | `src/archive/zip/reader.go:387,389` (`f.Name = string(d[…])`, `f.Comment = string(d[…])`); 310 k `*File` records each carry two SSO headers + 3 maps keyed by string in `initFileList` (`reader.go:814-819`) |
| `encoding/json.BenchmarkTypeFieldsCache/MissTypes100000` | 1,438,768, 117 MB | reflect-driven cache; every cached `field` record holds string keys at 24 B each |
| `errors.BenchmarkAs` | 3 (vs stock 2), 120 B (vs 40) | `src/errors/wrap.go:124` — `target.Elem().Set(reflectlite.ValueOf(err))` boxes the `*errorT.s string` SSO header into a 32-B fat-iface |
| `net/url.BenchmarkEncodeQuery/oe=utf8&q=puppies` | 4 (vs 3), 104 B (vs 56) | `src/net/url/url.go:1009-1016` — each `QueryEscape(k)` returns a heap-rep string copied through `buf.WriteString`'s `iface` arg |
| `crypto/tls.BenchmarkThroughput/DynamicPacket/16MB/TLSv13` | 2,464, 419 K B (+11.1% B vs stock at near-equal allocs) | per-record string descriptors and labels; SSO header tax across the connection lifecycle |

Root cause: heap-rep header is 24 B in the fork. Eliminable only via SSO
Phase D (12-B header) or per-call-site refactors that avoid storing the
string. See `doc/gd/sso-string.md`.

### Pattern 2: `string(buf)` cast on a function-local `[]byte` builder

The compiler can elide the conversion when it proves `buf` doesn't escape
and isn't aliased. When it cannot (return value, hand-built byte slice
that the function then converts at the end), `slicebytetostring` runs and
allocates the heap-rep header for any result >15 B.

| benchmark | gd (a, B) | site |
|---|---|---|
| `os.BenchmarkExpand/multiple` | 2, 48 B | `src/os/env.go:44` — `return string(buf) + s[i:]` (string+string concat) |
| `net.BenchmarkIPMaskString` | 2, 64 B | `src/net/ip.go:323` — `func hexString(b []byte) string { … return string(s) }` (the `make([]byte, …)` + the conversion are both heap allocs) |
| `strconv.BenchmarkQuote` | 2, 112 B | `src/strconv/quote.go:24` — `return string(appendQuotedWith(make(...), …))` |
| `strconv.BenchmarkUnquoteHard` | 2, 192 B | `src/strconv/quote.go:557` — same `string(buf)` pattern at the end of `Unquote` |
| `mime.BenchmarkQEncodeWord` | 2, 80 B | `src/mime/encodedword.go` — `string(buf.Bytes())` (heap conversion) |
| `text/template/parse.BenchmarkVariableString` | 3, 72 B | `src/text/template/parse/node.go:406` — `sb.String()` returns the SSO heap-rep wrapper of `sb.buf`; the prior `sb.buf = append(...)` grows once → 2 allocs from the builder + 1 from boxing the result |

Root cause: `string([]byte)` lowers to `slicebytetostring`, which always
heap-allocates >15 bytes. The compiler's escape analysis already handles
the no-mutation case, but not the "buf was just built" case. A
peephole that recognises `return string(buf)` at the end of a function
where `buf` provably has no other live reference (and rewrites it to
`unsafe.String(unsafe.SliceData(buf), len(buf))`) would eliminate one
alloc per call. The trickier case is when `buf` was made via `make` in
the same scope and the result heads straight into a `string` — the
runtime path could detect this via the existing `tmpBuf` mechanism if
the compiler hands it the buffer pointer (already wired for some sites).

### Pattern 3: error struct boxed via interface return

Functions that return `error` from a custom struct type pay 2 allocs per
error path: 1 for the struct itself, 1 for any embedded heap-rep string.
For `*ParseError`-style types where the only payload is a constant
message + an input string, both can often collapse to a package-level
sentinel (no alloc) when the input string isn't echoed.

| benchmark | gd (a, B) | site |
|---|---|---|
| `time.BenchmarkParseDurationError` | 2, 96 B | `src/time/format.go:1650, 1662, 1668, 1682, 1694, 1700, 1704, 1713` — every `&parseDurationError{...}` is `1 alloc + the value string` |
| `strconv.BenchmarkUnquoteHard` (within) | 2, 192 B | `src/strconv/quote.go` — `*NumError`-style struct |
| `encoding/json.BenchmarkUnmarshalNumber` | 2, 200 B | `src/encoding/json/decode.go` — `*UnmarshalTypeError` with embedded type/value strings |
| `mime.BenchmarkExtensionsByType/text/html;_charset=utf-8` | 3, 560 B | `src/mime/type.go` — multi-segment parse builds `*url.Error`-like struct + sub-strings |

Root cause: each struct boxed into the `error` interface heap-allocates;
embedded heap-rep strings (24 B each) drive B/op up. Many of these
structs include the original input as a context field, even on simple
errors. Refactoring the hottest ones to return package-level sentinels
on the common path (e.g. `errInvalidDuration`) would zero the alloc when
the caller only needs the kind, not the value. Phase G escape-bits
(`project_escape_bits_phase_g_roadmap.md`) addresses this generally for
any non-escaping error: the key is identifying which call sites benefit.

### Pattern 4: builder allocations dominate short string builds

`strings.Builder` and `bytes.Buffer` allocate the underlying `[]byte`
the first time they grow past zero, then a 24-B SSO header for
`.String()`. Short, single-write builds pay 2 allocs even when the
result fits in 15 B (where SSO inline-rep would otherwise be free) —
because the Builder path goes through `unsafe.String` of the (now
heap-allocated) buf, never through `inlineStringFromBytes`.

| benchmark | gd (a, B) | site |
|---|---|---|
| `strings.BenchmarkBuildString_Builder/1Write_NoGrow` | 1, 48 B | `src/strings/builder.go:46-48` — `unsafe.String` of `b.buf`; the buf grow is the alloc |
| `strings.BenchmarkBuildString_ByteBuffer/1Write_NoGrow` | 2, 112 B | `src/bytes/buffer.go` — `bytes.Buffer.String()` does `string(b.buf[…])` (heap copy + header) |
| `strings.BenchmarkBuildString_WriteString/3Write_NoGrow` | 3, 336 B | one alloc per grow + final header |
| `text/template/parse.BenchmarkVariableString` | 3, 72 B | Builder buf alloc + grow + final header |

Root cause: SSO inline-rep can't be produced by `Builder.String()`
because the `[]byte` buf is already heap-allocated (the SSO inline-rep
would require zero-allocation construction from a stack array). A fast
path in `(*Builder).String()` that returns the inline-rep when
`len(b.buf) <= 15` would erase 1 alloc on small builds, but not 2 (the
buf alloc itself). Bigger win is in `bytes.Buffer.String()`, which
currently does an extra copy via `string(b.buf[b.off:])` — switching to
`unsafe.String(&b.buf[b.off], len(b.buf)-b.off)` would drop the
duplicate alloc and put `Buffer` on par with `Builder`.

## Per-package allocation hotspots

Sorted by sum of medians across benchmarks in the package; "verdict"
distinguishes per-iter-scaled work from refactor-removable.

| pkg | top benches | site | verdict |
|---|---|---|---|
| `encoding/json` (16 M sum) | `TypeFieldsCache/MissTypes100000` (1.4 M alloc), `CodeUnmarshal` (38 k) | reflect-driven, `src/encoding/json/encode.go:typeFields` cache | mostly **unavoidable** (scales with input); +22% B vs stock is SSO header tax (Pattern 1) |
| `archive/zip` (993 k) | `BenchmarkReaderManyShallowFiles` (932 k) | `src/archive/zip/reader.go:387, 814` | **unavoidable per file** (each entry needs Name string); SSO header is the tax — Pattern 1 |
| `text/tabwriter` (335 k) | `BenchmarkTable/100x100000/new` (100 k) | `src/text/tabwriter/tabwriter.go:115, 119` (`b.lines` growth + per-cell append) | **unavoidable** for the test design, but +5-15% ns vs stock points to Pattern 1 (per-cell string ops) |
| `database/sql` (92 k) | `BenchmarkConcurrentTxStmtQuery` (17 k) | per-row `Scan` → `*string` | **unavoidable** (per-row), but B/op is heap-rep header tax |
| `go/constant` (87 k) | `BenchmarkStringAdd/65536` (65 k) | `src/go/constant/value.go:stringVal{l, r *Value}` repeated string concat | partially **eliminable**: `+` on `Value` flatly allocates; rope-flatten happens in `Render` only (`value.go:130-160`). A per-call rope cap would amortise. |
| `text/template/parse` (80 k) | `BenchmarkParseLarge` (80 k) | `src/text/template/parse/parse.go:Tree.parse` builds nodes | **unavoidable** (every Node is heap) |
| `crypto/tls` (78 k) | throughput benches (3-7 k) | per-record state machines | mostly **unavoidable**, but B/op +10-30% vs stock is SSO header in cipher state structs (Pattern 1) |
| `net/http` (34 k) | `BenchmarkFileAndServer_64MB/h2` (22 k) | h2 frame parsing | partially **eliminable**: many string-from-bytes paths in `src/net/http/h2_bundle.go` (header decode) — Pattern 2 |
| `go/parser` (32 k) | `BenchmarkParse` (16 k) | `src/go/parser/parser.go:Tree.next` | **unavoidable** (per-token Node allocs) |
| `runtime/pprof` (29 k) | `BenchmarkGoroutine/Profile.WriteTo_idle_5000` (25 k) | `src/runtime/pprof/proto.go:newProfileBuilder` per-stack | partially **eliminable**: per-frame string lookups; Phase G could elide them when the caller is the writer itself |
| `math/big` (21 k) | `BenchmarkHilbert` (15 k) | `*Int.Mul/Div` allocate intermediate `nat` | **unavoidable** for the algorithm (math operations require fresh storage) |
| `time` (16 k) | `BenchmarkAdjustTimers10000` (10 k) | per-`AfterFunc` Timer + closure | **unavoidable** by test design |
| `crypto/rsa` (5.6 k) | `BenchmarkGenerateKey/2048` (5.3 k) | math/big intermediates | **unavoidable** |

## Easy-elimination candidates (1-3 alloc/op, plausible <30 LOC fixes)

Each entry: bench → file:line → why it allocates → minimal fix sketch.
Ordered by leverage (benchmark count × confidence the fix doesn't break
anything).

1. **`net.BenchmarkIPMaskString`** — 2 → 1 alloc. `src/net/ip.go:319-323`.
   `make([]byte, …)` then `string(s)` does both allocs; the second is
   the redundant heap copy. Replace `return string(s)` with
   `return unsafe.String(unsafe.SliceData(s), len(s))`. ~3 LOC + import.
   Saves 1 alloc/call.

   **CAUTION — empirically tested 2026-05-03**: applying this rewrite
   to `hexString` *regressed* `BenchmarkIPMaskString` to 4 allocs / 80 B
   (vs baseline 2 allocs / 64 B), even though a stand-alone `noinline`
   version of the same code shows the expected 1-alloc win when called
   directly. The regression appears only when `hexString` is inlined
   into `IPMask.String`. Escape-analysis output (`-gcflags=-m=2`) shows
   one heap alloc for the `make` in either version; the extra two
   allocs and 16 B per-call delta isn't visible in `-m`-style escape
   reports. This is suggests something specific to the fork's
   `OUNSAFESTRING`/SSO-string lowering interacting with the inliner.
   **Before applying the unsafe.String swap to any of the candidates
   below, validate empirically with `-benchmem` and `-count=3`** —
   the call-site context can make the rewrite a regression rather than
   a win. The other candidates (strconv.Quote, os.Expand, etc.) likely
   have the same risk.

2. **`strconv.BenchmarkQuote, BenchmarkUnquoteHard`** —
   `src/strconv/quote.go:24, 28`. Same pattern: `string(appendXxxWith(
   make(…), …))`. Two allocs (make + cast) → one (just make), via
   `unsafe.String`. ~6 LOC across 2 sites. Saves 1 alloc each on
   `Quote`/`QuoteRune`/`QuoteRuneToASCII`/`AppendQuoteRune`/`Unquote`.

3. **`net/url.BenchmarkPathEscape`** (already 0 alloc — verify the
   pattern carries over). Confirm `(*URL).EscapedPath` has the same
   `unsafe.String` shape; if not, apply.

4. **`os.BenchmarkExpand/multiple`** — `src/os/env.go:44`. Two allocs:
   `string(buf) + s[i:]` is a string-string concat. Refactor to
   `buf = append(buf, s[i:]...); return unsafe.String(unsafe.SliceData(buf), len(buf))`.
   ~4 LOC. Saves 1 alloc on every Expand that hits the second branch.

5. **`net/url.BenchmarkResolvePath`** — 2 → 1 alloc.
   `src/net/url/url.go:1030`: `full = base[:i+1] + ref` is a concat
   producing a heap-rep header even though the result is then iterated
   character-by-character. Pre-allocate one buffer and avoid the
   intermediate string. ~10 LOC.

6. **`strings.BenchmarkBuildString_ByteBuffer/*`** — `bytes.Buffer.String()`
   calls `string(b.buf[b.off:])` (`src/bytes/buffer.go:73`) which heap
   copies. Switch to `unsafe.String(&b.buf[b.off], len(b.buf)-b.off)`.
   ~3 LOC. Affects every `Buffer.String()` caller. Note: the API doc
   says callers may still mutate the Buffer afterwards — but the same
   note already applies to `strings.Builder.String()` in the fork, so
   the precedent exists.

7. **`time.BenchmarkParseDurationError`** — 2 → 1 alloc, possibly 0.
   `src/time/format.go:1650-1714` (8 sites). Each `&parseDurationError{
   "kind", orig}` is a fresh struct + the orig string (which already
   exists). Most of the kinds are constant; group them as 6 package-level
   sentinels (`errBadDuration`, `errMissingUnit`, etc.) and only allocate
   when the caller actually needs `orig` in the message. ~30 LOC to
   convert. Saves 1-2 allocs on every error path.

8. **`go/constant.BenchmarkStringAdd/65536`** — high alloc count
   (65 k for size 64 k). `src/go/constant/value.go:stringVal` is a
   rope; every `+` creates a new node. Adding a small-string fast path
   that flattens when `len(l.s) + len(r.s) <= 64` would amortise.
   ~20 LOC. Affects all `go/constant` benches at the size.

9. **`mime.BenchmarkQEncodeWord, BenchmarkQDecodeWord`** —
   `src/mime/encodedword.go`. `string(buf)` at end of build-up. Apply
   `unsafe.String` (Pattern 2 fix). ~5 LOC.

10. **`text/template/parse.BenchmarkVariableString`** — 3 → 2 alloc.
    `src/text/template/parse/node.go:406`. `sb.String()` is fine; the
    issue is the Builder buf grow happens twice for this input (5
    idents joined with '.' = 47 chars, hits `Builder.grow` twice). A
    `sb.Grow(64)` at the start of `writeTo` (or pre-summing
    `len(v.Ident)` plus separators) drops one alloc. ~5 LOC.

11. **`encoding/binary.BenchmarkWriteFloats, BenchmarkReadFloats`** — 2
    alloc each. `src/encoding/binary/binary.go:414, 429`. `Write` does
    `make([]byte, n)` → `w.Write(bs)` for each call. For the fast-path
    case (basic types), a per-goroutine pooled buffer (or sync.Pool)
    would amortise. ~30 LOC. Saves 1 alloc on every fast-path
    `Write`/`Read`.

12. **`fmt.BenchmarkSprintfStructure`** — 3 alloc, `src/fmt/print.go:240`.
    `s := string(p.buf)` heap-copies; `p.buf` pool-managed. Compiler
    cannot rewrite (p outlives `s`'s buf reference) but a runtime
    fast-path that detects "p.buf is from pool, transfer ownership"
    would avoid the copy. ~50 LOC, deeper change.

13. **`html/template.BenchmarkStripTagsNoSpecials`** — 2 alloc.
    Checking `src/html/template/html.go` (stripTags): allocates a
    `[]byte` then `string(out)`. Pattern 2 fix. ~3 LOC.

14. **`bufio.BenchmarkWriterCopyUnoptimal, BenchmarkReaderCopyUnoptimal`**
    — 2 alloc. `src/bufio/bufio.go:Writer.ReadFrom` allocates a
    fallback buffer when the underlying reader doesn't expose `WriteTo`.
    Pool the buffer. ~15 LOC.

15. **`encoding/json.BenchmarkIssue34127`** — 2 alloc, 32 B. `Marshal`
    on a 1-field struct allocates the result `[]byte` + the SSO header
    on the returned `string`-cast. Returning a pre-sized buffer from a
    sync.Pool when the encoder state allows would drop 1. ~20 LOC.

## Open puzzle: `unsafe.String` of a freshly-built `[]byte` doubles allocs when inlined

The "Pattern 2" rewrite (replace `return string(buf)` with
`return unsafe.String(unsafe.SliceData(buf), len(buf))`) is the first
recommended fix in the table above and is the right shape under stock Go.
Tested empirically against `net.hexString` (`src/net/ip.go:318-324`),
which is inlined into `IPMask.String`:

| variant | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| baseline `string(s)` (inlined) | 100 | 64 | 2 |
| `unsafe.String(SliceData(s), len(s))` (inlined) | 98 | 80 | 4 |
| `unsafe.String` standalone (`//go:noinline`, AllocsPerRun) | — | — | 1 |

The standalone form behaves as expected (one heap alloc for the `make`).
Inlined into the caller, two extra allocs and 16 B/call appear with no
hint in `-gcflags=-m=2` escape diagnostics. Plausible suspects:

- The fork's `walkUnsafeString` constructs an `OSTRINGHEADER`; the SSA
  lowering of that op for SSO heap-rep may be emitting a runtime helper
  call (e.g. a defensive copy) when the input pointer flows through an
  inlined boundary.
- Escape solver's treatment of the `OUNSAFESTRING` arg-flow
  (`escape/expr.go:155, escape/call.go:262`) may pessimistically heap
  the byte-pointer when escape into a result is bridged through
  inlining.

This needs investigation before any of the Pattern-2 candidates above
can be safely landed. Single-call validation with
`testing.AllocsPerRun` is *not* sufficient — the alloc penalty only
shows up under realistic call-site inlining. Recommended next step:
SSA-dump `IPMask.String` before/after the rewrite and identify which
runtime call appears with the unsafe.String form.

## Structural barriers (need design-level work, not in this report's scope)

- **24-B SSO heap-rep header** is the dominant ~1.5x B/op tax across all
  long-string-storing benches (`encoding/json.TypeFieldsCache`,
  `archive/zip.BenchmarkReader*`, `crypto/tls.BenchmarkThroughput*`,
  `database/sql` row scans). Phase D (12-B header) is the rumored
  answer; until then this can only be amortised at call sites that don't
  store strings.

- **32-B fat-iface boxing of strings** adds 1 alloc per
  `interface{}`-receiving call when the value is a heap-rep string;
  surfaces in `errors.As` (+1 alloc), `net/url.EncodeQuery` (+1), every
  `fmt.Print/Sprintf("%s", longString)` path. Phase G escape-bits is
  the right hook but needs eligibility tightening.

- **`reflect.Value` 32-B layout** drives the `BenchmarkDeepEqual/*`,
  `BenchmarkIsZero/*`, `encoding/binary` slow-path, `errors.As`
  regressions. Tracked at
  `~/.claude/projects/-home-quentin-git-go/memory/project_fat_iface_alloc_regression.md`.

- **`loopheapify` OSLICESTR bail** (`cmd/compile/internal/loopheapify/
  loopheapify.go:43-44`) keeps `s = s[k:]` byte-scan loops slow but
  doesn't add allocs — it's a ns regression, not an alloc one. Already
  covered in `remaining-regressions-2026-05-03.md`.

## Recommended next-step fixes, ranked

| # | fix | LOC | benches affected | est. impact |
|---|---|---:|---:|---|
| 1 | `bytes.Buffer.String()` use `unsafe.String` (already done in `strings.Builder`) | 3 | ~25 (any `BuildString_ByteBuffer/*`, mime, html/template, etc.) | 1 alloc/call |
| 2 | Build-up + `string(buf)` peephole in compiler (covers `strconv.Quote`, `os.Expand`, `net.IPMask`, `mime.QEncode`, etc.) | 50-150 | ~30 1-3-alloc benches in Pattern 2 | 1 alloc/call |
| 3 | `time.parseDurationError` → package-level sentinels for the constant-message branches | 30 | `time.BenchmarkParseDurationError` (and ad-hoc callers); precedent template for other `*Error` types | 1-2 alloc/call |
| 4 | `encoding/binary.Write/Read` pooled fast-path scratch buffer | 30 | `BenchmarkWriteFloats`, `BenchmarkReadFloats`, plus all 2-alloc fast-path sites in the same file | 1 alloc/call |
| 5 | `(*Builder).String()` SSO inline-rep fast path when `len(b.buf) ≤ 15` | 10 | small-result Builder users; modest win, but easy and demo-quality | 1 alloc on small results |

Items 1 and 2 are the highest-leverage compiler/library refactors;
items 3-5 are surgical and each touches a single file. None requires
the SSO Phase D header shrink or fat-iface redesign.
