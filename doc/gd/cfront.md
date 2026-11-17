# C with derived capabilities: a prototype

`doc/gd/cfront` translates a subset of C to Go so that C and Go share one
heap, one collector and one pointer representation, and C cannot break
what Go relies on. It is a prototype of the memory rules, not of the
compiler: it emits Go source that the existing toolchain compiles.

## The rules

Go needs one thing of a word it reads as a pointer: that it is an address
in a live object (or nil). C gets Fil-C's guarantees without Go's pointers
carrying anything.

- **In a register** a C pointer is `ccap.Ptr{obj, size, off}`: the base and
  size of the object it came from (its capability: Fil-C's capability
  object reduced to the object's base, since the heap knows its size) and
  an offset, which may be anything. Arithmetic is never checked; every
  access is checked against the capability.
- **In memory** a C pointer is one word in a word the collector reads as a
  pointer, as Go lays it out. If its address is in its object, the word is
  the address: a Go pointer. If not, the word is the address with bit 55
  set: not canonical, so it faults wherever Go dereferences it, and the
  collector does not take it for a pointer. Its capability is kept in a
  side table, so C can bring it back in bounds.
- **Capabilities are derived, not stored.** When C loads an untagged word,
  or Go passes it a pointer, the capability is the heap object that
  contains the address (`runtime.ccapObject`: span, size class, allocation
  header). That is sound because every untagged pointer word points into
  a live object: Go's invariant, which every store of C keeps.
- **Stores keep the invariant**, and each decides from its own operands
  only, so no check races with another thread:
  - a scalar store may not write a word the collector reads as a pointer
    (`runtime.ccapIsPtr`, from the object's type);
  - a pointer store may only write such a word (a pointer hidden in bytes
    would be invisible to the collector);
  - a pointer made of an integer has no capability: it can be passed
    around and compared, not dereferenced.
- **Allocations are typed.** `malloc` takes its type from what its result
  is converted to (`struct node *n = malloc(...)` allocates
  `[]struct_node`), so the collector knows its pointer words. Untyped
  allocations (`char *`, `void *`) have none. Address-taken locals,
  arrays, structs and globals are heap objects. `free` does nothing.

Each C struct has a Go mirror type (`struct_node`), with Go pointers where
C has pointers. Go code uses C's memory through it with plain field
loads and stores, and each C function `f` has a wrapper `C_f` that takes
and returns Go pointers.

## What the demo shows (`go test` in doc/gd/cfront)

`testdata/basic.c` (structs, typedefs, enums, switch, static locals,
pointer arithmetic, strings, malloc) prints the same as gcc's build.

`demo/interop` (from `testdata/interop.c`):

```
C's list in Go: 1 2 3 4 5
Go's list summed by C: 60
100000 nodes, after 3 collections: Go 5000050000 C 5000050000
30 C reads arr[2] of Go's slice                 ok
0 C reads arr[3] (inside the 16-byte size class) ok
C reads arr[4] of Go's slice                 trapped: ccap: access of 4 bytes out of bounds of {base 0x3416fd4da110, size 16, off 16}
word C stored past the end: 0x803416fd4da128
Go dereferences it                           trapped: runtime error: invalid memory address or nil pointer dereference
30 C brings it back in bounds (arr[2])          ok
C dereferences it out of bounds              trapped: ccap: access of 4 bytes out of bounds of {base 0x3416fd4da110, size 16, off 24}
60 C one-based indexing                         ok
C writes an integer over a pointer word      trapped: ccap: scalar store over a pointer word
C dereferences an integer                    trapped: ccap: access through a pointer with no capability
C hides a pointer in bytes                   trapped: ccap: pointer store into a word the collector does not scan
```

## What it does not do yet

- **Bounds are the size class.** A derived capability covers the whole
  allocation slot: `arr[3]` of a 3-element `[]int32` is inside its 16-byte
  slot and is not caught. Capabilities C makes itself (`malloc`, `&x`) are
  exact. Recording requested sizes, at least for C's allocations, would
  close it.
- **Go's fault on a tagged word is fatal** (`unexpected fault address
  0x0`): the kernel reports a non-canonical address as `SI_KERNEL` with no
  address, which `sigpanic` does not turn into a panic. Safe, but the demo
  needs `debug.SetPanicOnFault`. The runtime could treat that fault in Go
  code as `panicmem`.
- **The side table** of tagged words' capabilities is one map keyed by the
  word's address, under a mutex, whose entries are never freed. It belongs
  in the allocation (an array of capabilities per C allocation that has
  pointers, as in Fil-C), which also keeps a tagged word's object alive.
  A tagged word Go copies loses its capability (C can then compare it but
  not use it).
- **Every access is a call**, and every scalar store asks the runtime
  whether it writes a pointer word. Most of these checks can be removed
  statically (stores whose type matches the allocation's), as CCured does.
- **Source to source.** Lowering to the compiler's IR or SSA would let the
  checks be inlined and eliminated by `prove`.
- **Language:** no unions, bitfields, function pointers, `goto`, variadic
  definitions, struct parameters or results, compound literals, or
  function-like macros; `#include` is ignored and a few libc functions are
  built in (`printf`, `malloc`, `memcpy`, `strlen`...). Go globals passed
  to C have no capability (they are not in the heap).
