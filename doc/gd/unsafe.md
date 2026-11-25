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
> APIs have) is a pointer again if its object is exposed; and a variable
> of the stack, and one of the program's data, is an allocation with its
> own bounds and its type, as an object of the heap is (below, "The
> variables of the stack and of the data", 2026-11-26). Not yet: the
> integer keeping its object alive, exact sizes in the heap (an object's
> bound is its size class, but for what C's malloc gave: `cc.md`), and
> the rest of that plan.

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
  computed from is in the same object, or the object is *exposed*. If
  it is in the stack or in the program's data, one of them is in the
  same variable, and the result is in the variable or at its end.

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
  `void *`): those are left to the runtime.
- **A variable of the stack, or of the program's data**, has the type
  it was declared with, which the runtime finds from where it is
  (below, "The variables of the stack and of the data"), and compares
  `T` with as with the type of an object of the heap. A variable with
  no Go type, as the bytes of a string are, has no pointer.
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

### A store through an unsafe.Pointer

An `unsafe.Pointer` may be over a typed pointer: `(*unsafe.Pointer)(p)`
where `p` points to a `*A`. A load through it gives the pointer, with no
type. A store through it would put any pointer where a `*A` is, so it is
looked at where it is made (`runtime.checkptrStore`): what is stored in
a word that the memory's type has a `*A` in is a pointer to an `A`, or
to what is compatible with one, or the program stops. That is for

    *(*unsafe.Pointer)(p) = q

and for `sync/atomic`'s `StorePointer`, `SwapPointer` and
`CompareAndSwapPointer`, which are how such a pointer is mostly used.
A word that the memory's type has an `unsafe.Pointer` in takes any
pointer, as before; `atomic.Pointer[T]` has one.

Only a pointer to an `unsafe.Pointer` itself, or to an array of them, is
over a typed pointer this way. A struct that has an `unsafe.Pointer`
where the memory has a `*A` is not compatible with it, unless the
conversion is only loaded through: a store to that field would be a
plain store, with nothing looking.

### A pointer to bytes that go past the object

`(*[1 << 30]byte)(p)` was let be, as it is in Go: the bytes were looked
at as far as the object went, and an index went anywhere. The type is to
fit in what `p` points into now, as any other does. What is sliced at
once, `(*[1 << 30]byte)(p)[:n:n]`, is asked about as its `n` bytes, which
is how code made a slice of a pointer before `unsafe.Slice`.

### What this does not close

- **What a pointer points to, in an object that generic code allocated
  for a type parameter**: the object has the type's shape. Closing it
  needs the allocation to take its type from the dictionary, as the
  conversion does.
- **A small object of a type that the program made as it ran**
  (`reflect.StructOf`) has no number, and is compared by where its
  pointers are.
- **An integer that is the address of something and is made a pointer
  with nothing in the expression that it came from** is a pointer if
  the address is of an object whose address the program took as an
  integer ("exposed", below), or of nothing that the checks know.

## The variables of the stack and of the data

An object of the heap has its bounds and its type in its span. A
variable of a stack frame, and one of the program's data, had neither:
the stack was one allocation to these checks, and so was each of data
and bss, so that arithmetic on a pointer to one local reached every
other slot of the stack, return addresses with them, and a pointer to a
`long` could be converted to a `**byte` on its way through an
`unsafe.Pointer`. They have both now, in two tables.

- **A function has the variables of its frame**, as a funcdata
  (`FUNCDATA_StackVars`; `cmd/compile/internal/liveness`,
  `emitStackVars`): a record of 8 bytes for each, its offset in the
  frame, from `varp` if it is negative and from `argp` otherwise, as a
  stack object's, and its type, as the offset of the descriptor in the
  module's types. The size is the type's. It has every variable with a
  slot in the frame whose address is taken, which are the ones that a
  pointer can be to, and every one that has pointers: named variables,
  parameters and results, with the slots that the parameters passed in
  registers are spilled to, the compiler's temporaries, and the slots
  that values in registers are spilled to, each of which is for values
  of one type. `-d=stackvars=1` leaves out the ones whose address is not
  taken, and `=0` writes no table.
- **Which functions.** Every one but those of the runtime's packages
  and the equality and hash functions that the compiler writes for a
  type (`ir.StackVars`), whether its package is checked or not: a
  pointer to a variable of a package that is not checked (reflect,
  `internal/c23`, one compiled with `-d=checkptr=0`) is given to code
  that is. It goes by the function's own package, so that the instance
  of a generic function is the same in every package that has a copy
  of it.
- **A module has the symbols of its data**, sorted by address
  (`runtime.datavars`; `cmd/link/internal/ld/datavars.go`): each symbol
  of data, bss, noptrdata and noptrbss, and of the read-only data that
  Go code can have a pointer to, which is the bytes of strings and
  read-only variables, and not type descriptors, method tables or what
  the tables of functions are in. An entry is 8 bytes: the symbol's
  offset from the start of its section, and the offset of its type's
  descriptor, or its size if it has no Go type. The type is the one the
  object file has for the symbol, which the linker did not keep for
  anything but debugging information; it keeps the descriptor now, but
  not for the variables of the runtime's own packages, whose types
  nothing else asks for and which would be several times the table.
  The bits that the collector has for a type are in the table as bytes,
  since the bytes of a string are in such a symbol when they are the
  same bytes: symbols named by what they hold are one symbol. The
  counters of a coverage build are one variable, which is how their
  runtime reads them. `-ldflags=-datavars=false` leaves the table out.
- **Nothing follows a variable at once.** The compiler leaves a byte
  between a variable of a frame whose address is taken and the next
  one, and the linker a byte after each symbol of the table, as an
  object of C's on the heap has one (`cc.md`). So the address after a
  variable, which C may make a pointer of and the check of arithmetic
  allows, is in no variable: it is not also a pointer to the start of
  another, which would be let through as that one.
- **The lookup** (`runtime/stackvar.go`, `runtime/datavar.go`). An
  address of the data is found by a binary search of the module's
  table. An address of the stack is in the frame of some caller of the
  check: the stack is unwound from the check to the frame that the
  address is in, on the system stack, and the address is looked up in
  the table of that frame's function, which is the function that is
  compiled, with what is inlined in it. On amd64 the frame is found
  first by the frame pointers that the frames save, two loads for each
  frame between; the function is the one that the frame's callee
  returns to; and what is found is kept only if the frame is where the
  function's frame size says it is, since a frame that saves no frame
  pointer is not seen that way. The P keeps the last four variables
  that were found, so a loop that steps a pointer through a variable
  finds it again with no call.
- **The checks.** Arithmetic gives a pointer into the variable that one
  of its operands is in, or to its end (`checkptrArithmetic`).
  `(*T)(p)` is in the variable whole, and `T` is compatible with the
  variable's type, as with an object's (`checkptrAlignVar`); so are the
  elements of `unsafe.Slice`, and bytes that are kept
  (`checkptrViewKept`). `unsafe.Slice` and `unsafe.String` do not go
  past the variable.
- **What is in no variable that is known**: the frame of a function of
  the runtime's or of assembly, the space between two variables, the
  slot of a variable without pointers whose address is not taken, the
  saved registers and return addresses. Arithmetic gives no pointer to
  such an address, but for one that an operand already is, and for the
  end of the variable that an operand is in. A conversion of a pointer
  to one is not checked: nothing is known of it.

Measured on amd64, the size of `cmd/go` and of a program that prints a
line, as `go build` leaves them, each row with what the rows above it
have:

| | `cmd/go` | hello |
| --- | ---: | ---: |
| before | 105,141,289 | 2,727,476 |
| the checks themselves, and no table | 105,208,973 | 2,746,105 |
| the variables of frames whose address is taken (`-d=stackvars=1`) | 106,187,017, +0.9% | 2,759,030, +0.5% |
| and those that have pointers | 107,663,653, +2.3% | 2,771,502, +0.9% |
| the symbols of the data, their types, the byte after each | 108,654,869, +0.9% | 2,817,420, +1.7% |

In all 3.3% for both. The table of the data is 597,772 bytes of
`cmd/go`'s 991,216, for 74,718 symbols, most of them the bytes of
strings; the rest is the descriptors that are kept and the bytes
between.

The checks, in a function that is given a `*byte` to an array of 64
bytes and stores in each through `unsafe.Add`, which is two checks for
a byte, and in one that converts an `unsafe.Pointer` to a `*uint64`
that is kept (ns for each byte, and for each conversion):

| What is pointed to | before | after |
| --- | ---: | ---: |
| a byte of an array of the caller's frame | 10.7 | 35.6 |
| the same, from 8 frames further down | 10.8 | 55.8 |
| a byte of a global array | 20.3 | 25.0 |
| a byte of an object of the heap | 14.4 | 14.5 |
| a `uint64` of the caller's frame | 3.1 | 15.7 |
| a global `uint64` | 7.0 | 11.1 |
| a `uint64` of the heap | 11.4 | 12.1 |

That is with the variable found again among the last four of the P.
One that is not costs about 60 ns in a frame of the stack, whatever
the number of frames between; where the stack is unwound, about 110 ns
and 50 ns more for each frame between; and about 30 ns in the data.

### What this does not close

- **A frame of the runtime's, or of assembly**, has no table.
- **A parameter or a result that is in the frame of the caller** has no
  byte after it: where it is, is the calling convention's. The end of
  one whose address is taken is the start of the next, if that one is
  in the table too.
- **A variable without pointers whose address is not taken** is not in
  the table: no pointer is to it, and arithmetic gives none.
- **The variables that share a slot** (the compiler's `mergelocals`)
  are variables whose address is not taken. Such a slot is in the table
  if they are of one type.
- **A variable of the runtime's own data** has its size and no type.
- **A view of bytes that is only loaded through** (`checkptrView`) is
  not compared with the type of a variable, as it is with an object's:
  a string, a slice or an interface of a variable can be read in part
  through one. And a view of bytes is as long as its type says, in a
  variable as in an object: `(*[1<<30]byte)(p)` is a type that is
  longer than what it is over, and an index in it is not checked
  against the variable. (In a package that is C translated by cmd/cc
  it is: `cc.md`.)
- **arm64 and the others** unwind the stack for each address of it:
  only amd64 follows the saved frame pointers, and remembers the
  variables of the stack that it found.

What the standard library, the tools and their tests had:

- The adapter that the compiler writes for a method of a value that an
  interface holds inline (`noder`'s `ifaceAdapter`) converts the words
  of the value, in a temporary of its own, to the value's type. The
  runtime has the type of the temporary now and would refuse the
  conversion; it is the compiler's own, and is not checked.
- `crypto/internal/fips140/check` hashes whole sections of the program,
  which are many variables, through `unsafe.Slice`: the function that
  makes the slice is `//go:nocheckptr`.
- Two tests do on purpose what the checks stop.
  `runtime/slicetear_test.go` makes a slice whose capacity is more than
  its array's, to have a capacity of 2^32, and the runtime's
  `BadTraceback` test program writes over its own return address by
  arithmetic on the address of its argument: both are
  `//go:nocheckptr`.
- `cmd/link`'s `TestDeadcode` has that a live variable does not make
  its type's descriptor live: that is so with `-datavars=false`, and
  the reverse otherwise.

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

- **The stack and globals through a plain `unsafe.Pointer`** have their
  types now (above, "The variables of the stack and of the data"), and
  a conversion is compared with them, but for a view of bytes that is
  only loaded through.
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

## What is outside

The checks are of Go and of C that this compiler compiled. A program
is only as safe as they say if nothing in it is outside them:

- **Assembly.** A `.s` file does what it likes. The standard library's
  are part of what is trusted; a program's own are not checked.
- **`//go:linkname` to the runtime's own functions** (`runtime.memmove`,
  `mallocgc`, `typedmemmove` and the others that packages pull) calls
  them with whatever it passes.
- **cgo with a C compiler.** What gcc or clang compiled has no checks:
  C is safe here when cmd/cc translates it (`cc.md`), which is what
  `CGO_ENABLED=0` does for a package that imports "C".
- **`-d=checkptr=0`, `//go:nocheckptr`, and the packages that are
  compiled without the checks** (the runtime's, reflect). The standard
  library's uses are part of what is trusted.
- **Two goroutines that store to the same string, slice or interface
  at once** with nothing between them: Go does not define what a third
  one loads. (That is the work on values of two words that are stored
  whole, not this document's.)
- **A check and the store that it is for are two steps**: a goroutine
  that changes what the memory is between them is not seen.

## What it does not see

- What the gaps of "Compatible types" list: the types of a small
  object's pointers, stores through an `unsafe.Pointer` over a typed
  pointer, and bytes written over a pointer.
- A pointer one past the end of an object of the heap is, to these
  checks, a pointer to the next object. (The end of a variable of the
  stack or of the data is in no variable.)
- What "The variables of the stack and of the data" lists: the frames
  of the runtime and of assembly, and what is between the variables of
  a frame.

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
objects with a type, with a bitmap and for what the compiler knows),
`TestCheckPtrVars` for the variables of the stack and of the data
(`testprog/checkptr_vars.go`) and
`TestCheckPtrViews` for views (`testprog/checkptr_whole.go`),
`runtime/mwholebits_test.go` for the bitmap;
the standard library's tests pass with the checks on. Six tests of
`test/` that count instructions or diagnostics, or that misuse `unsafe`
on purpose, run with `-d=checkptr=0`.
