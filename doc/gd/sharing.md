# Goroutine sharing (gd fork)

Goal: no memory unsafety from a data race. In Go a race on a map, or on a
value of more than one word (an interface, a slice, a string), can break
memory safety: a table half grown, an interface's type with another
value's data. The operations that can go wrong take a lock, but only on
memory that more than one goroutine can reach, and the runtime knows which
that is.

> **Status:** the shared bit, its publication, the compiler's store barrier
> and the map lock are in (step 1). Values of more than one word are not
> guarded yet (step 2), and the compiler proves nothing statically beyond
> "just allocated here" (step 3).

## Shared objects

A heap object is *unshared* while only the goroutine that allocated it can
reach it, and *shared* from the moment a second one might. Sharing lasts
for the life of the object and goes to everything the object points to.
There is one bit per object, in a bitmap of its span that is allocated
when the span gets its first shared object (`runtime/shared.go`; the
pinner's bits are kept the same way). The sweeper carries the bits of the
objects that survive to the next cycle. Memory outside the heap is shared
unless it is a goroutine's stack: globals are.

The goroutine that hands an object over marks it, and all that is
reachable from it, *before* the pointer leaves its hands (`publish`).
That is what makes the bit usable without a lock:

- nothing else can reach an unshared graph, so it does not change under
  the walk;
- a goroutine that finds one of its own objects unshared knows that it
  still is, since only it could have shared it.

Marking is monotonic, so the walks cost at most one visit per object over
its life.

## Where pointers are handed over

- `go f(x)`: the closure (`newproc`).
- A send on a channel that is itself shared (`chansend`, `selectgo`). A
  channel only its goroutine reaches shares nothing; what it holds is
  shared with it when the channel is published. A receive from such a
  channel straight into shared memory goes by way of a temporary.
- A store of a pointer in a global or in a shared object: the compiler's
  barrier, below.
- The runtime's own stores: `sync/atomic`'s pointer operations, the copies
  reflect and maps make (`reflect.typedmemmove`, `typedslicecopy`), a
  timer's function and argument, a finalizer's or a cleanup's object and
  function, and the object of a weak pointer (anyone who holds the weak
  pointer can make a strong one, and only the owner may publish).

## The store barrier

`cmd/compile/internal/ssa/writebarrier.go` already finds the stores of
pointers that are not stores in the stack. Before each one that may store
a heap pointer it calls `runtime.publishptr(dst, val)` (or
`publishmove(typ, dst, src)` for a copy), which publishes the value if
`dst` is shared. The call comes before the write barrier's test, which no
safe point may separate from the store, and each store gets its own
sequence: what a store publishes depends on the stores before it
(`x.f = y; global = x`).

No call is made for a store in an object that the function allocated and
has only initialized since (`isUnpublished`), which covers composite
literals. The runtime's packages are compiled without the barrier and
publish by hand. `-gcflags=all=-d=noshare` turns the barrier off, for
measurements only.

## Maps

A map's header has a lock word (in the padding it had on 64-bit targets):
readers count in it, a writer takes it whole, a waiting writer stops new
readers. Every map operation starts by asking whether the map is shared
(`internal/runtime/maps/shared.go`); a shared one runs the generic
operation with the lock held, and what a write allocates for the map is
shared from the start. The lock is held for the operation only, so two
goroutines that race on a map see a map; they may still see an entry
before its value is stored, or lose an update. "concurrent map writes" is
no longer reachable for a map goroutines share.

## What it costs (step 1, amd64, 8 CPUs)

Against the same toolchain with `-d=noshare`:

| benchmark | off | on |
|-----------|-----|----|
| lookup in a private map | 9.9 ns | 10.0 ns |
| assignment in a private map | 10.1 ns | 9.7 ns |
| lookup in a shared map | 11.6 ns | 20.6 ns |
| assignment in a shared map | 11.6 ns | 23.6 ns |
| lookups in a shared map, 8 goroutines | 1.5 ns | 107 ns (`sync.RWMutex` around a map: 124 ns) |
| build and sum a private tree of 32767 nodes | 655 µs | 734 µs |
| sort 4096 strings (stores in a private array) | 251 µs | 383 µs |
| link 4096 private list cells | 8.6 µs | 24.3 µs (3.8 ns a store) |
| link 4096 shared list cells | 9.3 µs | 59.7 µs (12 ns a store) |

The private map costs nothing measurable. The two costs to work on are the
call the barrier makes for every pointer store it cannot rule out (step 3:
prove the target unshared, or test the bit inline), and the readers of a
shared map that no goroutine writes, who now bounce the lock word between
them as `sync.RWMutex`'s readers do.

## Not covered

- Pointers stored by way of `unsafe` (as bytes, or through a `uintptr`).
- wasm: goroutines run one at a time there and are not preempted between
  instructions, so nothing is marked (`sharingEnabled`).
- User arenas are treated as shared and not walked.
