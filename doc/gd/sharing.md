# Goroutine sharing (gd fork)

Goal: no memory unsafety from a data race. In Go a race on a map, or on a
value of more than one word (an interface, a slice, a string), can break
memory safety: a table half grown, an interface's type with another
value's data. The operations that can go wrong take a lock, but only on
memory that more than one goroutine can reach, and the runtime knows which
that is.

> **Status:** the shared bit, its publication, the compiler's store barrier
> and the map lock are in (step 1), and so are whole loads and stores of
> slices, strings and interfaces on amd64, arm64 and the other targets with
> an inline atomic load (step 2). The compiler proves nothing statically
> beyond "just allocated here"; the plan for that is at the end (step 3).

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

## Values of more than one word

A slice, a string and an interface are several words. A goroutine that
loads one while another stores it can get the words of two values, and
then index past an array or call one type's method on another's value.
Loads and stores of such a value are made atomic with sequence locks
(`runtime/shared.go`, `cmd/compile/internal/ssagen/share.go`): 1024 of
them, found from the page of the value's first byte, odd while a store
holds one.

- **A load** from memory that is not the stack reads the lock, loads the
  value, and reads the lock again, inline; if the lock moved or was held
  it calls the runtime, which does the same until it succeeds. It never
  asks whether the memory is shared: memory that is not has no other
  writer.
- **A store** there calls `runtime.sharestore`, which takes the locks of
  the pages it writes if the memory is shared (and only then), and
  `shareunlock` after. The goroutine is not preempted in between. A store
  of some of the words only (`s = s[:n]`, an append in place) stores them
  all when the memory is shared: another goroutine may have stored the
  others since they were loaded.
- **A copy** of a struct or array that holds such values is a load, a
  store, or (between two places that are not the stack) a call to
  `sharetypedmemmove`.
- `len` and `cap` of a slice in memory load one word and need no lock.
- The runtime's copies do the same: reflect's and maps' `typedmemmove`,
  `typedslicecopy`, `growslice`'s copy of the old array, a channel receive
  straight into shared memory.

Not guarded: an object the function has just allocated; a value an
interface points to (never stored in); functions marked `nosplit` or
`norace` (syscall's, in the child of a fork); the equality and hash
functions the compiler writes, which must be the same in every package
(a struct of strings compared in place while it is stored can still be
read torn); `clear` and the zeroing loops the runtime does; 386 and arm.
On arm64 the second read of the lock is an acquire load but there is no
barrier before it, so a reordered load of the value is not excluded yet.
`-gcflags=all=-d=notear` turns this off, for measurements.

The loads are memory operations, so that the value's load stays between
them. That costs the optimizations that merge two loads of one field:
`for i := 0; i < len(p.b); i++ { p.b[i] }` checks its bounds again, where
`s := p.b` before the loop does not.

## What it costs (amd64, 8 CPUs)

Step 2, against `-d=notear`:

| benchmark | off | on |
|-----------|-----|----|
| index a slice loaded once, 4096 elements | 949 ns | 946 ns |
| index a slice field loaded per element | 1767 ns | 3604 ns |
| load a string field | 1.2 ns | 2.6 ns |
| call a method of an interface field | 1.5 ns | 1.9 ns |
| store a slice in a private object | 3.9 ns | 7.8 ns |
| store a slice in a shared object | 7.8 ns | 19.1 ns |
| append in place to a private slice field | 4.1 ns | 8.4 ns |
| `strings.Builder`, 64 writes | 341 ns | 627 ns |
| `strings.Fields`, 256 fields | 5.0 µs | 6.3 µs |
| sort 4096 strings | 375 µs | 576 µs |

Step 1, against `-d=noshare`:

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

## Next: local and shared memory in separate address ranges (step 3)

Agreed with the user on 2026-11-19. "Shares this argument" becomes a
per-parameter fact beside "escapes", computed by escape analysis and
carried by the callee masks for closures and interface methods
(`escape-bits.md`). An allocation that may reach a sharing sink is made in
the shared heap from the start; the others in the goroutine-local heap;
the two are separate address ranges, so "is it shared" is a compare of the
pointer, and one unsigned compare answers "non-nil and local". The store
barrier and `sharestore` become that compare inline, and call the runtime
only for a store of a local pointer in shared memory, which the analysis
should have excluded: the bitmap and the walk stay as the answer for the
flows it cannot see (reflect, unsafe, assembly).
