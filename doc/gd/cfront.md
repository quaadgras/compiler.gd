# C with typed capabilities: a prototype

`doc/gd/cfront` translates a subset of C to Go so that C and Go share one
heap, one stack, one collector and one pointer representation, and C
cannot break what Go relies on. It is a prototype of the memory rules,
not of the compiler: it emits Go source that the existing toolchain
compiles.

## What Go may rely on, with no checks

Plain Go code that uses C's memory through the generated mirror types
(`struct_node` for C's `struct node`), or that hands C its own memory,
may assume what it assumes of any Go memory:

- a word Go reads as a `*T` is nil, a pointer to a whole live `T`, or a
  word that faults (tagged, below);
- C never writes a word of a Go value with invariants (a `bool`, a string
  or slice header, an interface, map, chan or func), never writes an
  integer over a pointer word, and never writes outside what Go gave it.

C pays for all of it. Go pays nothing, and nothing has to be pinned or
kept on the heap for C's sake.

## The rules

**A C pointer in a register** is `ccap.Ptr{obj, size, off, ti}`: a
capability (the base, size and type of the memory it may reach) and an
offset, which may be anything. Arithmetic is never checked. Every access
is checked against the capability and its type.

**A C pointer in memory** is one word where the memory's type has a
pointer. If its address holds a whole value of the type the word points
to, the word is the address: a Go pointer. Otherwise (out of bounds, or
of the wrong type: an `int *` stored in a `struct node *` field) it is
the address with bit 55 set: not canonical, so it faults wherever Go
dereferences it, and the collector does not take it for a pointer. Its
capability is kept in a side table, so C can still use it as what it is.

**Capabilities come from types, never from the heap's guesses:**

- *C's objects* (`malloc`, globals, locals) begin with a header holding
  their type and exact size. A word that points into one gets the whole
  object back (`runtime.ccapObject` finds the object; the header is
  trusted only if its type descriptor is a registered one, which safe Go
  code cannot forge).
- *Go's pointers* get the capability of their static type, as cgo's rules
  say what C may reach: `ccap.FromGo(p)` the value `*p`, `ccap.FromSlice(s)`
  the slice's whole array. A C parameter declared as an array
  (`int a[]`) makes the generated wrapper take a slice.
- *A word loaded from Go's memory* gets the capability of the type the
  memory says it points to: a `*T` field gives one `T`. Go's own
  invariant makes that exact.
- *A capability a word cannot give back* (a slice's array stored in an
  `int *`, an out-of-bounds pointer) is kept, if the word is in C's
  object, in that object's side table: made when first needed, reached
  from the header, keyed by the word's offset, so that it moves with the
  object (on the stack too) and dies with it. It is found again only while
  the word is unchanged. Go's memory has no table: there such a
  capability is always dropped, and C gets back what the word's type
  gives (or nothing, for an out-of-bounds word).

**Stores keep Go's invariant**, and each decides from its own operands
only, so no check races with another thread:

- a scalar store may not write a pointer word or a word of a Go value
  with invariants;
- a pointer store may only write a pointer word, and stores a plain
  address only if the target holds a whole value of the pointee type
  (the same type, or for a type without pointers, bytes without any);
- a pointer made of an integer has no capability: it can be passed around
  and compared, not dereferenced.

**Types, not the heap, answer every check**, so C's objects and Go's may
be on the stack. Each C local is a Go variable of type `ccap.Obj[T]`;
escape analysis keeps it on the stack unless C stores its address
(`StorePtr` makes what it stores escape), exactly as it does for Go. A
Go caller's stack variables passed to C stay on the stack too: the demo
measures no allocations for either.

## What the demo shows (`go test` in doc/gd/cfront)

`testdata/basic.c` (structs, typedefs, enums, switch, static locals,
pointer arithmetic, strings, malloc) prints the same as gcc's build.

`demo/interop` (from `testdata/interop.c`):

```
C's list in Go: 1 2 3 4 5
Go's list summed by C: 60
100000 nodes, after 3 collections: Go 5000050000 C 5000050000
Go's stack pair summed by C: 3
C's locals: 213
allocations per call: sumPair 0 locals 0
30 C reads arr[2] of Go's slice                 ok
C reads arr[3] of Go's slice                 trapped: ccap: access of 4 bytes out of bounds of {int32, size 12, off 12}
word C stored past the end: 0x8019c3ad208028
Go dereferences it                           trapped: runtime error: invalid memory address or nil pointer dereference
30 C brings it back in bounds (arr[2])          ok
C dereferences it out of bounds              trapped: ccap: access of 4 bytes out of bounds of {int32, size 12, off 24}
...stored in Go's object instead             trapped: ccap: access through a pointer with no capability
60 C one-based indexing                         ok
C writes an integer over a pointer word      trapped: ccap: scalar store over a pointer word
C dereferences an integer                    trapped: ccap: access through a pointer with no capability
C hides a pointer in bytes                   trapped: ccap: pointer store into a word the collector does not scan
C puns a pointer word of a stack local       trapped: ccap: scalar store over a pointer word
int* stored in a struct node* field: 0x8019c3ad208060
Go follows it                                trapped: runtime error: invalid memory address or nil pointer dereference
42 C uses it as the int it is                   ok
C writes a plain field of a Go value         ok
C writes the length of a Go slice            trapped: ccap: store into a word of a Go value with invariants (main.goThing)
C writes past a Go value                     trapped: ccap: access of 8 bytes out of bounds of {main.goThing, size 32, off 32}
Go's value after: 7 2
```

## Escape analysis and the Ptr

Escape analysis sees a `Ptr` as one value: if anything reachable from it
leaks, so does everything its memory points to. So a `Ptr` holds its type
descriptor as an integer (descriptors live for ever), and fault messages
are built from the type alone; otherwise every check would move what C's
and Go's stack values point to into the heap.

## What it does not do yet

- **Go's fault on a tagged word is fatal** (`unexpected fault address
  0x0`): the kernel reports a non-canonical address as `SI_KERNEL` with no
  address, which `sigpanic` does not turn into a panic. Safe, but the demo
  needs `debug.SetPanicOnFault`. The runtime could treat that fault in Go
  code as `panicmem`.
- **Bit 55 is not portable**: 32-bit targets have no such bit, and LA57,
  s390x and amd64's LAM treat it differently. The tag should be a hook per
  architecture, with a reserved unmapped region where no faulting bit
  exists.
- **The side table** is a map under a mutex per object; an array of
  capabilities per word would be faster.
- **Unions and untyped memory** (Godot's `Variant`, placement `new` in a
  byte pool) are not supported: every word is statically a pointer or
  not. The plan is the `GOWASM=gc,split` rule on native: such words are
  bytes to Go and the collector, and their capabilities live in the
  object's array, trusted while the word still lies within them.
- **Storing a local's address always moves the local to the heap**, even
  into another local, where Go itself would keep both on the stack:
  `StorePtr` is opaque to escape analysis. Lowering into SSA fixes it.
- **Every access is a call** that reads type tables, and each pointer
  load and store asks the heap for C's object; nothing is eliminated
  statically yet (most stores' types are known, as in CCured). Each C
  object has a 24-byte header.
- **Language:** no unions, bitfields, function pointers, `goto`, variadic
  definitions, struct parameters or results, compound literals, or
  function-like macros; `#include` is ignored and a few libc functions are
  built in (`printf`, `malloc`, `memcpy`, `strlen`...).
