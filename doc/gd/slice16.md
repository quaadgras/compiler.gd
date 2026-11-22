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
returned in two integer registers, w0 and w1, and on the stack as in
memory), from interfaces (a slice is spread in an interface as in memory:
w0 in the data word, w1 in the first half of the payload, the second
zero, as a string is) and from `ssagen`. Rules fold an unpack of a pack
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

(big-endian: the halves of w1 swapped). It first drops the packs that
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
- Assembly: an ABI0 frame lays a slice out as memory does: `x_base+0`,
  `x_len+8` and `x_cap+12`, uint32s (vet's asmdecl knows), aligned to 16
  (§4). The functions of `internal/bytealg` that take slices take their
  words from Go wrappers (`compareSlices`, `countSlice`, `indexSlices`,
  `indexByteSlice`: the pointer, the length and the capacity, the stock
  slice's frame and registers), so they handle huge slices. The other
  assembly functions read the uint32 fields (`MOVL`, `MOVWU`, `MOVWZ`):
  given a huge slice they see an empty one (open item).
- cgo: `GoSlice` is `{void *data; GoUint32 len; GoUint32 cap;}`, aligned
  to 16; `C.CString` and `C.CBytes` make their slices of C memory from
  array pointers. SWIG's `_goslice_` is rewritten likewise (`cmd/go`).

## 4. Alignment

A slice is aligned to 16 where it is packed (`types.SliceAligned16`), as
a string is (`sso-string.md` §6), and for the same reasons: a slice in
the heap, the data or the bss is one aligned 16-byte block. The stack
aligns it to a word (`types.StackAlignment`). go/types and types2
(`gcSizes.slicePacked`), cgo, the linker (`sliceAlign`) and the runtime's
type descriptors (`abi.RTypeSize`) agree.

## 5. Targets

- 8-byte words but wasm: packed (amd64, arm64, ppc64, ppc64le, riscv64,
  s390x, loong64, mips64, mips64le).
- 4-byte words (386, arm, mips, mipsle): stock Go's three words, never
  packed, and not guarded: as phase 2 left their strings (stock Go's two
  words), a slice there is loaded and stored word by word.
- wasm (all GOWASM modes): stock Go's three words; its goroutines do not
  run in parallel.
