# Checked unsafe.Pointer (gd fork)

Goal: a program that misuses `unsafe.Pointer` stops, instead of reading
or writing memory that is not its own. The rules are Go's (package
unsafe's six patterns); the fork checks them in every build.

> **Status:** in: the checks Go has under the race detector
> (`-d=checkptr`) are the default, made cheaper for the usual cases, and
> extended with a check that the type converted to is compatible with
> what the memory is (below, "Compatible types": the first step of the
> plan for memory-safe `unsafe`, 2026-11-25); and an integer that
> was kept (a `uintptr` field, an argument of a callback, which C-style
> APIs have) is a pointer again if its object is exposed. Not yet: the
> integer keeping its object alive, bounds of stack objects and globals,
> exact sizes in the heap (an object's bound is its size class,
> but for what C's malloc gave: `cc.md`), and the rest of that plan.

## What is checked

The compiler adds a call where a pointer comes out of `unsafe`
(`cmd/compile/internal/walk`, `ssagen`), and the runtime looks at the
memory (`runtime/checkptr.go`). A check that fails is a fatal error.

- **`(*T)(p)`**, `p` an `unsafe.Pointer` (`checkptrAlignment`): `p` is
  aligned for `T` if `T` has pointers; the `T` at `p` is inside one
  allocation; and `T` is compatible with what the memory is (below,
  "Compatible types"): it has a pointer where the memory has one, and
  nowhere else, and its pointers point to what the memory's do.
- **`(*T)(p)` is not a partial view of a string, a slice or an interface**
  (below).
- **`unsafe.Pointer(u)`**, `u` a `uintptr` (`checkptrArithmetic`): if the
  result is in a heap object, one of the pointers the expression was
  computed from is in the same object, or the object is *exposed*.

  An object is exposed when the program makes an integer of its address
  and keeps it: compiled code calls `runtime.exposeptr` where it converts
  a pointer to a `uintptr`, unless the integer only lives in an
  expression that makes a pointer of it again, or in a comparison, or
  in a subtraction of one such integer from another, or the pointer is
  plainly to a variable of the stack. The mark is a bit for the object, kept as
  the shared bit is. So `h := uintptr(unsafe.Pointer(p))` kept in a field
  and `(*T)(unsafe.Pointer(h))` later work, and `(*T)(unsafe.Pointer(h +
  64))` does if it is still in `p`'s object, and not if it is in the
  next one, which never gave its address away. This is what C calls an
  exposed address (PNVI-ae). The integer does not keep the object alive:
  once it is freed the integer is the address of nothing exposed.
  A result that is in nothing the runtime knows, not in the heap, the
  stack or the data, is refused if every pointer it was computed from
  is in an object of the heap: it is in none of their objects, and
  what is there, if anything, is another thread's stack or the
  runtime's own memory. (A result below the first page is an integer,
  not an address.)
- **`unsafe.Add`**: as the arithmetic.
- **`unsafe.Slice`, `unsafe.String`**: the result is inside one
  allocation, which for an object that C's malloc gave ends where C
  asked for it to (`cc.md`), and the elements of a slice are
  compatible with what the memory is (below). (What the language says panics still panics, and is not a
  fatal error: `unsafeslicecheckptr` uses the caller's pc.)

## Compatible types

Code that uses `unsafe` may take the bytes of memory for what it likes,
and not its pointers. A conversion cannot tell a load from a store, so
it is refused if a store through its result, or a load, could make a
pointer of what is not one, or a pointer to a `T` of what points to
something else:

> `(*T)(p)` is a fatal error unless `T` and the type of the memory agree,
> word by word, on what a word is.

| `T` has | the memory has | |
| --- | --- | --- |
| no pointer | no pointer | allowed, whatever the types: integers, floats, arrays of them |
| no pointer | a pointer | refused: an integer stored there is a pointer that the program made up, once it is loaded again. Allowed where the result is only loaded through (below) |
| bytes (a type of alignment 1) | a pointer | as any integer: refused, unless the result is only loaded through. (In a package that is C translated by `cmd/cc`, bytes are over anything: C looks at all memory as bytes, and its stores of them are to be checked by themselves, the plan's step 3) |
| a pointer | no pointer | refused, whatever the integer is |
| `*B` | `*A` | allowed if `A` and `B` are the same size and agree in this way themselves, both ways: a `*B` stored there is loaded as a `*A` by the code that the memory is `A`'s to |
| `unsafe.Pointer`, or a pointer to a type of no size (C's `void *`) | any pointer, func, map or chan | allowed: what is loaded is converted before it is used, and checked then. (What is stored through it is not checked yet.) |
| `*B` | `unsafe.Pointer` | refused: it would be a conversion with no check |
| a func, a map, a chan | the same | allowed if the parameters and results, or the key and the element, are the same types. (A map is also a pointer to `internal/runtime/maps.Map`. A func is nothing else: its closure begins with the address of its code) |
| a slice | a slice | allowed if the elements are the same size and agree: `[]int32` as `[]float32`, not `[]byte` as `[]uint64`, whose length would be in the wrong unit |
| an interface | an interface | allowed if both are of the same type, or have no methods, or one is `internal/abi`'s struct for it |

So a `(*uintptr)(unsafe.Pointer(&p))` that is kept or stored through is
refused for a pointer `p`, and so is a `reflect.StringHeader` variable
read as a `string`, which package unsafe never allowed. Types of the
same shape under other names are compatible, to any depth: a type that
points to itself is compared once.

**What is only loaded through.** The rule is about what a store could
do. Where the compiler sees that the result of the conversion is
dereferenced for a value and nothing else, as in
`*(*uintptr)(unsafe.Pointer(&p))`, `(*T)(p).f` and `(*[2]uintptr)(p)[0]`
that are not assigned to, sliced, or have their address taken
(`walk.markLoadOnly`, `ir.ConvExpr.LoadOnly`), integers may be over
pointers: a pointer read as an integer is an integer. The runtime's
check is then `checkptrAlignmentLoad`. Nothing else is relaxed: a
pointer over an integer, or over a pointer to something else, is a
pointer that the program would go on to use.

**Bytes are integers.** `(*[8]byte)(unsafe.Pointer(&p))[0]` and
`*(*[16]byte)(p)` are loads, and look at anything. A view of bytes
that is kept, `(*[16]byte)(p)[:]` or a `*byte` in a variable, can be
stored through later, by code that no conversion is in: it is refused
over a pointer as a `*uintptr` is (`checkptrViewKept`, for a type of
alignment 1, which needs no other check). The decision is the user's,
2026-11-25: "Go should refuse such views unless they are load-only".

**`unsafe.Slice(p, n)`** is of n elements where `p` was converted for
one. The elements are compared with the memory as a conversion to
`*[n]T` that is kept would be: `unsafe.Slice((*byte)(p), n)` over a
struct's pointers is refused, and `unsafe.Slice(&a[i], n)` in an array
of the same type is what the object was allocated as. `unsafe.String`
is not compared: a string is not stored through.

Where the type of the memory comes from:

- **An object of the heap that has pointers** records what it was
  allocated for, and the runtime compares `T` with it
  (`runtime/checkptr_compat.go`, `checkptrCompat`). One of more than 512
  bytes has the type in its header, or in its span if it is large. A
  smaller one has no header: its span keeps a number for each object,
  which stands for the type (below, "The types of small objects").
- **An object without pointers**, and a small object of a type that the
  program made as it ran (`reflect.StructOf`), which has no number:
  `T` must have no pointer, or its pointers where the object's bitmap
  has them and nowhere else (`checkptrPointers`).
- **What the compiler knows.** Where `p` is made of a pointer to `S`
  (`&x`, a `p` of static type `*S`, an offset of those: as for views,
  below), `T` is compared with `S` when the package is compiled
  (`cmd/compile/internal/types/compat.go`, `CompatView`), and the
  conversion fails when it runs if they disagree. `S` is itself a type
  that the memory was found compatible with, so what `S` and `T`
  disagree on, the memory and `T` do. Bytes say nothing of what they are
  over, and neither does an `unsafe.Pointer` of what the pointer under
  it points to, nor a pointer to a type of no size (`*struct{}`, C's
  `void *`): those are left to the runtime. This is the only check
  of the stack and of globals, which have no type at run time.
- **Generic code** has a type's shape, in which every pointer is a
  `*uint8`. The conversion is given the type itself, from the
  dictionary (`ir.ConvExpr.PtrRType`), and the compiler compares nothing
  there. What generic code allocates, a variable of a type parameter
  that is moved to the heap, is allocated as the shape, and that is the
  type that the object records: it is compared for what a shape says,
  its layout and its kinds, and a pointer of a shape is a pointer to
  anything (`compatShape`).

Not compared: the conversions of a package that is C translated by
`cmd/cc`, whose memory has its own rules until the plan's next steps
(`cc.md`); the runtime's packages and reflect, as for every check here;
and the functions that the compiler writes itself.

`-d=unsafewhole=1` reports the conversions that the compiler finds
incompatible, instead of failing. What the standard library, the tools
and their tests had:

- The compiler's own cache of slices gave out memory allocated as
  `[]*Value` as a `[]*Block`
  (`cmd/compile/internal/ssa/_gen/allocators.go`): it has a list of its
  own now. Slices of integers of different sizes still share memory
  there.
- Tests that look at a func value as the words of its closure
  (`cmd/compile/internal/test`'s escape bits, `reflect`'s
  `TestValuePointerAndUnsafePointer`, `test/fixedbugs/issue11656`, which
  makes a func of bytes): `//go:nocheckptr`, or the func's word read as
  an `unsafe.Pointer` first.
- Two of cgo's tests of its own pointer checks
  (`cmd/cgo/internal/testerrors`, `union1` and `union2`) pass the
  address of a Go pointer as a C union, which cgo has as bytes: a view
  of bytes that is kept over a pointer. They are `//go:nocheckptr`.
  Nothing else in the standard library, the tools or their tests keeps
  bytes over a pointer, or slices over what is not their element.
- `time.Ticker` is the runtime's timer, which the runtime allocates
  with its own type for it, whose channel is an `unsafe.Pointer`:
  `NewTicker`, `Stop` and `Reset` convert between the two and are
  `//go:nocheckptr`. (Found once small objects had their types.)
- What SWIG generates for a function that returns a string reads C's
  struct of a pointer and a length as a Go string. `cmd/cgo/internal/swig`
  compiles its programs with `-d=checkptr=0`: SWIG's output does not
  run with the checks.

### The types of small objects

An object with pointers of 512 bytes or less has no malloc header:
where its pointers are is in its span's bitmap, and a `struct{ p
*[1]int }` looked the same there as a `struct{ p *[1000]int }`. The
user's decision, 2026-11-25, on whether such objects are to have their
types: "yes if they contain pointers". So a span of them has a `uint32`
for each object (`runtime/mobjtypes.go`, `mspan.otypes`): the offset of
the type's descriptor in the program's types, plus one, and 0 for a type
that is not there. The object holds values of the type from its base
on, as one with a header does after it.

- **Who pays.** A span of objects without pointers has none. The
  numbers are 4 bytes an object, outside the heap, in `GCSys`, and go
  back to a free list with their span: an eighth of a heap of 32-byte
  objects, a quarter of one of 16-byte objects, half of one of single
  pointers.
- **Allocation** writes the object's number before the publication
  barrier, every time, so a slot never keeps the type of the object
  that was there. The collector does not touch them.
- **The checks** find the type as they find a header's, so a
  conversion back to the type that a small object was allocated as is
  let through at once, which it was not, and a view of part of a
  string, a slice or an interface in one is found from the type. (The
  bitmap of whole values below is still written, and is what the check
  goes by for an object whose type has no number.)

Measured on amd64 against the commit before, user-mode instructions
with `perf stat -r 5` and `GOGC=off`:

| Workload | Instructions | Repeat spread |
| --- | ---: | ---: |
| allocation of 32 bytes with a pointer | 157.5 to 176.1 per object, +12% | under 0.1% |
| allocation of 16 bytes with a pointer | 156.0 to 174.9, +12% | under 0.1% |
| allocation of a pointer (8 bytes) | 108.2 to 123.7, +14% | under 0.1% |
| allocation of 32 bytes with a string | 231.7 to 248.5, +7% | under 0.1% |
| `make([]*int, 8)` | 223.2 to 244.8, +10% | under 0.1% |
| fmt `SprintfStructure` | -0.5% | 0.6% |
| fmt `ManyArgs` | +0.01% | 0.01% |
| json encode, decode | -1.1%, +0.4% | 1.1%, 0.2% |
| http ClientServer | +0.8% | 0.1% |

In time, on one pinned CPU with the collector off, so that every object
and every number is in memory that is touched for the first time: 20.5
to 23.9 ns for an object of 32 bytes, 15.4 to 18.8 ns for one of 16,
9.3 to 12.6 ns for a pointer. 20M objects have 80 MB more in `GCSys`,
whatever their size: 635 to 715 MB in all for 32-byte objects, 159 to
239 MB for pointers.

### What this does not close

- **What a pointer points to, in an object that generic code allocated
  for a type parameter**: the object has the type's shape. Closing it
  needs the allocation to take its type from the dictionary, as the
  conversion does.
- **A small object of a type that the program made as it ran**
  (`reflect.StructOf`) has no number, and is compared by where its
  pointers are.
- **A store through an `unsafe.Pointer` that is over a typed pointer**
  is not checked: `*(*unsafe.Pointer)(unsafe.Pointer(&p)) = q` puts any
  pointer where a `*A` is. `sync/atomic.StorePointer` is used this way.
- **Bytes over a pointer in C's packages** can be written, until their
  stores of bytes are checked (step 3).
- **The stack and globals through a plain `unsafe.Pointer`** have no
  type at run time: only what the compiler knows is compared.

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

- Allowed: string as string, slice as slice, interface as interface,
  `internal/abi`'s `EmptyInterface` and `NonEmptyInterface` as an
  interface, and any of these inside a struct or an array at the same
  offset, if their types are compatible (above). A view of memory that
  holds none of them is not part of a value that goes whole, and is held
  to that rule alone.
- A plain pointer is not a value that goes whole. Its bytes may be
  loaded; a view of them that is kept, or a `uintptr` over it, is
  refused by the rule above.
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
  Objects with a malloc header and large objects record their type, and
  so do smaller ones now ("The types of small objects", above). Objects
  of 512 bytes or less also have the states of their words in a bitmap of
  their span (below), which is what is looked at for one whose type has
  no number. The comparison is the same: where the object's word is part
  of a whole value, the view's must have the same state, and the word just
  past the view must not be a continuation. Each value of the view must be
  over one whole value or over plain memory, not partly each, so an
  interface cannot be viewed over a string and the two plain words after it.
  A view of plain memory is not partial, whatever its type: whether a
  string may be over a pointer and an integer is for the rule of
  compatible types to say, and it says no where the memory has a type.

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

- What the gaps of "Compatible types" list: the types of a small
  object's pointers, stores through an `unsafe.Pointer` over a typed
  pointer, and bytes written over a pointer.
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
pointers, `TestCheckPtrCompat` for compatible types
(`testprog/checkptr_compat.go`: what must pass and what must stop, for
objects with a type, with a bitmap and for what the compiler knows) and
`TestCheckPtrViews` for views (`testprog/checkptr_whole.go`),
`runtime/mwholebits_test.go` for the bitmap;
the standard library's tests pass with the checks on. Six tests of
`test/` that count instructions or diagnostics, or that misuse `unsafe`
on purpose, run with `-d=checkptr=0`.
