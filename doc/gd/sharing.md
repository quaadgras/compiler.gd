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
> callee masks, and local and shared objects in spans of their own (the
> end of this document).

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

## Local objects in spans of their own (step 3)

Decided with the user on 2026-11-19: whether an object is shared is
decided **when it is allocated**, by OR-ing every vector through which
its address, or anything reached from it, could get to another goroutine.
A vector that cannot be evaluated at the allocation counts as shared.
Nothing becomes shared later, so the page of a word says whether
another goroutine may read or write it. The user also wants the collector
to use it: when a goroutine exits or is collected, the local memory it
allocated is free.

Local memory was first a range of addresses of its own (64 GiB at the
first arena hint, with the stacks in its second half), so that the test
was one compare. The user dropped that on 2026-11-19: the decision is in
the bits that callers and callees pass, the address was only read for
what is stored through a pointer, and the range cost a second region in
the page allocator and rules for where the rest of the heap may be that
windows and macOS did not meet. Local objects are now in spans of their
own, anywhere in the heap.

### The invariant

A **local** object (and a stack) is reached only by the goroutine that
allocated it, and so is everything that is reached from it: nothing that a
local object leads to is ever shared, however deep. A shared object only
points to shared objects. A local object may point to shared ones.

It follows that code that only has a pointer `p` knows what to allocate
for a store through it: `p.f = new(T)` allocates as `p` is. Whoever shares
something loaded from `p`'s object makes `p`'s object shared, and that
is visible to whoever allocated it: in its own function, or through the
parameter tags (a callee that leaks what its parameter points to), or
because the callee is unknown.

### Done

- **Allocator** (`runtime`: `localSpans`, `spanClass.local`,
  `otherPages`). amd64 and arm64 on linux, darwin, freebsd, windows.
  Each size class has a second span class, for local objects, so a page
  of the heap has local objects or other ones, never both. A table
  (`otherPages`, 2^20 numbers, indexed by the page's address modulo its
  size) counts, for each place, the pages there that hold something else
  than local objects: the spans of the other classes, the stacks, the
  globals, and the first page (nil). A pointer whose page has zero points
  to a local object: one load in compiled code. Two pages 8 GiB apart
  have the same place, so in a heap that is spread wider some local
  objects are not known to be (and what is stored in them is then
  shared, which is safe). The size-specialized mallocs and the tiny
  allocator are for the other objects only for now.
- **Entry points**. A type descriptor passed to `newobject`, `mallocgc`,
  `makeslice*`, `growslice*` may be tagged (bit 0, `allocType`): the object
  is local. Untagged, as everything not compiled for it is (reflect,
  assembly, linkname), is shared.
- **Static decision** (`escape`: `attrSharesDeep`, `attrReturned`,
  `markLocal`). An allocation is local if it is in the heap and nothing
  reached from it flows, at any depth, to where another goroutine reaches
  or to a result (the caller is not known). What is loaded from the
  contents of a tracked variable's objects counts for those objects
  (`location.owners`). An address kept as an integer is shared
  (`shareLoc`). Not in the runtime's packages or reflect.
  `ir.miniExpr.LocalAlloc`, `ir.LocalObject` on a moved variable.
  `-d=nolocalalloc` turns it off, `-m=2` prints the decision and why not.
- **Oracle**. The store barrier, the publish walk and the per-object bit
  still run and still decide the guards. `GODEBUG=sharestats=1` counts the
  objects of each range and the local ones that were published all the
  same, each of which is a wrong decision; `=2` traces them, `=3` throws.

- **Dynamic roots** (`escape/shares.go`: `dynRootOf`, `localTag`). A
  store through a pure path from a local variable (`p.f = x`, `*p = x`,
  `p[i] = x`, the elements of `append(p.f, x)`) is a store in a container
  known at run time. An allocation that only flows to such containers
  gets a tag computed where it is allocated: 1 if all of them are local
  objects. Allowed containers: the one of the allocation's own
  statement (a plain assignment; without a call if the path loads), and a
  parameter that is never assigned. `ir.LocalTag` holds the expression
  (`(otherPages[uintptr(P)>>13 & (1<<20-1)] - 1) >> 63`, `escape`'s
  `localTest`), which the code generator
  adds to the type descriptor. `-d=nolocaldyn` turns it off. For Phase G
  these containers are the heap (`isDynRoot`).

- **Stacks are not local objects.** A container on a stack is not
  known to be local from where it is, since the function that has it may
  return or share what a callee stored in it: the pages of the stacks
  count in the table.
- **The bits of a call** (`runtime/shared.go`, `escape/callbits.go`,
  `ssagen`: `takeLocalBits`, `setLocalBits`). What the address cannot
  say, the caller says, in `g.localBits`, right before a direct call:
  - bit 0: nothing reached from the results of this call will be
    shared. The caller knows from a location that each result flows
    through. An allocation that the callee only returns has the bit as
    its tag.
  - bit k+1: nothing stored through argument k (the receiver first)
    will be shared: the argument is `&x`, or a tracked variable, whose
    allocations and contents nothing shared is reached from. In the
    callee, a store through a parameter that is never assigned is in a
    local container if the bit is set or if the parameter points to a
    local object.
  - A caller that returns the results, or passes on a parameter, says
    what it was told (`return f(x)`, `g(p)`).

  The word holds the bits in its 16 high bits and the address of the
  function they are for in the others. A function that uses them takes
  the word when it starts, leaves zero, and has the bits only if the
  address is its own (`ir.Func.LocalBitsVar`, a local variable). So a
  word that was not taken says nothing to another function, a function
  that is called indirectly or by code that knows nothing has zero, and
  the caller need not know whether the callee takes them
  (`TakesLocalBits`, exported, only saves the store). A panic clears the
  word before the deferred calls. `-d=nolocalbits` turns it off.
- **Where parameters go** (`ir.ParamShare`, exported with the function).
  The escape tag of a parameter says "the heap", which was "shared" for
  the caller: a method that stores in its receiver made the receiver's
  object shared. Each parameter now also says the least number of
  dereferences that give what another goroutine may reach, the
  parameters through which it is stored (`p.f = x` with `p` never
  assigned: for the caller, a store through its argument), and the
  results it reaches through an object in the heap. The caller's
  argument flows to a heap that nobody reaches by itself
  (`ownHeapLoc`), so that escaping is as before. The elements of an
  appended slice flow there too: the new array is where the result goes.
- **Escaping follows the same record.** `ParamShare.Heap` is the least
  number of dereferences that give what must be in the heap for another
  reason than a store through a parameter. A caller's argument that is
  only stored through another argument flows to what that argument
  points to, not to the heap: with `func set(t *T, p *int) { t.p = p }`,
  `set(&t, &y)` leaves `y` on the stack if `t` is, and moves it to the
  heap if `t` escapes ("y escapes if t does"). Only for `&x` of a
  variable: a tracked pointer's contents and the containers known at run
  time are the heap for escaping. What is stored deeper (`t.q[0] = p`),
  or in an object that the callee allocates, is in the heap as before.
- **Indirect calls.** A call of a closure or of an interface method
  stores the bits with the code pointer that it calls, so a function
  that is called that way has them too; a wrapper in between forwards
  bit 0 if it returns the results (it is compiled like any function),
  and otherwise the word matches no one. Arguments of a function that
  is not known still flow to the heap and are shared, so only bit 0 is
  ever set there: the escape masks would have to say, per argument,
  what `ParamShare` says.
- **Arguments of indirect calls** (`escape/callbits.go`: `callArgRoot`,
  `callArgTag`; `ir.EscMaskShared`). Dependencies between the arguments
  of a function that is only known at run time are for locality only:
  on the stack they cannot be honoured (a candidate may be moved to the
  heap by a later call, and an address does not say how long its
  variable lives). A local object does not move and may be stored in
  any local object, but it must be local from birth, so the rule is:
  **an object is local for an indirect callee only if the callee is
  known where the object is allocated; otherwise it is shared.**
  - The escape mask that closures and itabs carry has, after the 21
    bits "the function keeps argument k", 21 bits "what it keeps may be
    reached by another goroutine" and 21 bits "it is stored through the
    receiver". They come from the parameters' escape notes (a ninth
    byte, from `ParamShare`), so every carrier of a function's mask
    agrees. A function with nothing known says that all is shared.
  - An argument of an unknown callee flows to a dynamic root of its
    own. An object that is allocated in the arguments, in the call's
    statement, and flows nowhere else, has as its tag what the mask
    says: local if the callee does not keep the argument; or keeps it
    where no other goroutine reaches; or stores it through its receiver
    and the receiver points to a local object. What is called must be a
    local variable or a method of an interface that is one. A nil
    function reads a mask that says "shared" (the call panics after the
    arguments are evaluated, as before).
  - Not yet: a callee that is a parameter of the allocating function,
    for objects allocated before the call's statement;
    `r.list = append(r.list, p)` in the callee (deeper than the
    receiver: shared).
- **Receivers of interface calls** (`escape/recv.go`). The method is
  only known when the program runs, but the object is allocated with a
  type, and the type says what the methods are: this is the rule above
  with the callee known from the type. The receiver of `i.M()` flows to
  a *receiver root* (the heap for escaping, with the interface type and
  `M`), and a conversion of a pointer to an interface type has a
  location that the pointer flows through, with the pointer's type.
  - A conversion that reaches a receiver root by a path with no other
    conversion on it (a *raw* path: the solver keeps, for the walks from
    these roots, the least dereferences of such paths apart) is checked:
    the method `M` of its type must not let another goroutine reach its
    receiver or what the receiver leads to, nor return it, nor store it
    through another parameter (the method's `ParamShare`; a method that
    is not analysed yet, or is not Go, fails). If one fails, what was
    converted is shared, as before. If all pass, the root says nothing
    of what was converted: `use(&Sq{})` with `func use(s Shape)` leaves
    the object local, and what `(*Sq).Grow` stores through its receiver
    is local by the receiver's address.
  - A parameter of an interface type that is itself the receiver, of
    methods of its own type, says so (`ParamShare.Recv`, exported), and
    a caller's argument flows to a receiver root for any method of that
    type. The parameter is shared in the escape masks: a caller that
    does not know the function cannot check.
  - Everything else that reaches a receiver root on a raw path holds or
    leads to an interface of a type that is not known here: an
    allocation the receiver is loaded from, the result of a call (the
    bit for results is clear), a parameter that is not the receiver
    (shared at that depth). A value that an interface was asserted to
    hold (`i.(*T)`, a case of a type switch) has a location too: what is
    reached from it is reached from the interface without its methods,
    so if a raw path gets there, the interface's object is shared.
  - `-d=nolocalrecv` turns it off. Not yet: a receiver that the method
    returns (`func (b *B) Self() I`); methods that are analysed after
    the function that converts (interface calls are not edges of the
    order in which functions are analysed); values that are not
    pointers (the box that `convT` makes is not tagged).
- Variables that are moved to the heap with the same tag are still
  allocated together.

What it finds, measured with `-m=2` and `GODEBUG=sharestats=1`
(2026-11-19):

- A program that builds trees through methods (`root := new(node)`,
  `root.add(...)` with `n.kids = append(n.kids, &node{})`): 88% of the
  objects local, none published.
- Allocation sites that can be local (statically or by a tag), before
  and after the bits: `encoding/json` 4 → 84 of 140, `strings` 72 of
  129, `regexp/syntax` 139 of 262, `go/parser` 52 of 598 (the rest are
  stored in interfaces or passed to unknown callees).
- No local object was published in the tests of `encoding/json`,
  `regexp`, `text/template`, `go/parser`, `strconv`, `net/http`,
  `go/types`. Few objects are local there (1% in `go/types` and
  `net/http`): tests are called through function values and hand their
  results to `reflect.DeepEqual` and `fmt`.

### Next

1. Callees that are parameters, consulted where the object is
   allocated. Receivers: results that are the receiver, and methods
   analysed before the functions that convert to their interfaces.
2. Results in a caller's buffer (Phase G, `maybeInPlace`) are shared
   when the buffer is nil: pass the tag.
3. `P.s = append(P.s, x)`: x is tested against the old array, nil the
   first time. A nil array could take the tag of `P`.
4. A variable that is assigned once and in scope at the allocation as a
   whole-function container (needs scopes in the graph).
5. Maps (`makemap` tagged, tables as the header), strings, `convT*`,
   closures.
6. `unique.Make` of a string gets no bits (an SSA rule needs the call to
   follow the conversion of its argument).

### The dynamic vectors as first planned



A location in the escape graph that is a **dynamic root** stands for a
container that is only known at run time. It has no `attrShares`; the
solver records, on every location that flows to it at any depth, that it
does (a small set per location). At the end an allocation with a static
mark is shared; one with only dynamic roots that can be evaluated where
it is allocated is local if all of them say so (the tag is computed);
any other root makes it shared.

1. **The container of a store** in the statement that allocates:
   `p.f = &T{}`, `*p = new(T)`, `s[i] = make(...)`, `m[k] = v`,
   `p.f = append(p.f, x)`. The root is "`P` points to a local object" for the pure path `P`
   (names, fields, derefs, no calls) that the store goes through. It can
   be evaluated at an allocation that is in the right-hand side of that
   statement, since nothing between the allocation and the store changes
   `P` (calls are hoisted before by order). For any other value that
   flows there the root is shared.
2. **Elements appended**: the container is the array. Local if the old
   array is (the old pointer's page: then everything the slice flows to is
   local, so the new array is too); for a nil slice, as the append itself
   is decided.
3. **A parameter that is never assigned** is a root for every allocation
   of the function, not only in the statement.
4. **Results**. What is only returned is the caller's to decide. The
   caller says so with the `outBuf` that Phase G already passes for a
   pointer result (a sentinel: "in the heap, but local"); other results
   (strings, slices, structs) need a bit that every function with such a
   result reads or clears on entry (a field of g set right before the
   call), forwarded by `return f(x)` and the wrappers.
5. Maps: tag `makemap`; the tables are allocated as the map header is.
   Strings (`concatstrings`, `slicebytetostring`: the buf argument can
   carry the tag), `convT*`, closures.

A stack variable whose contents get shared must then be in the shared
heap ("moved to heap" for a new reason), since a callee storing in it
only sees a stack address. This is the cost of the design for
`var b strings.Builder; ...; ch <- b.String()`.

### Then

- Guards become the test of the page inline: a local object, no lock;
  else the runtime (which still says "not shared" for stacks and C
  memory).
  `sharePages`, the store barrier, the publish walk and the per-object
  bit stay only under `-d=sharecheck` as the witness.
- Fast paths for local allocation (size-specialized, tiny).
- Exposed addresses: `exposeptr` of a local object must be refused or the
  conversion made a static sink everywhere (now only in escape analysis).
- Freeing a goroutine's local memory when it exits or is collected:
  needs to know the goroutine of a local span (spans owned per
  goroutine, or a list per goroutine).
