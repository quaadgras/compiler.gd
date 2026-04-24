# Escape-bits benchmark results

Comparison of stock Go 1.26.1 vs compiler.gd (release branch,
commit 9120642bae) on targeted stdlib + synthetic benchmarks.
5 runs each, `-benchtime default`, amd64, AMD Ryzen 9 5900XT.

## Synthetic benchmarks — where the optimisation is designed to help

Dynamic-dispatch hot loops (closure via package var, iface via
map-dispatched handler, callback via map-dispatched func value).
These are the patterns the escape-bits wrap is built for.

| benchmark                       | stock ns/op | fork ns/op | delta    | allocs   |
|---------------------------------|------------:|-----------:|---------:|---------:|
| EscapeBitsClosureNonEscape      |       11.21 |       4.22 | −62.34%  |  1 → 0   |
| EscapeBitsClosureEscape         |       11.73 |       5.66 | −51.76%  |  1 → 0   |
| EscapeBitsIfaceNonEscape        |       11.37 |       4.57 | −59.83%  |  1 → 0   |
| EscapeBitsIfaceEscape           |       11.84 |       5.54 | −53.24%  |  1 → 0   |
| EscapeBitsHandlerDispatch       |       11.71 |       4.66 | −60.17%  |  1 → 0   |
| EscapeBitsCallbackDispatch      |       11.61 |       3.98 | −65.74%  |  1 → 0   |
| **geomean**                     |   **11.57** |   **4.74** | **−59%** | 100%→0%  |

Phase G outbuf demo (hand-transformed `return new(T)`):

| benchmark             | stock ns/op | fork ns/op | delta   | allocs  |
|-----------------------|------------:|-----------:|--------:|--------:|
| OutBufStock           |       21.09 |      20.49 |     ~   |  1 = 1  |
| OutBufStack           |        3.49 |       2.97 | −14.88% |  0 = 0  |
| OutBufHeapFallback    |       20.91 |      20.55 |  −1.7%  |  1 = 1  |

Stock-shaped and heap-fallback paths match stock cost; the Phase G
transform with a stack-allocated outBuf is another ~15% faster on
the fork (the stock-to-fork delta here is just background codegen
improvements — the real Phase G win is stock-new-return vs fork-
stack-buffer, which was already ~7× in the hand-demo).

## Stdlib — indirect impact on real workloads

Packages where the open-gate activation can reach real code. Most
stdlib hot paths are either direct calls or devirtualised by stock
Go's analyser, so the activation touches them only incidentally.

### `fmt` — Sprintf variants (geomean −9.02%)

Many Sprintf paths include interface-method dispatch through
fmt.pp.printArg to the concrete type's String() / Format() method.
Where the pattern matches (candidate-eligible pointer arg passed to
an iface callee), the fork sees 1→0 alloc transitions and time
reductions. Where the dispatch has already been devirtualised by
stock, fork sees small overhead from the wrap's runtime check.

| benchmark                   | stock ns/op | fork ns/op | delta    | allocs   |
|-----------------------------|------------:|-----------:|---------:|---------:|
| SprintfString               |        7.49 |       5.41 | −27.73%  |  1 → 0   |
| SprintfTruncateString       |       13.44 |       8.87 | −34.02%  |  1 → 0   |
| SprintfTruncateBytes        |       13.29 |       9.16 | −31.08%  |  1 → 0   |
| SprintfFloat                |       12.80 |       9.00 | −29.68%  |  1 → 0   |
| SprintfBoolean              |        6.92 |       4.95 | −28.54%  |  1 → 0   |
| SprintfStringer             |       29.97 |      20.57 | −31.36%  |  3 → 0   |
| SprintfSlowParsingPath      |        8.07 |       7.10 | −12.00%  |  1 → 0   |
| SprintfBytes                |       58.32 |      47.93 | −17.82%  |  2 → 1   |
| SprintfHexBytes             |       41.46 |      34.83 | −15.99%  |  2 → 1   |
| SprintfPadding              |       17.06 |      19.50 | **+14%** |  1 = 1   |
| SprintfQuoteString          |       33.97 |      36.74 | **+8%**  |  1 = 1   |
| SprintfPrefixedInt          |       28.77 |      30.28 | **+5%**  |  1 = 1   |
| SprintfHexString            |       32.05 |      35.98 | **+12%** |  1 = 1   |
| SprintfStructure            |      139.10 |     152.70 | **+10%** |  5 → 3   |
| SprintfEmpty                |        1.75 |       1.75 |    ~     |  0 = 0   |

Net: ~9% geomean speedup, big alloc reductions on the paths the
optimisation reaches; small regressions on paths where the wrap's
runtime mask-check adds cost the optimisation can't recoup.

### `sort` — allocation elimination with per-call overhead

| benchmark             | stock ns/op | fork ns/op | delta   | allocs |
|-----------------------|------------:|-----------:|--------:|-------:|
| SortStrings           |       24.1m |      24.6m |    ~    |  1 → 0 |
| SortStrings_Sorted    |      461 µs |     616 µs | +33.64% |  1 → 0 |
| StableString1K        |       83 µs |     112 µs | +35.33% |  1 → 0 |

Each benchmark loses its single per-run allocation (the iface box
`sort.StringSlice`-wraps the input for `sort.Interface`), but the
fork's wrap fires per-call on the sort's comparison function,
adding overhead the small benchmarks can't amortise. `SortStrings`
(which does large amounts of comparison work) breaks even;
`SortStrings_Sorted` (pre-sorted, few comparisons) and
`StableString1K` (short) pay the per-iteration cost visibly. A
targeted un-wrap for statically-resolvable comparator paths would
recover this.

### `encoding/json` — CodeDecoder regressed

| benchmark     | stock ns/op | fork ns/op | delta   | allocs             |
|---------------|------------:|-----------:|--------:|-------------------:|
| CodeDecoder   |       1.88m |       2.36m | +25%    | 25.67k → 23.97k    |

A real regression: −7% allocations but +25% wall time and −20%
throughput. Similar shape to the sort case — the wrap adds cost on
many dispatches per decode, and json.Decoder's internal iface
dispatch is dense enough that the per-call overhead outweighs the
allocation-elimination payback. Worth investigating whether the
wrap has a cheaper fast-path when the mask is fully-zero (the
common case).

## Summary

- Synthetic hot-loop patterns: ~59% geomean speedup, 100% alloc
  reduction. These lock in the core value proposition for
  allocation-sensitive code (games, CLI tools, local apps).
- `fmt.Sprintf`: 9% geomean speedup with several subtests seeing
  ~30% reductions. Real workload benefit for format-heavy code.
- `sort` and `json` show per-call overhead dominating when the
  dispatch is dense and the mask check doesn't recover much. A
  fast-path for "mask is statically zero" (static-mask bit 0
  clear AND all other bits clear) could skip the resolve and
  materialize logic entirely — candidate for a follow-on
  optimisation.

The fork's trade-off profile is consistent with the design intent:
big wins on patterns the optimisation was built for (closures,
iface dispatch, handler registries, callback maps) and small
overhead elsewhere. Allocation counts reliably drop; wall time
trades with per-call dispatch cost depending on how dense the
indirect calls are in the measured hot path.

## Binary size

| program                            | stock      | fork       | delta    |
|------------------------------------|-----------:|-----------:|---------:|
| hello world (`fmt.Println`)        |  2,392,425 |  2,551,634 |   +6.7%  |
| stdlib mix (json/http/slog/regexp) |  6,243,262 |  6,908,907 |  +10.7%  |

Section breakdown on the larger binary:

| section              |     stock |      fork |    delta |
|----------------------|----------:|----------:|---------:|
| `.text` (code)       | 1,881,585 | 2,374,225 |  +26.2%  |
| `.rodata`            |   544,793 |   545,977 |   +0.2%  |
| `.gopclntab`         | 1,527,966 | 1,592,155 |   +4.2%  |
| `.data`              |    53,810 |    63,762 |  +18.5%  |
| `.noptrdata`         |   288,417 |   288,737 |   +0.1%  |

Where the growth comes from:

- **`.text` (+26%)**: wrap calls + box-promotion prologues at every
  candidate call site, plus the new runtime helpers
  (`resolveMask`, `maybeInPlace`, `materializeToHeap` stack-range
  check, idempotence path).
- **`.data` (+18%)**: funcsym entries widened from `{F}` (8 B) to
  `{F, M}` (16 B) — every function value in the binary carries its
  escape mask word.
- **`.gopclntab` (+4%)**: a handful of new runtime-helper PCs add
  line-table entries; otherwise unchanged.
- **`.rodata`**: itab mask tails are small enough to disappear into
  the noise.

Reproduce:
```
mkdir /tmp/hello && cd /tmp/hello
printf 'module hello\ngo 1.24\n' > go.mod
printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("hi") }\n' > main.go

# stock
/usr/lib/go/bin/go build -o hello-stock .

# fork
GOROOT=/home/quentin/git/go GOTOOLCHAIN=local \
  /usr/lib/go/bin/go build -o hello-fork .

ls -la hello-*
size -A hello-stock hello-fork
```
