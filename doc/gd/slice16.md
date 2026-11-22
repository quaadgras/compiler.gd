# Slices of the gd fork: two words

With 8-byte words (but on GOARCH=wasm) a slice is two words, 16 bytes,
so that it can be loaded and stored with one 16-byte access: a racing
program may load a stale slice, never one made of two (a pointer of one
and a length or capacity of another), which would not be memory safe
(`doc/gd/sharing.md`). With 4-byte words (386, arm, mips) and on wasm a
slice is stock Go's three words.

`types.SlicePacked` (`cmd/compile/internal/types`), `abi.SlicePacked`
(`internal/abi`) and the linker's `slicePacked` decide it.

## 1. Layout

```
compact:  w0 @0   the address of the array
          w1 @8   len (uint32 @8) and cap (uint32 @12)
huge:     w0 @0   the address of a header {ptr, len, cap} (three words)
          w1 @8   the marker: len field 0, cap field 0xFFFFFFFF
```

- A slice whose capacity fits in 32 bits but is not 0xFFFFFFFF
  (`cap < types.SliceHugeCap = 2^32-1`) is compact: its pointer, its
  length and its capacity are in its words. Its length is at most its
  capacity, so it fits too.
- Any other slice is **huge**: w0 is the address of an immutable header in
  the heap (`runtime.slicehdr` makes one, a `runtime.slice`), and w1 is
  the marker, which no compact slice has (its capacity field would be
  0xFFFFFFFF). Each store of a huge slice (or pass of one in registers)
  makes a new header; nothing writes a header after it is published.
- The fields are in bytes, not in a word's bits: len is at offset 8 and
  cap at 12 on any byte order. As a uint64, w1 is `len | cap<<32`
  little-endian and `len<<32 | cap` big-endian.
- The empty slice and nil are compact: `(nil, 0)` is nil.

### Why the marker is in w1, and why it is safe

- **The collector keeps the header alive.** w0 is the pointer word of
  every type that holds a slice, as before; a huge slice's w0 is a plain
  pointer to its header, a heap object of type `runtime.slice`, whose
  first word (the array) is a pointer. So the header is marked through
  w0, and the array through the header, with no special case anywhere in
  the collector: scanning, the write barrier (w0 is stored with the
  barrier of a pointer, the header's address), stack copying, checkptr
  and cgocheck all see a heap pointer. A tag in w0's top bit (as inline
  strings have, `sso-string.md`) would have made the collector ignore
  the header; a tag in w0's low bits is not possible, a `[]byte`'s array
  has any alignment.
- **The header is published before its address.** `slicehdr` writes the
  header and then has a publication barrier; a goroutine that loads
  (header address, marker) reads the header through the address, a
  dependent load. A header is never written again.
- **A huge slice decoded as a compact one is empty.** Its length field is
  0: code that reads w1's length without testing for the marker (an
  assembly function, C through cgo, a stale reader of the bytes) sees
  `len == 0`, and indexes nothing through w0, which is not an array. The
  capacity field is 0xFFFFFFFF, so nothing reads it as a capacity
  without the test: compiled code never does (§3), and what cap-checks a
  slice in assembly takes its words from Go (`internal/bytealg`).
- **A compact slice is never decoded as huge**: its capacity is below
  0xFFFFFFFF. Packing tests `cap < 0xFFFFFFFF`, unpacking `cap field ==
  0xFFFFFFFF`.
- The runtime's own slices are never huge, so the paths that must not
  allocate (no write barriers, the signal handler, the allocator) never
  make a header: `slicehdr` throws if it is called on the system stack,
  the signal stack or while allocating. The compiler's check of the
  functions with no write barrier (`ssagen/nowb.go`) does not follow the
  calls to `slicehdr`. A slice of a type that is not in the heap is
  stored without a write barrier and has no huge form
  (`runtime.slicehdrnotinheap` throws); `runtime.noWBSlice` lets
  runtime code that must not have write barriers store a slice it
  reslices (its array stays) as one of those.

## 2. In SSA: three values, packed in memory

In SSA a slice is `SliceMake(ptr, len, cap)` as in stock Go, the length
and capacity full words, so that bounds checks, `prove`'s facts and the
rules about slices are those of stock Go. It is packed where it goes to
memory or to registers, and unpacked where it comes from them
(`ssa/slicecodec.go`):

- `SlicePack(ptr, len, cap, mem) → (w0, mem)` and
  `SlicePackW1(len, cap, w0) → w1`;
- `SliceUnpackPtr/Len/Cap(w0, w1, mem)`.

They come from the decomposition of loads and stores of slices
(`dec.rules`), from the calls (`expand_calls.go`: a slice is passed and
returned in two integer registers, w0 and w1, and on ABIInternal's
stack as in memory; ABI0's frames have stock Go's three words, §5a),
from interfaces (a slice is spread in an interface as in memory: w0 in
the data word, w1 in the first half of the payload, the second zero, as
a string is) and from `ssagen`. Rules fold an unpack of a pack
(a slice stored and loaded back), and the w0 and w1 of a pack of an
unpack.

The **slice codec** pass (after `generic deadcode`, before the write
barriers) expands them, splitting the block:

```
unpack:   if w1>>32 == 0xFFFFFFFF { ptr, len, cap = w0[0], w0[1], w0[2] }    // unlikely
          else                    { ptr, len, cap = w0, uint32(w1), w1>>32 }
pack:     if cap < 0xFFFFFFFF     { w0, w1 = ptr, len | cap<<32 }            // likely
          else                    { w0, w1 = runtime.slicehdr(ptr, len, cap), marker }
```

(big-endian: the halves of w1 swapped). The marker test is the load's,
not each use's: the branch of an unpack (unlikely, so predicted) serves
every index, `len`, `cap`, slice expression and append of the loaded
slice, which then work with its real pointer, length and capacity: a
slice expression of a huge slice adds its offsets to the array's
address, never to the header's. (An index alone would not need the
test: a huge slice's length field is 0, so `i <u len` fails and could
reach a slow path that decodes the header. Not testing loads that are
only indexed is an open item, §7.) The pass first drops the packs that
need no branch (a capacity that is a constant that fits, or the slice
unpacked from words w0 and w1, which are its words) and the unpacks that
leaves dead, so that a slice that is passed on, or loaded and stored, is
copied as its words. The unpacks of the same words share one branch; a pack of w1 with no pack of w0 next to it is expanded
without a branch. The packs of a call's arguments come before the stores
of its stack arguments (`hoistSlicePacks`): `slicehdr`'s call would write
over them.

`runtime.growslice` and its variants, and `moveSlice`, return the
pointer, the length and the capacity, not a slice. `s = s[:n]` and an
append in place store the whole slice (its length is in a word with its
capacity).

## 3. Where slices are made and read by hand

- `runtime.slice` is the three words (a header, `growslice`'s result);
  `internal/unsafeheader.Slice` too, with `ReadSlice` and `WriteSlice`
  for a slice in memory; `reflect.SliceHeader` is stock Go's, and a
  program that casts a slice's address to it is wrong with 8-byte words.
- The runtime's and reflect's casts of a slice's address to a header are
  gone: they use the language (`unsafe.Slice`, `unsafe.SliceData`, `len`,
  `cap`), `unsafeheader.ReadSlice`/`WriteSlice`, or for slices backed by
  memory not in the heap `mkNotInHeapSlice` and `noWBSlice`.
- Static data (`staticdata.InitSlice`, `rttype.WriteSlice`, `//go:embed`),
  the linker (`moduledata`, the inittasks: `addSliceRest`; the type
  descriptors it decodes) and DWARF (`runtime.sliceDWARF`) write and read
  the packed form; `runtime-gdb.py` decodes a huge slice.
- Assembly: a slice that is an argument or a result of its own is
  stock Go's three words in an ABI0 frame, `x_base+0`, `x_len+8` and
  `x_cap+16`, 24 bytes aligned to a word, a huge slice too: its real
  pointer, length and capacity (`abi.ABIConfig.StockSlices`, §5a).
  So the assembly is stock Go's, and so is vet's asmdecl for arguments.
  A slice in a struct or an array argument is as in memory; no assembly
  takes one. The functions of `internal/bytealg` that take slices are
  ABIInternal (registers) on most targets: they take the words from Go
  wrappers (`compareSlices`, `countSlice`, `indexSlices`,
  `indexByteSlice`: the pointer, the length and the capacity, the stock
  slice's registers), which are inlined.
- No assembly reads a slice in memory (a field or a global) by its
  offsets: the search for `_len`/`_cap` and slice fields of `go_asm.h`
  outside frames finds none.
- cgo: `GoSlice` is stock Go's `{void *data; GoInt len; GoInt cap;}`.
  An exported function's Go wrapper gets its slice arguments as that
  and makes the slices (`unsafe.Slice`), and lays out its slice results
  so (`cmd/cgo`): C sees a huge slice's real length. C cannot declare a
  Go slice parameter of an imported function. `C.CString` and `C.CBytes`
  make their slices of C memory from array pointers. SWIG's
  `_goslice_`, which SWIG's Go code reads from a slice in memory, is
  rewritten to the packed layout (`cmd/go`; a huge slice is empty there).

## 5a. ABI0: stock slices

ABI0, the assembly's calling convention (and on the targets without
register arguments, s390x and mips64x, Go's own), lays out a slice
argument or result of its own as stock Go does. The compiler converts at
the boundary, where a slice is in SSA three values anyway
(`expand_calls`: a call's slice arguments are stored as their three
words, its results made of them; a pack or an unpack happens only where
the slice goes to or comes from memory or registers). A huge result
becomes a header in the Go caller or the ABI wrapper when it is stored,
never in assembly. Where a function with ABI0 has its body in Go
(s390x and mips64x, the ABI wrappers, `go:cgo_unsafe_args`) and a slice
parameter's address is taken, the parameter is laid out as in memory in
its own 24-byte slot at entry; such a result is laid out again in stock
Go's words at return (`rewriteFuncResults`). reflect's frames on those
targets convert their slices (`reflect.stockSlice`), and so do the
traceback's argument maps (`EmitArgInfo`). No assembly function returns
a slice.

Against ABI0 frames of packed slices (amd64, best of 6 over 152
benchmarks, the asm-heavy hash/crc32, crypto/sha256, crypto/cipher and
math/big among them): a geomean of +0.1%, +1.2% for those four
packages. A call of assembly on a small input is up to 0.7 ns slower
(math/big's `AddVV` of one word +13%, crc32 IEEE of 15 bytes +8%): each
slice argument loaded from memory is unpacked (its marker tested) and
its three words stored, where a packed frame took its two words as they
were. Large inputs are unchanged, and so is cmd/gofmt's text.

## 4. Alignment

A slice is aligned to 16 where it is packed (`types.SliceAligned16`), as
a string is (`sso-string.md` §6), and for the same reasons: a slice in
the heap, the data or the bss is one aligned 16-byte block. The stack
aligns it to a word (`types.StackAlignment`). go/types and types2
(`gcSizes.slicePacked`), cgo, the linker (`sliceAlign`) and the runtime's
type descriptors (`abi.RTypeSize`) agree.

## 5. Loaded and stored whole

Aligned to 16, a slice is guarded as a string is (`sharing.md`, "the
slices too"), by default, everywhere but in the stack (`types.Has16`,
`HasWhole`):

- A store packs the slice first (a huge one's header is made then,
  before any lock is taken), then stores its words with one 16-byte store
  (`StringStore16`, with the write barrier of w0) where a 16-byte access
  is atomic (amd64 with AVX, arm64 with LSE2: `runtime.stringAtomic`),
  else calls `runtime.slicestore`, which stores them under the sequence
  lock of their block.
- A load is one 16-byte load (`StringLoad16`) and an unpack, or, where
  the access is not atomic, two 8-byte loads between two reads of the
  sequence lock: the words are unpacked only once the lock says no store
  was in progress (`shareLoadSliceLocked`), since a torn w0 with a marker
  is no header; else `runtime.shareloadslice` waits.
- The runtime copies and clears what holds slices 16 bytes at a time or
  under the locks (`tearKind`: a slice is `tearString`), as it does
  strings: `typedmemmove`, `memclr`, `growslice`, maps, channels,
  reflect.

So a racing load gets one store's slice: w0 and w1 come from one store,
so a compact slice's pointer, length and capacity go together, and a
huge slice's marker comes with its header's address, whose header was
published before it and is never written. Stale, never torn.
`TestSliceTear` (`runtime/slicetear_test.go`) checks it, with huge
slices among the values; with `-d=notear` it fails.

`len(s)` and `cap(s)` of a slice in memory load w1 alone, one 8-byte
load (its two fields go together), and the whole slice only if it is the
marker (`sliceLenCapMem`). The marker test of an unpack is a 32-bit
compare with -1 (an 8-bit immediate on amd64). Where a load has two
paths (the 16-byte load, or the runtime's while a store is in progress)
it unpacks the words of both once.

What it costs (amd64, GOAMD64=v1, against the release branch, best of
6, a noisy machine): a geomean of +5.5% over 64 benchmarks of sort,
encoding/json, bytes, strings, bufio, io and the runtime's slices
(`-d=notear`: +0.6%). An append in place to a slice in memory is the
worst (`AppendInPlace/NoGrow/Byte` x3): each append loads the slice and
stores it whole, a chain of 16-byte loads and stores through memory,
where stock Go stores the length. encoding/json is +15% to +39%. What
allocates slices of slices allocates a third less (`bytes.Fields`).
The text of cmd/gofmt is 2.08 MB, +15% (1.81 MB on the release branch,
1.75 MB with `-d=notear`: two words are less code than three).

## 6. Targets

- 8-byte words but wasm: packed (amd64, arm64, ppc64, ppc64le, riscv64,
  s390x, loong64, mips64, mips64le).
- 4-byte words (386, arm, mips, mipsle): stock Go's three words, never
  packed, and not guarded: as phase 2 left their strings (stock Go's two
  words), a slice there is loaded and stored word by word.
- wasm (all GOWASM modes): stock Go's three words; its goroutines do not
  run in parallel.

## 7. Open items

- **Loads that are only indexed** test the marker anyway: a cheaper fast
  path, where `s[i]` of a loaded slice uses w1's
  length field (0 for a huge slice) and only a failed bounds check
  decodes the header, is not done.
- On s390x and mips64x a slice parameter's DWARF location is its frame
  slot, typed as a packed slice, which holds stock Go's words: a
  debugger shows such a parameter wrong.
- SWIG's slices are the packed layout (§3).
- **Programs that cast a slice's address** to `reflect.SliceHeader` or
  a struct of their own (stock Go's layout) are wrong with 8-byte words;
  vet does not see it.
- An append in place stores w0 again, with its write barrier, which
  stock Go's length-only store did not (issue 14855's test skips); the
  parameters' liveness names autotmps where it named the slice
  (`test/fixedbugs/issue15747.go`).
- With `GOEXPERIMENT=sharing`, a huge slice's header is a new object
  that the store of w0 publishes as it would an array; the experiment's
  tests do not have huge slices.
- 4-byte words (386, arm, mips) are not guarded (§6).
