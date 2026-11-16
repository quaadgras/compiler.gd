# Wasm GC: spike results (2026-11-15)

Question: would compiling Go to WebAssembly GC objects, instead of linear
memory with Go's own allocator and collector, be worth it for this fork?

Short answer: it is feasible, and fast, in a hybrid design: one typed wasm
struct per Go object, pointers as single references cast on use (interior
pointers boxed), a (type, id) header, and pointer-free slices kept in
linear memory. A hand-translated program runs 2.4x to 8.7x faster than the
fork's js/wasm output, using more memory and with longer worst-case
pauses. A "universal" representation that keeps every Go pointer
semantic costs about 5x on pointer chasing and is not worth pursuing. The
sections below are in the order the spike went.

## What was measured

Hand-written wasm (`bench/wasmgc/gc.wat`, driver `run.mjs`), Node.js 24
(V8 13.6), best of 5. "Linear" is what the fork emits today in the best
case: i64 loads and stores, 64-bit pointers wrapped to i32, Go's bounds
checks. Build and run:

    wasm-as --enable-gc --enable-reference-types gc.wat -o gc.wasm
    node run.mjs

Three wasm GC representations:

- **Typed**: a Go struct is a wasm struct, a slice is a wasm array, a pointer
  is a reference. No interior pointers: `&s.f` and `&a[i]` have no
  representation, and neither does `unsafe`.
- **Flat words**: a slice of structs is one `array i64` and `&s[i]` is
  (array, index).
- **Universal**: every Go object is `{words: array i64, refs: array ref}` and
  every pointer is (object, word index); a pointer stored in memory has its
  object in `refs[i]` and its index in `words[i]`. This keeps interior
  pointers and most of `unsafe` working. Bytes are packed 8 to a word.

## Results

Access (time relative to linear memory, lower is better):

| Workload | Linear | Typed | Flat words / universal |
|---|---|---|---|
| Linked list traversal, 1e6 nodes x50 | 52 ms | 1.1x | universal: 4.8x |
| `[]int64` increment and sum | 41 ms | 1.0x | — |
| `[]struct` field update through `&s[i]` | 33 ms | 1.2x (boxed elements) | flat words: 1.4x |
| Sieve over `[]bool` | 44 ms | 1.35x (`array i8`) | bytes in words: 2.4x |
| `uint64` read at every offset of a `[]byte` | 16 ms | 6.1x (`array i8`) | — |

Allocation:

| Workload | Go allocator, fork on js/wasm | Typed wasm GC | Universal |
|---|---|---|---|
| 2e7 short-lived 16-byte objects | 31 ns each | 2.0 ns each | 4.9 ns each |
| Build a 1e6-node list (retained) | 69 ms (33 ms with GOGC=off) | 39 ms | ~110 ms |

For reference the fork's native amd64 allocator takes 12 ns per object, and
traversing the same list in Go on js/wasm takes 52 ms, the linear figure.

Size: the collector and allocator are about 0.17 MB of a 3.56 MB code
section (4.7%) in a json+fmt program, which is what wasm GC could remove;
two-word pointers and reference casts would add code elsewhere.

## Reading

- Engine allocation is 6x to 15x faster than Go's allocator on wasm.
  Allocation-heavy code would gain a lot. (Collection pauses were not
  measured.)
- Typed objects cost 0% to 35% on access, except unaligned multi-byte reads
  from byte arrays (6x): wasm GC has no multi-byte array access, which
  hits `encoding/binary`, hashing, string comparison and the like.
- Typed objects cannot hold Go semantics: any `&x.f`, `&a[i]`, `unsafe`
  conversion or type-punned memory needs another representation.
- The universal representation, which can, pays 4.8x on pointer chasing
  (two array reads and a cast per pointer load) and 2.4x on bytes.
- Nothing lives on the Go stack any more: the fork's stack-allocated
  returns, inline interface payloads and 24-byte strings are all designs
  for linear memory, and would have to be redone or dropped.

## Tagged pointers (follow-up)

The universal model is slow because one Go object becomes three wasm
objects (a struct and two arrays), and a traversal touches all of them.
Variants of the same list traversal, relative to linear memory:

| Pointer representation | Wasm objects per Go object | Traversal |
|---|---|---|
| Typed reference, no interior pointers | 1 | 1.1x |
| (reference, tag), whole-object pointer: tag check, cast, field access | 1 | 1.5x |
| (reference, tag), interior pointer: call through a per-field accessor | 1 | 2.7x |
| (words array, refs array, offset): the pointer carries both arrays | 2 | 3.4x |
| Universal: (object, word index) | 3 | 4.8x |
| Universal with a typed reference array and byte offsets | 3 | 5.9x |

So the workable design keeps one typed wasm struct per Go object and tags
the pointer: tag 0 means "the whole object, of the pointer's static type",
anything else names a field and a byte inside it and is dereferenced
through an accessor. The field's size does not need to be in the tag: the
pointer's static type gives it, except for `unsafe.Pointer`. A stack bit
is only meaningful if stack frames stay in linear memory, and those cannot
hold references, so frames with pointers would be wasm GC objects as well
(or their pointers go through a table of references, which pins what they
point to).

Still unsolved in that design: `unsafe` and type punning, unaligned
multi-byte reads from byte arrays (6x), and slices of structs, whose
elements are either boxed (1.2x, and `&s[i]` is then a whole-object
pointer) or flat words (1.4x, every element pointer interior).

## Pointer-free data in linear memory (follow-up)

Holding a JavaScript typed array from wasm (an externref) and reading it
through an import costs 5.2 ns per byte against 0.31 ns in linear memory
(17x; `bench/wasmgc/fin.wat`, `fin.mjs`). But linear memory already is a
JavaScript ArrayBuffer, so the backing store of slices whose elements hold
no pointers (`[]byte`, `[]float32`, `[]Vertex`) can stay there: accesses
are plain loads, multi-byte reads and type punning between such slices
work as today, and WebGL/WebGPU get a zero-copy view.

Its lifetime then hangs on a wasm GC object, a handle that every slice
header into the block references. V8 runs a FinalizationRegistry callback
for dead wasm GC objects (1000 of 1000 in the test), which can free the
block; allocating a handle and registering it costs about 113 ns, against
5 ns for the handle alone, so it suits buffers, not small values. Short
strings need none of it with the fork's inline strings.

## A whole program in the hybrid design (follow-up)

`bench/wasmgc/hybrid/goprog/main.go` is a small program: binary trees
(about 15 million allocations, pointer chasing) and 2000 frames of a
game-like tick over 20000 entities (pointers between entities, 2000
short-lived events and 200 replaced entities per frame, positions written
to a `[]float32`). `hybrid.wat` is the same program hand-translated to the
hybrid design: one typed wasm struct per Go object with a (type, id)
header, a pointer as a single `(ref null struct)` cast on dereference
(an interior pointer would be a box holding the object and the field;
the program takes none), `[]*T` as a wasm GC array, `[]float32` in linear
memory. `linear.wat` is a control: the same program hand-written against
linear memory with a bump allocator that frees nothing, i.e. ideal code
with a free allocator and no collector. All three print the same
checksums. Node.js 24, two runs each:

| | Trees | Frames, total | Frame median / p99 / max | Peak RSS |
|---|---|---|---|---|
| Fork, js/wasm | 1027-1099 ms | 876-936 ms | 0.37 / 0.81 / 1.2 ms | 85 MB |
| Stock go1.27, js/wasm | 1361-1407 ms | 1020-1037 ms | 0.42 / 1.0 / 1.4 ms | 87 MB |
| Hybrid wasm GC, hand-written | 118-125 ms | 369-375 ms | 0.17 / 0.27 / 2.6-3.5 ms | 140 MB |
| Linear, hand-written, no collection | 90-91 ms | 442-457 ms | 0.23 / 0.25-0.32 / 0.6-0.9 ms | 173 MB |

(An empty Node.js process peaks at 49 MB.)

- The hybrid design costs little against ideal linear code: 1.3x on the
  trees, where the control allocates for free and never collects, and it
  is faster on the frames.
- Against what the fork really emits it is 8.7x faster on the trees and
  2.4x on the frames. The trees are the allocator and collector (Go's
  takes about 31 ns per object here, the engine's about 2 ns). On the
  frames about half the gap is code generation, not the memory model: the
  hand-written control is 2x faster than the fork's output too.
- It uses more memory: about 91 MB above the empty process against 36 MB.
- Its worst frame is longer: 2.6 to 3.5 ms against 1.2 ms. Median and p99
  are lower.
- The hand-written programs have no goroutines, no stack checks and no
  explicit bounds or nil checks (wasm GC traps on both by itself; the
  linear control checks the entity index and the target pointer).

## What it would take

A new backend and a new runtime: object layout, pointer representation,
maps, channels, reflect, strings and slices, the write barrier (gone),
stack maps (gone), `unsafe` (restricted). Goroutine switching (JSPI) and
structured control flow, done on 2026-11-15, carry over unchanged.

## Recommendation

The first conclusion here, from the universal model, was not to move the
fork's memory model to wasm GC. The hybrid design changes that: with one
typed struct per object, single-reference pointers that are cast on use,
boxed interior pointers, a (type, id) header for reflect, hashing and the
type-punning slow path, and pointer-free slices left in linear memory,
access is about as fast as linear memory and allocation is an order of
magnitude cheaper. What it costs is memory, longer worst-case pauses that
the program cannot control (the fork's aim of running with the collector
off has no equivalent), and the work: a new backend and most of a new
runtime. Whether that is worth it is a question of effort, not of
feasibility.

## Implementation (started 2026-11-15)

`GOWASM=gc` selects the hybrid design in the real toolchain. It is a
first stage: enough of the compiler, assembler, linker and a new runtime
to build and run the reference program above (`bench/wasmgc/compiled`,
which is `hybrid/goprog` without the packages `time` and `sort`). The go
command cannot build for it yet; `compiled/build.sh` drives the compiler
and linker directly.

| Node 24                          | trees      | 2000 frames | frame median / p99 / max  | peak RSS |
|----------------------------------|-----------:|------------:|---------------------------|---------:|
| fork js/wasm (linear memory)     | 1027–1099 ms | 876–936 ms | 0.37 / 0.81 / 1.2 ms      | 85 MB    |
| `GOWASM=gc`, compiled, stage 1   | 141–163 ms | 740–774 ms  | 0.37–0.38 / 0.46–0.59 / 2.1–3.0 ms | 147 MB |
| `GOWASM=gc`, compiled, stage 2   | 153–168 ms | 849–881 ms  | 0.43–0.44 / 0.58–0.64 / 2.1–3.2 ms | not measured |
| `GOWASM=gc`, compiled, stage 3   | 192–210 ms | 826–948 ms  | 0.42–0.44 / 0.61–0.69 / 2.8–5.5 ms | not measured |
| hybrid, written by hand          | 118–125 ms | 369–375 ms  | 0.17 / 0.27 / 2.6–3.5 ms  | 140 MB   |

The checksums are those of the reference. The module is 21 KB (29 KB
at stage 2, with the accessors). The allocation-bound half is within
20-30% of the hand translation; the frame loop is twice as slow as it,
for reasons that are all in the list of what is not done (casts repeated
on every access, the slice header read from its global on every index,
bounds checked twice). Stage 2 made every access through a pointer able
to fall back to an accessor, which cost the frame loop about 15%: it is
now where the linear-memory build is. `compiled/pointers.go` exercises
what stage 2 added and prints what the native build prints.

Size, for the same program (the linear-memory build is `hybrid/goprog`,
which also links `time` and `sort`):

|                         | linear memory | `GOWASM=gc` |
|-------------------------|--------------:|------------:|
| module                  | 1,862 KB      | 29 KB       |
| gzip                    | 539 KB        | 7.5 KB      |
| `(*node).check`         | 281 B         | 90 B        |
| `bottomUp`              | 472 B         | 126 B       |
| `trees`                 | 611 B         | 220 B       |
| `frame`                 | 3,117 B       | 3,412 B     |
| accessors, start        | —             | 3.7 KB      |
| runtime functions       | (most of it)  | 3.5 KB      |

The module total says little: the runtime for `GOWASM=gc` is nearly
empty. The functions say more. Code that allocates and follows pointers
to whole objects is a third of the size (no stack frame, no write
barrier, no arguments stored to memory). Code that reads and writes
through pointers that might be something else, like `frame`, is 10%
larger for now: each access carries the call of an accessor.

At stage 3 the same program is 165 KB (62 KB gzip): 42 KB of code the
linker generates (23 KB of it the start function that builds the global
variables, the rest the accessors), 25 KB of `internal/strconv`, 24 KB
of runtime, 62 KB of data. Two things in that are waste: a global
without pointers is built by code *and* kept in the data section (it
should simply stay in linear memory), and the tables for tracebacks are
emitted though nothing reads them. The trees half got 25% slower
between stages 2 and 3, for a reason not found yet.

How it is put together:

- **Types by name** (`cmd/internal/obj/wasm/gc.go`). A WebAssembly type
  is a symbol whose name spells it: `wasm.struct.r0,l8,l16`,
  `wasm.array.f`, `wasm.func.lr.r`. The compiler needs no type table and
  the linker defines each type it sees a reference to. A struct's name
  is its Go layout: each scalar and its offset, and nested arrays with
  their element size and count. Every struct is a subtype of the
  two-word header, whose first word is the layout (the index of the
  type the object was created as); every field is mutable; every
  reference is an `eqref`, cast where it is used.
- **Layout** (`cmd/compile/internal/wasm/gc.go`). Go's layout is kept as
  the *name* of a place: the generic SSA still computes addresses with
  `OffPtr`, `PtrIndex` and `AddPtr` (their lowering to arithmetic is
  switched off), and the backend turns an address used by a load or
  store into an object and a field or element, or into a pointer and a
  byte offset. A struct is flattened to its scalars; an array of two or
  more elements is a WebAssembly array, its own object even when nested
  in a struct; elements are scalars, or references to one object per
  element.
- **Pointers** are one of five things: an object (a pointer to the whole
  of it); a box of an object and a byte offset (a pointer into it); a
  pointer to linear memory with the object that owns the block; an `i31`
  (the address of static data, or of a variable on the stack); or null.
  Code reads and writes through a pointer on the assumption that it is
  what it most likely is — an object of the pointee type for a `*T`,
  linear memory for an element of a slice without pointers — and
  otherwise calls an accessor with the pointer and the byte offset.
- **Accessors** (`cmd/link/internal/wasm/gchelpers.go`). The linker
  generates a load and a store for each kind of scalar, and pointer
  addition. They handle every kind of pointer; for an object they
  switch on the layout in its header and then on the offset, so they
  know every layout in the program. This is what makes `&s.f`, methods
  on embedded structs, `unsafe.Pointer` and the generated equality
  functions work.
- **Linear memory** holds what has no pointers and that the host may
  want as bytes:
  - the arrays of slices whose elements have no pointers (`[]byte`,
    `[]float32`, `[]Vertex`), from an allocator in the runtime
    (`runtime/wasmgc/linear.go`: power-of-two blocks, a free list per
    size). The pointer that `make` returns owns the block; pointers
    derived from it keep it alive; a `FinalizationRegistry` in the host
    frees the block when the owner is collected;
  - the variables whose address is taken and that have no pointers, in
    the function's frame on a stack there. The compiler only leaves a
    variable on the stack if no pointer to it outlives the function, so
    `&x` is just the address, and costs nothing. (A variable with
    pointers whose address is taken is an object allocated on entry.)
  - string constants, type descriptors and itabs, where the linker put
    them.
- **Values.** Each SSA value is a WebAssembly local, a constant, or an
  operand left on the stack for its only use; register allocation is
  skipped. Arguments and results are WebAssembly parameters and results
  (the register ABI with 120 registers of each kind).
- **Strings and interfaces.** The fork's inline strings and interface
  payloads need nothing: they are already scalars in SSA.
- **Linker** (`cmd/link/internal/wasm/gc.go`). Each data symbol the code
  uses as an object becomes a global; the module's start function
  creates the objects from the symbols' data and relocations (so
  statically initialized data, including pointers between symbols,
  works, and unreachable data is still dropped). Constant arrays come
  from a passive data segment with `array.new_data`.
- **Runtime** (`src/runtime/wasmgc`, tagged `wasm.gc`): the linear
  allocator, `growslice`, printing, and panics that stop the program.
  No collector or stack-growth code exists or is needed.

### Stage 3: the language runtime

`compiled/tests` holds a program for each of the following; each prints
what the native build prints (`compiled/test.sh`).

- **Package initialization.** The linker calls the init functions, in
  dependency order, before `main`.
- **Closures.** A function value is an object whose first word is the
  function (an index in the table). A static one has no Go type: the
  linker makes it an object of words.
- **Strings** (`runtime/wasmgc/string.go`). Up to 15 bytes are in the
  words, as on every target of the fork; longer ones are in linear
  memory, owned like a slice's array, and constants stay where the
  linker put them. The runtime functions are written in plain Go over
  `s[i]` and `len(s)`.
- **Interfaces** (`iface.go`, `type.go`, `alg.go`). Type descriptors
  and itabs are in linear memory and are read through pointers that are
  addresses, so `internal/abi` is used as it is. Itabs for assertions
  to interface types are built at run time, in linear memory, from the
  module data the linker fills in (which is an object: the runtime
  declares `firstmoduledata` with the layout the linker writes). The
  two payload words of an inline or spread value are floats in SSA and
  in objects; a method call through an interface passes a pointer to a
  staged copy rather than the words, because a WebAssembly function
  cannot be called with arguments it does not declare.
- **Defer, panic, recover** (`panic.go`). Deferred calls are in a list
  in the runtime, each with the address of the frame (on the stack in
  linear memory) of the function that deferred it; open-coded defers
  are off. A panic runs them innermost first. If one recovers, the
  runtime throws a WebAssembly exception; every function that defers
  is wrapped in a try block, and the one whose frame the deferred call
  belonged to runs its remaining deferred calls and returns, while the
  others rethrow.
- **Slices of structs with pointers**, `[]string` among them, are
  arrays of one object per element (`wasm.array.o`). `make` and
  `append` copy the zero value or the old elements with `wasm.clone`,
  which the linker generates.
- **Maps** (`map.go`, `hash.go`). A new table with open addressing; the
  keys and the elements are in a block of linear memory if they have
  no pointers, and objects otherwise. The compiler passes the zero
  value of an element that is an object (a variable per type that the
  linker creates), and does not use the variants for kinds of keys or
  maps on the stack. Pointers are hashed by identity: `wasm.ptrid`
  assigns one in the header of the object on first use.
- **Bytes of anything.** An integer can be loaded from where there is
  another scalar (`wasm.bits`): part of a wider integer, the bits of a
  float, the identity of a pointer. That is what lets the equality and
  hash functions the compiler generates, and `memequal` and `memhash`,
  work on objects.
- **The standard library.** `internal/goarch`, `math/bits`,
  `internal/abi` and `internal/strconv` compile as they are; `println`
  of a float goes through `strconv`. A function that uses something not
  supported yet (a parameter that is an array, for one) is compiled to
  a trap, with a note, so that the rest of its package can be used.
- **The fork's stack-allocated results** (Phase G) are off: there is no
  stack that a result with pointers could be written to.

### Stage 4: the test suite

The aim is the language tests of `$GOROOT/test`. `conformance/run.py`
builds and runs every `// run` test there (1005 of them) and writes
`conformance/status.txt`: a line per test and the failures by reason.
It takes four seconds.

| 2026-11-15                      | pass | fail | skipped |
|---------------------------------|-----:|-----:|--------:|
| at the start of the stage       | 386  | 199  | 420     |
| at the end of the stage         | 512  | 73   | 420     |

The skipped tests import a package that does not build yet: `fmt` in
234 of them, `os`, `reflect`, `strings`. That is now the largest
obstacle, ahead of anything in the language.

What the stage added:

- **Goroutines** (`runtime/wasmgc/proc.go`). Each runs on a
  WebAssembly stack of its own that the host makes (JSPI): the host
  calls `gorun` to start one; a goroutine that blocks calls the
  suspending import `park`; before suspending it, and when a goroutine
  ends, the host calls `sched` for the one to run next. The scheduler
  is a run queue in Go; there is no stack switching code. A goroutine
  has 64 KB of linear memory for its variables without pointers; the
  deferred calls and the panics are per goroutine. A deadlock is
  reported as on other targets.
- **Channels and select** (`chan.go`), written from scratch: there is
  one thread, so no locks. The elements are in linear memory or are
  objects, as in a map; a value is copied between objects with
  `wasm.copy`, which the linker generates.
- **Nil pointers panic.** The nil checks are all kept (the pass that
  removes those a fault would catch is off) and call `panicnil`.
- **The runtime's API**: `GC`, `Gosched`, `KeepAlive`, `Caller` and the
  like, mostly as stubs; type assertion errors are the real
  `TypeAssertionError`; division of the most negative integer by -1.
- **Generics.** Dictionaries stay in linear memory. Read-only static
  data is no longer merged by content: a `[5]byte` and a string with
  the same bytes are different objects.

Not done, roughly in the order planned:

1. *Pointers, the rest.* Pointer to integer (identity, for hashing and
   `%p`). Comparing two pointers into the same object, which are two
   boxes. Nil dereference is a trap, not a panic. A slice of an array
   that is an object (a field) cannot be appended to or copied from
   yet. Moving or zeroing an array of more than 64 scalars that is not
   on the stack assumes it is an object. The stack in linear memory is
   one, of 1 MB, unchecked.
2. *The language runtime, the rest.* `copy` of a slice of structs
   with pointers; channels and `select` (with goroutines); `reflect`;
   parameters and results that are arrays; functions with irreducible
   control flow; `Goexit`; `recover` is honoured anywhere under a
   deferred call, not only in the deferred function itself; NaN keys
   and random iteration order in maps; a store of bytes into an object
   (only loads go through `wasm.bits`); runtime errors are plain
   values, not the types of the real runtime.
3. *Goroutines, the rest*: timers and `time.Sleep`, `Goexit`,
   `sync`, `syscall/js`; a goroutine's stack in linear memory is fixed
   and unchecked.
4. *The go command and the standard library*: selecting this runtime,
   and every package that uses `unsafe`.
5. *Performance*: typed locals instead of casts, the accessor fallback
   only where a pointer can be something else, slices not boxed when
   re-sliced, memory freed without waiting for the host's finalizers (a
   program that never returns to the event loop never frees a slice).

### Stage 5: the standard library (2026-11-16)

The standard library now builds and runs with `GOWASM=gc`, on
`GOOS=wasip1` (whose system calls are plain imports; `js` needs
`syscall/js` and asynchronous callbacks, which are not done). `fmt`,
`os`, `strings`, `strconv`, `reflect`, `time`, `sync`, `sort`,
`regexp`, `encoding/json`, `text/template`, `math/big` and the rest of
what the tests import compile, and `std/build.py` builds a program
and the packages it needs, as `go build` will (`compiled/build.sh`
and `conformance/run.py` use it). The host (`compiled/run.mjs`)
provides WASI with node's `node:wasi`. With `GOWASMGC_TRACE=1` it
prints the WebAssembly stack when the program exits with an error.

| conformance (`// run` tests)    | pass | fail | not built / excluded |
|---------------------------------|-----:|-----:|--------:|
| end of stage 4                  | 512  | 73   | 420     |
| end of stage 5                  | 902  | 54   | 9 + 40 excluded by `//go:build` |
| end of stage 6                  | 908  | 48   | 9 + 40 excluded by `//go:build` |

What the stage added, by layer:

- **Build tags.** `GOWASM=gc` sets the tag `wasm.gc`, which selects
  the pure Go versions of what has assembly for wasm (`internal/bytealg`,
  `math`, `math/big`), the runtime's own maps in `reflect`
  (`map_wasmgc.go`), and the code below in `reflect`.
- **Types known at run time.** The linker numbers the constructor of
  every type descriptor of the program and writes the number into the
  descriptor's `GCData` word, which nothing else needs here;
  `wasm.new` makes an object by that number. With it the runtime has
  `unsafe_New`, `newarray`, `typedmemmove` and `typedmemclr` (by the
  scalars of the type, which `typeScalars` walks), and the compiler
  falls back to them for types it does not know (from dictionaries).
  The layout names now end in the Go size (`r0,l8,l16/24`), so the
  accessors can step into arrays of objects, and stores of bytes into
  wider fields read, modify and write them (`wasm.storebits`; zeros
  stored over a pointer clear it).
- **Pointers.** `wasm.ptreq` compares two pointers by where they point
  (an i31 and a `GCLinear` of the same address, two boxes of the same
  field); 0 converts to nil; an array gets an identity from the host
  (`gd.objid`, a `WeakMap`). Adding to a pointer into an array nested
  in a struct gives a pointer into that array, so slices of a field
  work. Calls whose arguments the ABI puts on the stack (arrays larger
  than the registers) pass them as objects, by reference, and arrays
  up to 255 registers are passed element by element.
- **The runtime**: the hooks of `sync` (semaphores, notify lists),
  `sync/atomic`, `time` (timers, `Sleep`), `os`, `syscall`,
  `internal/poll` (no poller: files block), `reflect` and
  `internal/reflectlite`, `internal/godebug`, `runtime/debug`;
  `Goexit`; `deferprocat` (defers in range-over-func bodies); the real
  bounds error messages; `makeslice` errors in the right order.
- **reflect**: `Value.Call`, methods and `MakeFunc` go through code
  the linker generates for each func type of the program
  (`cmd/link/internal/wasm/gcreflect.go`): an adapter loads each
  register of the arguments through the accessors and calls through
  the table; a `MakeFunc` stub makes an object of each argument and
  calls `reflect.makeFuncCallGC`. `FuncOf` makes func types in linear
  memory.
- **recover** recovers only in the function the panic called: a
  function that calls others has a frame on the linear stack (8 bytes
  if nothing else), and `gorecoverfp` compares where its caller's
  frame ends with where the panic made the call. Wrappers have no
  frame, unless a function was inlined into them, and their own
  recover recovers only for a deferred call of a function the panic
  called (`defer recover()` in a deferred function).
- **The stack in linear memory grows**: a function whose frame would
  go below the limit (`GCGlobalStackLo`) gets a segment of its own
  (`runtime/wasmgc/stack.go`); what the host calls runs on a system
  stack. The host sets JSPI stacks to 8 MB.
- **wasip1 system calls** take addresses in linear memory: a pointer
  argument of an import from a module other than `gd` is passed as
  its address, and `syscall` copies a buffer that is in an object to
  linear memory.

Left, by reason, of the 54 failures:

- About 20 test what this design does not have: `runtime.Caller`,
  `Stack` and line numbers (no stack to walk), finalizers and when the
  collector runs, memory profiles, the `go` command and files in
  `/tmp`.
- `reflect`: `Type.Method(i).Func.Call` (its func type is made at run
  time, so no adapter); `StructOf` and `ArrayOf` with layouts the
  program does not have; `recover` in a function called by
  `reflect.Value.Call` from a deferred call.
- A few compiler cases: one irreducible function, a copy of a slice of
  arrays in linear memory read through an array of objects
  (`ken/complit.go`), unsafe tricks with zero-size objects.

Size: the benchmark program is 262 KB (80 KB gzipped), up from 186 KB:
the constructors of all the types, and the new helpers, are linked in
as soon as anything may allocate a type known at run time.

### Stage 6: first optimizations (2026-11-16)

Measured on the benchmark (`compiled/main.go`), the frame loop spends
its time in the function itself, not in the accessors: what costs is
the cast before every field access (a `br_on_cast_fail` per load and
store, nine for one entity per iteration).

- **Typed locals.** A pointer that is the base of more than one
  access, or a new object, gets a second local of type
  `(ref null $T)`, set once where the pointer is set (null if it does
  not point to a `$T`); an access checks that local for null
  (`br_on_non_null`) instead of casting. Locals of reference types are
  a new kind of local declaration in the object file.
- **Typed globals.** The global that holds the object of a package
  variable is declared with the object's type, so the cast where it is
  read is one the engine drops.
- **Allocation fused with initialization.** The stores that set the
  fields of a new object before anything else can see it (as
  `&node{l, r}` compiles to) go into the `struct.new`, which saves a
  cast and a write barrier per field.
- A pointer that is the base of accesses to linear memory keeps the
  address in a local (no measurable change here: the slice header is
  read from its global in each iteration).
- The frames that strict `recover` gives every function that calls
  others cost nothing measurable.

| benchmark | end of stage 5 | stage 6 |
|-----------|---------------:|--------:|
| trees     | 193-200 ms     | 176-185 ms |
| frames    | 810-870 ms     | 726-740 ms |

(The hand-written hybrid program ran them in 118-125 and 369-375 ms.)

Also in this stage, for the tests: variables of size zero all have the
address of `runtime.zerobase`, and slices of them no array; `typehash`
is the real one (by kind: floats, strings, interfaces, arrays and
structs field by field) and a map with no entries still checks that a
key can be hashed; a slice of a one-element array field (which is a
field, not an array) goes through the accessors.

Known bug: an in-place `append` to a slice of a stack-allocated array
of structs whose element pointer is reused as a base (`append.go`)
casts the wrong object.

- **Package variables without pointers stay in linear memory**, as
  the stack's do: the compiler addresses them as constants (by the type
  of the address, `gcLinearVar`), the linker lays them out with the
  other data (by the type of the variable, and it reports a reference
  to one as an object), and the zero-initialized ones are in the
  memory too. The start function no longer builds objects for them,
  and composite literals of such types are static data in linear
  memory, as slices of them expect. Benchmark: 262 KB to 244 KB (70 KB
  gzipped).

Where the size goes now (benchmark, 244 KB): 84 KB of linker-generated
helpers, each a dispatch over every layout (`wasm.storebits` alone is
13 KB): the next thing to shrink.

### Stage 7: the tests of the standard library (2026-11-16)

`bench/wasmgc/std/test.py pkg... [-- flags]` runs a package's own
tests with GOWASM=gc, as `go test` would: it compiles the package with
its `_test.go` files, compiles again every package of the closure that
imports it (against that variant, as the go command does), the
external test package, and a generated test main, links, and runs the
binary under node in the package's directory. (One process at a time
uses `std/out`: `build.py` holds a lock.)

What the tests needed:

- `testing` and `testing/internal/testdeps`: the profiling and tracing
  API of the runtime (empty profiles; `StartTrace` reports an error),
  the hooks of `runtime/pprof` and `runtime/trace`, `os/signal` (no
  signals arrive), the FIPS indicator.
- `runtime.Callers` from the host's stack trace (`gd.callers`: the
  wasm function index of each frame, as a pc `index<<16 + 1`, which
  `FuncForPC`, `CallersFrames` and `Stack` name from the name section).
  Files and lines are not known (`?:0`).
- `os.Args` and the environment from WASI (`args_get`,
  `environ_get`); the host gives the program `/` as a preopened
  directory and the current directory as `PWD`, so tests can read
  their `testdata`.
- `runtime.divideError` and `overflowError`, which `math/bits`
  panics with.
- Every conversion to an interface that allocates calls `convT` with
  the type, so the object made is of the type converted
  (`convTstring` and the like made an object of the wrong layout).
- An index into an array reached through an unsafe conversion of a
  pointer to an array of another element type (`keccakF1600Generic`'s
  `(*[25]uint64)(unsafe.Pointer(da))` of a `*[200]byte`) is an access to
  memory, through the accessors, not to the array (which failed
  validation).
- Allocation fusion no longer moves a store into the `struct.new` when
  the value stored is the new object itself or computed from it
  (`l.root.next = &l.root` in `container/list`).
- `clear` of a slice or array of objects (and `memclrNoHeapPointers`
  of structs) clears by type (`runtime.memclrHasPointersT`, given the
  element type the address had before its conversion to
  `unsafe.Pointer`), and is passed the array and the offset of the
  first element, as an element object does not know its array.
- A pointer to a struct field that is itself a struct (`&d.tokens`)
  is no longer turned into a pointer into the array the struct starts
  with: `wasm.addptr` takes bit 30 of the offset as "the pointer is to
  a struct" (`GCAddPtrStruct`), which the compiler sets for `OffPtr`
  of a struct type and reflect for `Value.Field` of one. Zeroing an
  array through such a pointer falls back to `typedmemclr`.
- Functions whose control flow is irreducible, or has unreachable
  blocks, get a dispatch loop (`ssa.wasmDispatch`: the next block's
  index in a local, a `br_table` at the top of a loop) instead of a
  trap: `compress/flate`'s decompressor.
- Loads from static data in linear memory (a string's bytes, by the
  address of the symbol) are loads at a constant address.
- Large arrays of structs (more than 1000) are allocated by the runtime
  (`newobjarrayT`).
- Weak pointers (`weak`, and so `unique`, `net/netip`,
  `text/template`) are the host's `WeakRef`s: `gd.weakmake` gives an
  object a number (the same for the same object), `gd.weakget` returns
  the object or nil; a pointer into an object (a box, made anew for
  each pointer) is held strongly. Imports of module `gd` may now return
  a pointer.
- `runtime.haveHighResSleep` is true (the `time` tests check it), and
  the host clamps a timer's delay to 2^31-1 ms (`setTimeout` fires a
  longer one at once).
- `Stat`, `Lstat` and `Fstat` go through a `Stat_t` in linear memory
  when the caller's is an object, which the host cannot write
  (`filepath.Glob` failed with EOVERFLOW).
- The crypto packages link: `crypto/internal/boring/sig` has Go bodies
  under `wasm.gc` (there is no assembly), and the runtime has the FIPS
  bypass and `crypto/rand` hooks.
- `memhash` hashed the last bytes of a value one at a time when the
  value was in linear memory and as one word when it was in an object,
  so a key of 4 bytes found from an object (as reflect passes one) had
  another hash than the same key inserted from the stack. Both hash
  the same way now. (reflect also copies a key or element that a
  `Value` holds in its pointer word into an object of its type before
  passing it to the map runtime.)
- The host (`run.mjs`) turns off V8's baseline compiler
  (`--no-liftoff`): it gives every local a slot in the frame, and the
  compiler gives every value its own local (up to 360 in a function), so
  `regexp/syntax`'s compiler ran out of stack at a nesting depth of 300.
  The real fix is for the compiler to reuse locals. (Benchmark: trees
  146-153 ms, frames 761-820 ms.)

- An access whose pointer is itself loaded on the way (`t.prefix[0]`)
  lost the slow path of the outer access: the inner one reset
  `g.slow` to nil instead of restoring it, so the outer cast without a
  fallback (`strings.NewReplacer`'s trie, where the string data is in
  linear memory).


After 08:00:

- **Locals are shared.** The values that need a local get one by
  liveness (`colorLocals`: backward dataflow over the blocks, a value
  conflicts with what is live where it is set, greedy coloring by wasm
  type; the results of a call are set at the call, a phi at the end of
  each predecessor; what the code after a recovered panic reads keeps a
  local of its own). The most locals in a function went from 360 to 48
  (`regexp/syntax`); the benchmark is 245 KB, as fast as before.
  Recursion through `regexp/syntax`'s compiler now reaches a depth of
  300 under V8's baseline compiler, not 1000: the host keeps
  `--no-liftoff`.
- Paths passed to WASI by `syscall` and `internal/syscall/unix` are
  copied to linear memory when the string's bytes are an object
  (`linearString`), as `os.RemoveAll` of a directory made at run time
  needed; `Fstatat` writes through a `Stat_t` in linear memory.
- `GODEBUG` set at run time reaches `internal/godebug`
  (`math/rand`'s `TestSeedNop`), and `time.Sleep` of a duration that
  overflows sleeps forever.

- **The V8 aborts** (`Check failed: isolate_->IsOnCentralStack()`)
  came from garbage collection for external memory, which V8 starts at
  a host call after linear memory has grown a lot; when that call is
  on a goroutine's (JSPI) stack, it aborts. Memory now grows through
  the host (`gd.grow`, a suspending import whose promise a microtask
  resolves after growing, on the host's stack; `gd.growsync` from the
  scheduler, which is on the host's stack), and the host collects
  right after a large growth. For that the scheduler must run on the
  host's stack: `park` schedules from a microtask instead of from
  inside the import, and `systemstack` counts as the host's stack
  before it allocates its own. The host also compiles the whole module
  up front and synchronously (`--no-wasm-lazy-compilation`, `new
  WebAssembly.Module`).
- Timers: a stopped or reset timer whose value was sent but not
  received counts as pending (the channel is synchronous since Go
  1.23), a timer channel reports length and capacity 0, a periodic
  timer whose next time overflows never fires again.
- `runtime.GOROOT` is `$GOROOT` when the environment has it
  (`winbatch.go` passes: conformance 924).

- `syscall` and `internal/syscall/unix` copy run-time strings to linear
  memory before passing them to WASI (`linearString`): `os.RemoveAll`
  works.
- `panic(nil)` panics with a `*runtime.PanicNilError` (unless
  `GODEBUG=panicnil=1`).
- Coroutines (`iter.Pull`): `newcoro` makes a goroutine that is not
  runnable, `coroswitch` readies the other side and parks.
- reflect: `Select` (over the runtime's `selectgo`, passed the arrays of
  the cases, not their first element); type descriptors made at run
  time by `SliceOf`, `PointerTo`, `ChanOf`, `MapOf`, `ArrayOf` are
  copied to linear memory (`linearType`), where the runtime reads type
  descriptors; `ArrayOf` types have no constructor number and the
  runtime makes them element by element; `MapOf` types have no group
  and no hasher closure (the runtime hashes by the key type); a channel
  of single references has no zero object, and its elements are copied
  as pointers.
- `std/build.py` writes the `importmap` of vendored packages (so `net`
  builds) and a separate `importcfg.link` without them for the linker.
- reflect's own tests: 183 pass. Still crashing or failing: `StructOf`
  (no constructor for a layout made at run time); calls and `MakeFunc`
  for function types made at run time (`FuncOf`) and through
  `Type.Method(i).Func` (the linker makes adapters only for the func
  types of the program); `Value` panic messages, which come from
  `runtime.Callers` and miss inlined frames; GC bitmaps and
  collection-dependent tests.

- **Reflect calls of func types made at run time.** The adapters (to
  call) and stubs (`MakeFunc`) are per func type of the program. A func
  type made at run time (`FuncOf`) uses those of a func type of the
  program whose arguments and results are in the same registers: reflect
  spells the registers as the linker does (`gcFuncKey`) and finds one
  among the typelinks. `Type.Method(i).Func`, whose type has the
  receiver first, is called as the method value of its receiver. When
  no func type of the program has those registers (the user's
  observation): only reflect can call a function of a type the program
  does not have (Go code would have to assert it to that type, which
  would then be in the program), so `MakeFunc` keeps the `[]Value`
  function and `Value.Call` calls it directly.
- reflect: a variadic method value calls its method with `CallSlice`
  (its arguments come packed); a map insertion from reflect into a map
  whose elements are objects makes the element object; a channel of
  single references copies its elements as pointers before anything
  else.
- **`StructOf`.** Its type descriptor and fields are in linear memory.
  A value without pointers is in linear memory; one with pointers is a
  `wasm.generic` (the user's design): the header, the size, and by
  8-byte word a pointer (array of references) and the bits (array of
  i64). The accessors, `wasm.size`, `wasm.clone` and `wasm.copy` know
  it; `wasm.new` makes one for a negative number (the size in its low
  bits). A pointer stored there is also kept, as its identity, in the
  bits; a pointer loaded where none was stored is the address in the
  bits (the type word of an interface). Such a struct type has no
  `Equal` (a closure is an object, which linear memory cannot hold):
  `TFlagGCMaskOnDemand`, unused with GOWASM=gc, marks it comparable,
  and the runtime compares it field by field. A struct with methods is
  not supported.

Afternoon:

- **Allocations are counted** (`runtime.ReadMemStats().Mallocs`, which
  `testing.AllocsPerRun` reads): the compiler calls
  `runtime.countalloc` where an object escapes (`runtime.newobject`),
  and the runtime counts its slices, strings and objects of types known
  at run time. Not the objects that do not escape, which Go puts on the
  stack. Growing a slice rounds its capacity to Go's size classes, as
  `bytealg.MakeNoZero` does.
- **`runtime.GC`** collects, through the host, from a task of its own
  (a suspending import), after emptying the `sync.Pool`s; then it
  yields, so that cleanups run. Weak pointers go nil.
  `runtime.KeepAlive` stores its argument in a global (`GCGlobalKeep`),
  a use the engine keeps; `GC` clears it first.
- **`runtime.AddCleanup`** registers the object with the host's
  `FinalizationRegistry`; once it is collected the host calls the
  export `cleanup`, and a goroutine runs the cleanup. If nothing can
  run, the host collects once more before it reports a deadlock.
  **`runtime.SetFinalizer`** works the same way and runs the finalizer
  with a copy of the object as it was when the finalizer was set (the
  object itself is gone): enough for the usual finalizer, which reads
  what was set before. Finalizers of one pointer or interface argument
  and no results run; others do not. A pointer into linear memory gets
  no cleanup or finalizer.
- `sync`: semaphores hand off to the first waiter, which runs next, and
  a waiter that waited before is queued first (`sync.Mutex` in
  starvation mode).
- Static arrays of more than 10000 structs, all zero, are made by the
  runtime (`newobjarrayT`) from the start function.
- Linear memory grows by a quarter at least: with the collection that
  follows each growth, growing a page at a time made `math/big`'s tests
  40 times slower.
- reflect: `ArrayOf` of comparable elements is comparable (the runtime
  compares element by element), map keys that need it are updated on
  overwrite, `MapOf` maps check that keys hash, `SliceAt` checks the
  size, `StructOf` with methods works, an `unsafe.Pointer` phi that is
  dereferenced as an array is cast to that array.

- Allocation counting leaves out the runtime's own temporaries (the
  compiler counts escaping objects outside package runtime; the runtime
  counts channels, maps, slices and strings, not a conversion into a
  stack buffer).
- A coroutine's goroutine wakes its waiter in a deferred call, also
  when the iterator calls `runtime.Goexit` (`iter`).
- The linker emits the `wasm.generic` code only for programs with
  `reflect.StructOf`, and the runtime's `cleanup` export only for those
  with cleanups or finalizers.

- **Cleanups on linear memory** (the user's design): a block's header
  holds, above its size class, 1 + the index of its list of cleanups
  (`linCleanups`, whose indexes are reused); `linfree`, which the host
  calls when the block's owner is collected, queues them. The compiler
  gives the block's address from any pointer into it
  (`runtime.linbase`: the owner's address, from the `wasm.linear`).
  `sync`'s tests all pass.
- **Lazy channel timers** (as Go 1.23's, in runtime/wasmgc only): a
  timer with a channel is in the timer heap only while goroutines are
  blocked receiving from its channel (`blocked`); a receive or a select
  first fires one that is due (`maybeRunChan`); `Stop` and `Reset`
  report an armed timer as pending. A timer or ticker that nothing
  refers to is collected (checked with a weak pointer, as natively).
  `TestTimerGC` still fails: it counts `Mallocs - Frees`, and frees of
  objects, which the host collects, are not counted.

Status at about 14:00: conformance 935 to 936 of 965 (`deferfin.go`
depends on when the host collects: it passes alone, and sometimes not
under the parallel run). Std packages: 41 of the 47 tried pass
(unicode, unicode/utf8, unicode/utf16, errors, container/*, strconv,
strings, bytes, sort, slices, maps, path, bufio, io, encoding/hex,
encoding/base64, encoding/binary, encoding/csv, hash/crc32, hash/fnv,
crypto/sha256, crypto/md5, crypto/sha1, math, math/bits, math/rand,
math/big, fmt, regexp, regexp/syntax, text/tabwriter, html,
image/color, compress/flate, compress/gzip, text/template,
path/filepath, weak, iter, context). Failing: `time` (`TestTimerGC`:
timers nothing refers to are not collected, as Go 1.23's are), `sync`
(`TestOnceXGC`: a cleanup on linear memory), `unique` (its internal
`canonMap` tests), `reflect` (structs with methods made by StructOf
that are called, a 256-pointer argument, Value panic messages);
`encoding/json` (its test helper package does not build). Benchmark:
262 KB (68.8 KB gzipped), trees 140-165 ms, frames 780-840 ms.

Status at about 13:00: reflect's own tests: 195 pass. Still failing:
`StructOf` of a struct with methods (embedded fields); `TestLarge`
(an argument of 256 pointers, passed by reference); `ArrayOf` of an
array of structs compared generically; `SliceAt` and `MapOf` flag
checks; GC bitmaps, allocation counts and finalizers. Conformance 927
of 965 (`issue30606`, `issue30606b`, `reflectmethod7` now pass).

Status at 07:50: of 45 packages tried, the tests of 31 pass:
unicode, unicode/utf8, unicode/utf16, errors, container/list,
container/ring, container/heap, strconv, bytes, sort, maps, path,
encoding/hex, encoding/base64, encoding/binary, encoding/csv,
hash/crc32, hash/fnv, crypto/sha256, crypto/md5, crypto/sha1, math,
math/bits, fmt, regexp, regexp/syntax, text/tabwriter, html,
image/color, compress/flate, text/template.
Failing: tests that count allocations (`testing.AllocsPerRun` sees 0:
`strings`, `bufio`, `slices`); `io` (removing a temporary directory:
`unlinkat` fails); `math/rand` (`TestSeedNop`); `math/big` (times out);
`time` ("big sleep returned" before the timer clamp; not rerun);
`weak` and `unique` (they need a collection that `runtime.GC` cannot
force; `unique`'s test has a static array of 131072 structs, which the
linker refuses); V8 sometimes aborts with `IsOnCentralStack` in tests
with many goroutines (seen in `compress/flate` and `regexp` runs).
Conformance: 923 of 965 (`issue34123` now fails: it wants line numbers
from `runtime.Callers`).
