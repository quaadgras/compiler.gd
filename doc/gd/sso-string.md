# Strings of the gd fork

A string is two words, as in stock Go, but with 8-byte words a string of
1 to 15 bytes keeps its bytes in them: `len`, `s[i]`, `==` and hashing of
a short string touch no memory but its words, and making one (`string(b)`,
`a+b`, `strconv.Itoa`, a conversion of a rune) allocates nothing. With
4-byte words (386, arm, mips) a string is stock Go's. GOARCH=wasm with
GOWASM=gc has forms of its own (§7).

History: the first form (2026) was three words, 24 bytes: word 0 a
pointer or nil, word 1 a cached hash of a heap string or bytes 0..7, and
the length or a tag over bytes 8..14 in word 2. It is now 16 bytes so that
a string can be loaded and stored with one 16-byte access, tear-free
(`doc/gd/sharing.md`); the cached hash went with it (a hash is computed
from the bytes, with the map's seed, as in stock Go).

## 1. Layout (8-byte words)

```
not inline:  w0 @0  the address of the bytes (nil for "")
             w1 @8  the length
inline:      w0 @0  1<<63 | n<<56 | bytes 8..14   (n = 1..15)
             w1 @8  bytes 0..7
```

- **The tag is bit 63 of w0**: no address of the heap, the data, the bss
  or a stack has it (user addresses are below 2^47, 2^48 or 2^56), so it
  alone decides the form: `inline(s) = int64(w0) < 0`. The length of an
  inline string is bits 56..59 (bits 60..62 are 0): the top byte of w0 is
  `0x80|n`.
- **Byte order.** Byte i of an inline string is bits 8i..8i+7 of w1
  (i < 8) or of w0 (8 <= i < 15, as i-8): the 16 bytes are those a
  little-endian store of w1 and then w0 writes, the tag last. So the
  bytes of an inline string are in order in a 16-byte buffer that w1 and
  w0 are stored to, in that order (`abi.StringInlineBytes`); they are not
  in order in the string itself, whose w0 comes first. On a big-endian
  target w1 is bytes 0..7 as a big-endian load of them has them, and
  `w0<<8` is bytes 8..15 (`types.StringTaggedWords`).
- **Not canonical.** A string made at run time of 1 to 15 bytes is
  inline (its unused bytes zero); a constant is not, however short: its
  bytes are in rodata, and it is their address and its length, as are
  `unsafe.String`, a substring of a longer string, `-X` and embedded
  strings. So a value of 1 to 15 bytes may have either form, and nothing
  infers the form from the length: it is decoded from the tag. Two inline
  strings are equal exactly when their words are; otherwise equal words
  are a fast "equal" and the bytes decide (equality and hashing go by the
  bytes). The empty string is (nil, 0), never inline.
- `len(s) = inline ? w0>>56&15 : w1` (dec.rules lowers `StringLen` to
  shifts and masks, no branch; a string the compiler made of an address
  and a length, `stringHeapPtr`, has the length).
- `s[i]`: a test of w0's sign; the bytes of an inline one are shifted out
  of w1 or w0 (no memory), the others loaded through w0.
- `==`: equal words are equal strings; otherwise a string not inline of
  the same length is compared with `memequal`, and an inline one is
  unequal to another inline one. An inline string and one that is not, of
  the same length, call `runtime.streqfast`. Against a constant of 1..15
  bytes the words the constant would have inline are immediates, then a
  string not inline of its length compares bytes (loads of exact width).
- **Ordering** (`cmpstring`, amd64 and arm64 assembly): two inline
  strings compare as their byte-swapped words do, w1 then w0, whose low
  byte after the swap is the tag (the shorter of two equal prefixes is
  less); otherwise the bytes of an inline one are put in order in the
  frame for the usual comparison.
- **Hash**: `strhash` hashes the bytes (an inline string's from a 16-byte
  copy) with the seed, so a key hashes alike in either form.

Constants stay in rodata: `unsafe.StringData` of a constant is the
stable address of its bytes, as on stock Go, and C reads them in place.
(The 24-byte form made short constants inline.) An inline string has no
address: `unsafe.StringData` of one that escapes is a copy in the heap
(`runtime.stringDataHeap`), of one that does not, a copy in the frame.

## 2. The collector and word 0

w0 is in the pointer bitmap of every type that holds a string, and in
SSA it is a pointer, so that a heap string's bytes are kept alive. An
inline string's w0 is no address the runtime knows, and every path that
looks at a pointer word ignores it:

- `spanOf`/`findObject` (scanobject, scanblock, conservative scanning of
  async-preempted frames, `shade`, the write barrier buffer's flush,
  `heapBitsSmallForAddr`): `arenaIndex(w0)` is out of the arena index's
  range on every target (bit 63 is above `heapAddrBits`, and above
  `arenaBaseOffset` on amd64, whose kernel half 0xffff8... never has the
  top byte 0x81..0x8f): no span, so not marked and no bad pointer.
  `debug.invalidptr` only throws for a value below 4096 or a pointer into
  a free span.
- Stack copying (`adjustpointers`) moves values inside the old stack only;
  its "invalid pointer" check is for values in (0, 4096).
- Write barriers record w0 old and new; the flush drops them (no span).
  The compiler emits the barrier for any w0 stored to the heap.
- checkptr (`checkptrArithmetic`, `checkptrBase`) finds no allocation for
  w0 and checks nothing more; cgocheck's `cgoIsGoPointer` says no.
- The race detector, msan and asan instrument addresses, not values; a
  string's bytes are read through w0 only after the tag says it is an
  address.
- arm64's top-byte-ignore is not used by the runtime for Go pointers; but
  a C pointer of Android's tagged heap (top byte 0xb4) made into a string
  with `unsafe.String` would read as inline (C.GoString copies).

## 3. Where strings are made

Inline when 1..15 bytes: `slicebytetostring`, `concatstrings`,
`slicerunetostring`, `intstring`, `sliceinlinestring` (a substring of an
inline string), and what calls them (`strconv`, `fmt`, conversions).
Not inline (any length): constants (`ConstString` lowers to the
go:string symbol, static data writes its address), `rawstring` users
(`gostring`, `gostringn`, ...), `unsafe.String`, substrings of heap
strings, the linker's `-X` and runtime strings, `//go:embed`.

C sees a string as its address and length (`_GoString_`): cgo's stubs copy
an inline string to the heap first (`runtime.cgoStringNormalize`), as SWIG
wrappers do (`cmd/go`'s `swigForGd`). (A string an exported function
returns to C must not be inline: cgocheck would reject the heap copy.)

## 4. Compiler

`types.StringTwoWords` / `StringTagged` (`cmd/compile/internal/types`).
In SSA a string is `StringMake(w0, w1)`: `StringPtr` is w0 raw (a
pointer, the tag if inline), `StringWord1` w1, `StringLen` the length;
`PtrBits` reads w0 as an integer. The register ABI passes two integer
registers. `ssagen/stringtagged.go` has the index, the address of the
bytes (`stringBytesTransient`: an inline string's are copied to the
frame), slicing (`runtime.sliceinlinestring` for an inline source) and
equality. A constant's bytes are its go:string symbol's.

Assembly that takes a string (`internal/bytealg`: `IndexString`,
`IndexByteString`, `CountString`, `cmpstring` on amd64 and arm64) tests
w0's sign first and puts an inline string's bytes in order (in its
argument slots, ABI0, or in a frame); the other targets' assembly is
reached through Go (`cmpstringFallback`, `IndexString`), which copies an
inline string to the frame, or not at all (the generic Go code).

## 5. Runtime and libraries

`runtime.stringStruct` is `{str unsafe.Pointer; len int}` with
`isInline`, `length` and `data(buf)`; `internal/abi` has
`StringIsInline`, `StringInlineBytes` and `StringFromInlineBytes`;
`internal/unsafeheader.String` and `reflect.StringHeader` are stock Go's
(Data is the tag for an inline string). reflect reads strings with `len`
and indexing; `Value.Pointer`/`UnsafePointer` of an inline string is a
heap copy. `runtime-gdb.py` and `lib/wasm/wasm_exec.js` (GOWASM=linear)
read inline strings.

## 6. Alignment

With 8-byte words but on GOARCH=wasm (`types.StringAligned16`) a string
is **aligned to 16**, and so is a type that holds one: a string in the
heap, the data or the bss is then one aligned 16-byte block, which can be
loaded and stored with one access (`doc/gd/sharing.md`). What follows
from it:

- `types.CalcSize` (TSTRING align 16), go/types' and types2' `gcSizes`
  (`stringAlign`, so `unsafe.Alignof("")` is 16 and vet's asmdecl lays
  out argument frames with it), `cmd/cgo` (`_GoString_` is
  `aligned(16)`), the linker (`alignString` before the strings of
  `moduledata` and `modulehash`; a symbol of 16 bytes or more is aligned
  to 16 already).
- The heap: a size class of 16 or more is a multiple of 16, so an object
  of a type aligned to 16 is; the malloc header of objects of 512 bytes
  to 32 KiB with pointers is 16 bytes (`gc.MallocHeaderSize`), so that
  what follows it is aligned too. `makechan` allows elements aligned to
  16. The maps read `ElemOff` for 8-byte keys too: an element aligned to
  16 follows an 8-byte key at 16.
- Stacks are aligned to 8 only, and a string in one is too
  (`types.StackAlignment`, which escape analysis, frame layout and
  `typebits` use: a type is not "too aligned for the stack" for its
  strings). No other goroutine accesses a stack, so the 16-byte accesses
  there need only work, not be atomic (MOVUPS and LDP do), and checkptr
  checks a pointer's alignment up to a word's.
- Arguments on the stack (ABI0, spills, reflect's frames) are aligned
  to 16 as the type is, counted from where the arguments start (abiutils
  and `rtcall` as reflect and vet's asmdecl do: on arm64 they start at
  8(RSP)): `debugCallV2` reads `debugCallCheck`'s result at 16(SP)
  (amd64, a frame of 160 bytes) and 24(RSP) (arm64, registers saved from
  48(RSP)). The compare stubs of the other 64-bit targets had a wrong
  argument size for `Compare` since the first commit.

Not on wasm (linear or GOWASM=gc), whose goroutines never run in
parallel, nor with 4-byte words: there a string is aligned to a word.

## 7. Other targets

### 7.1 4-byte words

386, arm and mips: stock Go's two words, never inline
(`abi.StringInlineCap` is 0).

### 7.2 wasm32: 16 bytes, 15 inline

With 4-byte words on WebAssembly a string is **16 bytes, aligned to 4**,
seen as two `uint64`s, A at offset 0 and B at offset 8:

```
inline (tag 1..15):  bytes 0..14 at offsets 0..14, the unused ones zero;
                     the top 4 bits of B (byte 15) are the tag = length;
                     bits 56..59 of B zero.
                     (A and B are exactly words 1 and 2 of the 8-byte form.)
otherwise (tag 0):   word0 @0  address of the bytes (nil for "")
                     word1 @4  length (32 bits)
                     word2 @8  cached hash slot (0; at most 60 bits of B)
                     word3 @12 0
```

`len(s) = tag != 0 ? tag : word1`; the bytes are at `&s` if the tag is
not 0, else at word 0; the empty string is all zero; two inline strings
are equal exactly when their A and B are. The inline capacity, the tag,
the shift and `StringInlineWords` are those of the 8-byte form.

**Word 0 of an inline string is text, not a pointer.** It may be here because the WebAssembly engine
never scans linear memory. With split (`wasm-gc.md` stage 15) what keeps
the bytes of a string alive is the element of its block's array of
pointers for word 0, and that element is nil for an inline string. So
every path that would read word 0 as a pointer tests the tag first
(`OpStringLoadPtr` in the compiler, `stringData` in the runtime,
`abi.StringIsInline`/`StringBytes`/`SpreadString` elsewhere, a register
kind `'s'` in reflect's adapters), and every store of a string stores
word 0 as a pointer (setting the element, nil if inline) before the 16
bytes, which write the word again (`OpStringStorePtr`). A native 32-bit
target, whose collector scans word 0, could not have this form; the
fallbacks considered were 12 bytes inline with the length in word 0 (a
number there, never 1..12, no object at those addresses) and 11 bytes
inline with word 0 nil and the length in the last byte (the 8-byte
scheme scaled down, which would suit a native 32-bit target).

In SSA a string is word 0 as a pointer (nil if inline), A and B: the
pointer and A overlap. In an interface a string is spread as word 0's
pointer in the data word and A and B in the 16-byte payload (not in a
row, as the 8-byte form is), so `any(s)` does not allocate.

### 7.3 GOWASM=gc with 8-byte words

The struct model (`GOWASM=gc`) and `GOWASM=gc,split,ptr64` keep the
three-word form (`types.StringWasmGC64`): word 0 the address or nil (a
reference of the engine's in the struct model, which cannot hold text),
word 1 a hash slot (0) or bytes 0..7, word 2 the length or the tag in the
top 4 bits over bytes 8..14 (`internal/abi/string_wasmgc64.go`,
`runtime/wasmgc`).
