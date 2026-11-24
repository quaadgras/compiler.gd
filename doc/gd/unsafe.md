# Checked unsafe.Pointer (gd fork)

Goal: a program that misuses `unsafe.Pointer` stops, instead of reading
or writing memory that is not its own. The rules are Go's (package
unsafe's six patterns); the fork checks them in every build.

> **Status:** in: the checks Go has under the race detector
> (`-d=checkptr`) are the default, made cheaper for the usual cases, and
> extended with a check of where the pointers are; and an integer that
> was kept (a `uintptr` field, an argument of a callback, which C-style
> APIs have) is a pointer again if its object is exposed. Not yet: the
> integer keeping its object alive, bounds of stack objects and globals,
> and exact sizes in the heap (an object's bound is its size class).

## What is checked

The compiler adds a call where a pointer comes out of `unsafe`
(`cmd/compile/internal/walk`, `ssagen`), and the runtime looks at the
memory (`runtime/checkptr.go`). A check that fails is a fatal error.

- **`(*T)(p)`**, `p` an `unsafe.Pointer` (`checkptrAlignment`): `p` is
  aligned for `T` if `T` has pointers; the `T` at `p` is inside one
  allocation; and where `T` has a pointer, the object has one too, or
  has a word that does not hold the address of anything in the heap. The
  last is new: an integer that happens to be an address is not read as a
  pointer. (The first word of an interface is a word the collector does
  not look at that points to a type or a method table, outside the heap,
  and reflect reads it as a pointer: this check allows it, and reflect is
  not held to the rule on views below.)
- **`(*T)(p)` is not a partial view of a string, a slice or an interface**
  (below).
- **`unsafe.Pointer(u)`**, `u` a `uintptr` (`checkptrArithmetic`): if the
  result is in a heap object, one of the pointers the expression was
  computed from is in the same object, or the object is *exposed*.

  An object is exposed when the program makes an integer of its address
  and keeps it: compiled code calls `runtime.exposeptr` where it converts
  a pointer to a `uintptr`, unless the integer only lives in an
  expression that makes a pointer of it again, or the pointer is plainly
  to a variable of the stack. The mark is a bit for the object, kept as
  the shared bit is. So `h := uintptr(unsafe.Pointer(p))` kept in a field
  and `(*T)(unsafe.Pointer(h))` later work, and `(*T)(unsafe.Pointer(h +
  64))` does if it is still in `p`'s object, and not if it is in the
  next one, which never gave its address away. This is what C calls an
  exposed address (PNVI-ae). The integer does not keep the object alive:
  once it is freed the integer is the address of nothing exposed.
- **`unsafe.Add`**: as the arithmetic.
- **`unsafe.Slice`, `unsafe.String`**: the result is inside one
  allocation. (What the language says panics still panics, and is not a
  fatal error: `unsafeslicecheckptr` uses the caller's pc.)

## Views of strings, slices and interfaces

These values are loaded and stored as a unit, which is what makes a race on
them safe (`publication-point.md`, `sso-string.md`, `slice16.md`,
`fat-iface.md`). Storing one word at a time through a converted pointer
would undo that for every reader, and a conversion cannot tell a load from
a store. So:

> A view is **whole** if every string, slice or interface it overlaps is
> covered completely by a value of the same kind at the same offset.
> Otherwise it is **partial**, and the conversion is a fatal error:
> `checkptr: converted pointer is a partial view of a string, a slice or an
> interface`.

- Allowed: string as string, slice as slice of any element type, interface
  as interface, `internal/abi`'s `EmptyInterface` and `NonEmptyInterface` as
  an interface, and any of these inside a struct or an array at the same
  offset. A view of memory that holds none of them may have any type: it is
  not part of a value that goes whole.
- A plain pointer viewed as `*uintptr` is not covered.
- **`[]byte` as `string`, and the reverse**, are partial with 8-byte words:
  they never worked there. A slice's second word is `len:32|cap:32`, so a
  slice read as a string has the length `len|cap<<32`, and an inline string
  read as a slice has its bytes for a pointer and a length. With 4-byte
  words a string is a slice's first two words, and they stay allowed. Use
  `unsafe.String` and `unsafe.Slice`.
- **`reflect.StringHeader`, `reflect.SliceHeader` and `internal/unsafeheader`
  views of a string or a slice are partial.** What the compiler has for
  them is two things, and neither makes such a view safe: a store to a
  `Data` field has a write barrier (`ir.IsReflectHeaderDataField`), and
  `unsafe.Pointer(h.Data)` is not checked as arithmetic. A `SliceHeader`
  has not been a slice's layout with 8-byte words since slices were packed.
  The other direction, a header variable read as a value, is not a partial
  view and is not rejected: a `StringHeader` read as a `string` works, but
  a `SliceHeader` read as a `[]T` has its `Len` as the length and a
  capacity of 0, which is wrong.

How it is checked:

- **The compiler**, where it knows what `p` points into: `&x`, `&x.f`,
  `&a[i]`, a `p` of static type `*U`, `unsafe.Add` or `uintptr` arithmetic
  of those with a constant, and a local assigned once to any of these
  (`walk/wholeview.go`, `types/whole.go`). If the view is partial, it emits a
  call that fails at the conversion, so code that never runs that line
  still builds. If it is whole, nothing is added.
- **The runtime**, for a plain `unsafe.Pointer`: `checkptrAlignment`
  compares the view with the type the object records, if it has one, and
  `checkptrView` does for types of alignment 1, which need no other check.
  Objects with a malloc header and large objects record their type. Objects
  of 512 bytes or less have the states of their words in a bitmap of their
  span (below). The comparison is the same: where the object's word is part
  of a whole value, the view's must have the same state, and the word just
  past the view must not be a continuation. Each value of the view must be
  over one whole value or over plain memory, not partly each, so an
  interface cannot be viewed over a string and the two plain words after it.
  A view of plain memory may have any type, so
  `*(*string)(unsafe.Pointer(&hdr))` keeps working.

A type longer than its object, as in `(*[1<<30]byte)(p)`, is compared up to
the end of the object, or with a recorded type, of the value `p` is in: an
object may have room after its last value, which holds nothing. Generic
code has a type's shape, so the shapes of `reflect.Value` and of
`internal/abi`'s interfaces count as what they are.

### The bitmap of small objects

A span of objects of 512 bytes or less that holds a whole value has a
`wholeBits` (`runtime/mwholebits.go`), two bits for each word of the span:

| State | Word |
| --- | --- |
| `00` | not part of a whole value |
| `10` | a later word of a whole value |
| `11` | the first word of a string, an interface or `reflect.Value` |
| `01` | the first word of a slice |

One bit per kind would not do: in an array of interfaces every word would
have it, and a view shifted by a word would pass.

- **Who pays.** A noscan span has none. A span that never held a whole
  value has a nil pointer. It is 256 bytes for an 8 KiB span with 8-byte
  words (3.1%), outside the heap, in `GCSys`, and goes back to a free list
  with its span.
- **Allocation.** `mallocgc` tests one bit of the type's descriptor
  (`abi.FieldAlignWhole`, the high bit of `FieldAlign_`, which no alignment
  has), set by the compiler and by reflect for a type that holds a whole
  value, and looks at whether the span has a bitmap. The states of such a
  type are computed once from its descriptor and kept in a table, so the
  binary has no new data. An allocation in a span with a bitmap always
  writes its slot, zero if it has no whole value, so a slot never keeps the
  states of the object that was there. The states are written before the
  publication barrier.
- **The collector** does not touch it: it is not in the GC's arenas, so the
  states of what survives stay.

`-d=checkptr=0` turns it off. `-d=unsafewhole=1` reports the conversions
the compiler proves partial instead of failing, and `=2` also those whose
source it does not know.

A nil pointer views nothing and always passes. `sync/atomic`'s `ifaceEq` is
`//go:nocheckptr`: it compares two by-value copies in its own frame, word by
word, which no other goroutine can reach.

Measured on amd64 against the commit before the bitmap, user-mode
instructions with `perf stat -r 5` and `GOGC=off`, in two passes:

| Workload | Instructions | Repeat spread |
| --- | ---: | ---: |
| allocation of 32 bytes, no whole value | 188.4 to 195.5 per object, +3.8% | under 0.1% |
| allocation of 32 bytes with a string or an interface | 188.4 to 268.8, +43% | under 0.1% |
| `make([]string, 8)` | 411.7 to 577.4, +40% | under 0.1% |
| fmt `SprintfStructure` | +0.93% | 0.13% |
| fmt `ManyArgs` | +0.09% | 0.01% |
| json encode, decode | +0.10%, +0.16% | up to 0.09% |
| http ClientServer | +1.89% | 0.22% |
| compile `go/types` | +1.72% | 0.02% |

In time, on one pinned CPU: 21.4 to 21.8 ns for an object with no whole
value (+1.5%), 21.4 to 25.6 ns with a string or an interface (+19%). A heap
of 2M objects of 32 bytes with strings has 2.0 MB more in `GCSys` (3.1% of
those spans) and the same heap. Peak RSS is +1.5% for a compile of `go/types`
and for http ClientServer, within their spread of runs, and +0.2% for json.

### Known gaps

- **The stack and globals through a plain `unsafe.Pointer`**: they have no
  recorded type here. A pointer whose source the compiler can see is
  checked; one that came through a parameter or a field is not.
- A pointer of static type `*U` is taken to point to a `U`, so a `*byte`
  that really points into a string is only caught by the runtime check. The
  reverse rejects valid code: plain memory converted to `*string`, passed
  through a parameter and converted back to its own type is a fatal error.
- `(*[N]T)(p)[:l:m]` with a non-constant `m` is checked by the compiler for
  its first element only.
- `reflect.Value`, which goes whole too, is checked as a whole value, but
  package reflect itself is not held to the rule.

## Where it is not

- The runtime's packages and `internal/c23` (C's runtime, which keeps
  pointers the checks would refuse and has its own) are compiled without
  the checks. A function of theirs that is inlined in checked code has
  its conversions marked not to be checked (`ir.NoCheckPtrIn`), rather
  than not being inlined, as it was with `-d=checkptr`: `abi.TypeOf` and
  `stringslite.HasSuffix` inline as before. The same for a function
  marked `//go:nocheckptr`.
- Package reflect is compiled without the checks too: its pointers are
  to values of the types it says they are, by construction, but for
  `reflect.NewAt`, which is `(*T)(p)` and is checked as that
  (`reflect.checkptr`). Its functions inline in checked code as the
  runtime's do.
- The compiler's own conversions: a result built in the caller's buffer,
  the element pointer of a range loop, the key and element of a map's
  iterator (anything made with `typecheck.ConvNop`), the equality and
  hash functions it writes. These were checked on every iteration with
  `-d=checkptr`, which nobody ran in production.
- A function marked `//go:uintptrescapes` or `//go:uintptrkeepalive` is
  given pointers as integers, which its callers keep alive for it: the
  pointers it makes of integers are not checked.
- wasm, for now.

`-gcflags=all=-d=checkptr=0` turns the checks off.

## What it does not see

- A pointer stored, through a converted pointer, in a word the collector
  does not look at; an integer stored where the object has a pointer. A
  conversion cannot tell what will be stored.
- A pointer one past the end of an object is, to these checks, a pointer
  to the next object.
- The stack is one allocation, and so is each of data and bss.

- An integer that outlives its object and is then the address of
  another exposed object is a pointer to that one.

These need the capability to travel with the pointer, as it does for C
(`cc.md`): the plan is the thin representation agreed on 2026-11-18, the
address in the word and the capability beside it only for a word that
cannot give it back (an integer, a pointer out of bounds). Exposure is
the part of it that needs no side table.

## What it costs

The checks are calls. With reflect and the compiler's own conversions
out of them, `encoding/json`'s encoder and decoder and `fmt` cost what
they do without the checks; `net/http`'s header writer, which makes
strings of bytes with `unsafe.String`, is a fifth slower. The usual
cases are found without looking for the allocation of each end: the
goroutine's stack, one object of the heap, a pointer converted back to
the type its object was allocated with, and memory outside the heap.

## Tests

`runtime/checkptr_test.go`, with `CheckPtrPointers*` for the check of
pointers and `TestCheckPtrViews` for views (`testprog/checkptr_whole.go`),
`runtime/mwholebits_test.go` for the bitmap;
the standard library's tests pass with the checks on. Six tests of
`test/` that count instructions or diagnostics, or that misuse `unsafe`
on purpose, run with `-d=checkptr=0`.
