# Goroutine sharing (gd fork)

Goal: no memory unsafety from a data race. In Go a race on a map, or on a
value of more than one word (an interface, a slice, a string), can break
memory safety: a table half grown, an interface's type with another
value's data. The operations that can go wrong take a lock, but only on
memory that more than one goroutine can reach, and the runtime knows which
that is.

> **Status:** in: the shared bit, its publication, the compiler's store
> barrier and the map lock (step 1); whole loads and stores of slices,
> strings and interfaces on every target but wasm (step 2); and what the
> compiler proves unshared from escape analysis, within a function (the
> first part of step 3). Not yet: the per-parameter "shares" bit in the
> callee masks, and local and shared memory in separate address ranges
> (the end of this document).

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
(`runtime/shared.go`, `cmd/compile/internal/ssagen/share.go`): one for
every 64 bytes, in a table of 16384 that an address indexes modulo its
megabyte, each with a cache line to itself so that goroutines that store
in neighbouring blocks do not write the same line; odd while a store
holds one.

- **A load** from memory that is not the stack reads the lock, loads the
  value, and reads the lock again, inline; if the lock moved or was held
  it calls the runtime, which does the same until it succeeds. It never
  asks whether the memory is shared: memory that is not has no other
  writer.
- **A store** there calls `runtime.sharestore`, which takes the locks of
  the pages it writes if the memory is shared (and only then), and
  `shareunlock` after. The goroutine is not preempted in between. A store
  of the length only (`s = s[:n]`, an append in place) is, in shared
  memory, still that if the length fits the capacity that is there now,
  and a store of the whole slice otherwise: another goroutine may have
  stored a shorter one since this one was loaded.
- **Before it calls** `sharestore`, compiled code loads one byte
  (`runtime.sharePages`, a byte for each 8 KiB page of memory, found from
  the address) that is zero unless something in a page like that one was
  ever shared; if it is zero there is no call. And if the runtime says
  the memory is not shared, the store that follows has no publishing
  calls either: there are two copies of the store.
- **A function with its own exclusion** for what it loads and stores is
  marked `//go:notear` and has none of this (its pointer stores still
  publish): `sync.Pool`'s, where a P's private value is its own and the
  queues hand values over with atomics. Without it, and with the locks
  found from pages, every `sync.Pool` operation of every P took the same
  lock: 151 ns instead of 1.3; it is 1.9 now.
- **A copy** of a struct or array that holds such values is a load, a
  store, or (between two places that are not the stack) a call to
  `sharetypedmemmove`.
- `len` and `cap` of a slice in memory load one word and need no lock.
- The runtime's copies do the same: reflect's and maps' `typedmemmove`,
  `typedslicecopy`, `growslice`'s copy of the old array, a channel receive
  straight into shared memory.

Not guarded: memory the compiler knows no other goroutine can reach (an
object the function has just allocated, and what the next section finds);
a value an interface points to (never stored in); functions marked
`nosplit` or `norace` (syscall's, in the child of a fork). The equality
and hash functions the compiler writes load whole in every package, the
runtime's included, since all copies of one must be the same. `clear`
and the runtime's clears (`memclrHasPointers`, `typedmemclr`) take the
locks in shared memory. Where the runtime reads a value through its
address (boxing in an interface, a map key, a channel send), walk copies
the value to the stack first unless it is in a variable of the stack
(`order.go`'s `addrTemp`). On targets without an inline atomic load (386,
arm) the lock is read with a call. On arm64 a `DMB ISHLD` follows the
value's load, so that it is done before the lock is read again; the
runtime's own load has a barrier there too, and one after it takes a
lock, so that the lock is seen held before the stores are (without them
`TestSharedTear` failed one run in three on an Apple M-series machine,
and passes there now, as it always did on amd64).
`-gcflags=all=-d=notear` turns all this off, for measurements.

The loads are memory operations, so that the value's load stays between
them. That costs the optimizations that merge two loads of one field:
`for i := 0; i < len(p.b); i++ { p.b[i] }` checks its bounds again, where
`s := p.b` before the loop does not.

## What the compiler proves (`cmd/compile/internal/escape/shares.go`)

The solver has two more attributes. `attrShares`: the location's address
may flow to where another goroutine can reach it, which is the heap (a
global, memory stored in through an untracked pointer, a channel, a go
statement, a callee that keeps it) or a location that has the attribute.
`attrSharesValue`: the value held there may. A returned value flows to
neither: the caller is the same goroutine. An allocation without
`attrShares` is reached, while the function that made it runs, by that
function's goroutine only.

The code generator stores and loads through variables, so the facts it
needs are about variables (`ir.Name.Unshared`):

- `UnsharedStorage`: the variable's own memory is not shared (it matters
  for a variable moved to the heap);
- `UnsharedPointee`: every pointer the variable ever holds is to an
  unshared allocation of this function, or nil.

The graph knows where an address flows, but not all that a variable may
be assigned (a call's result, a load from the heap). So only *tracked*
variables can have `UnsharedPointee`: pointer and slice locals, not
address-taken, in no closure, whose every assignment, found in the
syntax, is from `new`, `&T{}`, `make`, a slice literal, `append` to, a
slice of, or the address of a part of what another tracked variable
points to, or another tracked variable.

A store through a pointer is, to stock escape analysis, a store in the
heap, which would make everything a function links together shared. For
tracked variables the graph has a location for what their allocations
hold, with `x = &contents`: a store through `x` flows there, a load
through `x` (or through a copy of `x`, tracked or not) flows from there,
and the contents are shared exactly when `x`'s value is. Variables that
may hold the same pointer have one contents location. For escaping, the
contents are the heap, as before: the escape tests' output is unchanged.

With the fact, `ssagen` generates plain loads and stores (`localExpr`),
and marks the pointer stores (`Store`'s auxint) so that the write barrier
pass adds no call to `publishptr`. Filling or appending to a local slice
of strings, and building a list or a tree of local nodes, cost what they
did.

**Checking it.** A wrong fact would be silent, so there is a mode that
checks them: with `-gcflags=all=-d=sharecheck`, every access that relies
on a fact first calls `runtime.sharecheck`, which throws if the memory is
shared (the runtime's bit is the witness). `go test -short
-gcflags=all=-d=sharecheck std` passes; it has found two things so far,
both in the runtime's bookkeeping and neither in the analysis (a store in
the runtime's own memory taken for a store in shared memory, which
published a pointer to a freed object). `TestSharedStatic` has the alias
cases that reasoning found: a store through one variable loaded through
another, an element's address, a slice of a slice, an append that moves,
a callee that publishes what its argument points to.
`-gcflags=all=-d=nolocal` turns the facts off.

Not proved: anything about a parameter, a result of a call that is not
inlined, or a pointer loaded from memory. That needs the fact to arrive
with the pointer: the next step.

## What it costs (amd64, 8 CPUs)

Everything on against everything off (`-d=noshare,checkptr=0`), the best
of three runs, 2026-11-19, after the changes above:

| benchmark | off | on | |
|-----------|-----|----|-|
| `sync` Mutex, uncontended | 0.45 ns | 0.44 ns | 1.0 |
| `sort` 1000 ints | 8.1 µs | 8.2 µs | 1.0 |
| `strconv` Quote | 206 ns | 218 ns | 1.1 |
| `net/http` ReadRequest | 2.84 µs | 3.17 µs | 1.1 |
| `go/parser` Parse | 1.48 ms | 1.66 ms | 1.1 |
| `bufio` Writer flush | 9.1 ns | 11.2 ns | 1.2 |
| `fmt` Sprintf of a string | 9.7 ns | 13.5 ns | 1.4 |
| `regexp` one-pass | 249 ns | 349 ns | 1.4 |
| `sync` Pool | 1.2 ns | 1.9 ns | 1.5 |
| `sort` 1000 strings | 35 µs | 60 µs | 1.7 |
| `fmt` Sprintf of an int | 7.3 ns | 14.1 ns | 1.9 |
| `encoding/json` decoder | 2.25 ms | 4.70 ms | 2.1 |
| `sort` stable, 1000 strings | 80 µs | 169 µs | 2.1 |
| `fmt` Fprint of an int | 39 ns | 107 ns | 2.7 |
| `net/http` header write | 326 ns | 939 ns | 2.9 |
| `encoding/json` encoder | 0.64 ms | 1.88 ms | 2.9 |

The compiler, which is a Go program with a few pools and a lot of
slices, builds the standard library in 27.5 s with all of this in itself
and 23.3 s without: 18% more.

Before the lock table was by 64 bytes and `sync.Pool` had its pragma the
last ones were 6 to 35 times, and Pool itself 119.

What is left is of two kinds. Strings and slices sorted or copied in
place through a parameter, which the compiler knows nothing about: two
lock reads around each load, and a call for each store that the byte of
the page does not excuse. And objects that come from a `sync.Pool`, which
are shared for good once they have been in it: every store of a slice, a
string or an interface in one takes a lock (a buffer's `buf = append(buf,
...)`), and every pointer stored is looked up. Both want the next step.

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
