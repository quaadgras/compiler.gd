# Maps of the gd fork: a data race is memory safe

A racing program may see stale entries in a map, never a torn map: no
index out of bounds, no key or element made of two stores, no wild
pointer. It is the fourth step of the work that makes data races memory
safe, after interfaces (`fat-iface.md`, `sharing.md`), strings
(`sso-string.md`) and slices (`slice16.md`). The code is in
`internal/runtime/maps`, mostly `seq.go`. `TestMapTear`,
`TestMapConcurrentWrites` and `TestMapHashPanicFree`
(`runtime/maptear_test.go`) check it.

## 1. The rules

- **A writer takes the map with one compare-and-swap** of `Map.seq`, a
  sequence lock, odd while a writer holds it. If it is held, the writer
  **panics** with the `runtime.Error` "concurrent map writes". It never
  waits.
- **A reader writes nothing.** It loads the header's directory fields
  between two loads of `seq`, then follows only what stays safe whatever
  a writer does, and checks `seq` again at the end.
- **A read that overlaps a write is not an error.** It starts again, up
  to `mapReadTries` (8) times, and then returns what it found.

### The panic can be recovered

Stock Go makes this a `fatal error`, because by then the map may be
corrupt. Here it cannot be: the loser panics before it has stored
anything, and the winner is not disturbed. So a program can recover from
it, and the map is a map afterwards (`TestMapConcurrentWrites` checks it,
and then writes to it).

A panic inside a write would leave the map held, so nothing that can
panic runs while it is held:

- The key is hashed **before** the compare-and-swap, since `Hasher` panics
  on an unhashable dynamic type (`lockWriteHash`). The seed is read before
  hashing and compared once the map is held, because a write that came in
  between may have changed it (`Clear`, or the delete of the last entry);
  then the map is released and the key hashed again.
- `Equal` on the keys of a held map does not panic: the key was just
  hashed, so its dynamic types are comparable, and so are those of the
  keys in the map. The rehash of a growth hashes keys that are in the map.
- The fast variants' hashes do not panic: they take the map first.
- What is left is the allocator, whose failure is a `throw`.

### A read does not fail

Stock Go's "concurrent map read and map write" and "concurrent map
iteration and map write" are gone. Stock Go only detected them by luck (a
flag read once, at the start). With the safety below the read has an
answer to give, so crashing the program buys nothing, and the race
detector remains the tool that finds these races. What it costs is that a
program that reads and writes a map without a lock is no longer told so
by a crash unless two writers meet.

A reader **waits** only in `readBeginSlow`, to get one state of the three
header fields, and a writer holds the map for a short time and runs no
user code meanwhile. The rest of a read is wait-free: a bounded probe,
retried a bounded number of times. Without the bound a writer in a loop
could starve a reader forever, as the time between two of its writes is
shorter than a lookup.

## 2. Why each racy load is safe

| What a reader loads | Why it is safe |
| --- | --- |
| `used` | One word (`len(m)`, `reflect`): any value is only a length. Nothing indexes with it. |
| `seed` | A hash from a stale seed finds the wrong slot or none; the key comparison decides. |
| `dirPtr`, `dirLen`, `globalShift` | They must go together. They are loaded between two loads of `seq` (`readFields`, `readOK`), and only written with it odd, so an index computed from them is in bounds of that directory. |
| A directory entry | One pointer, to a table initialized before it was stored (§3). Directories are never reused; an old one stays valid, and it is what a stale reader sees. |
| `t.groups` (`data`, `lengthMask`), `t.capacity` | **Never change** once the table is published: growth makes a new table. So a probe stays in the table's array whatever the control words say. |
| `t.index`, `t.localDepth` (iteration) | Plain words. The iterator only uses them to choose what to visit; it indexes with its own bounds, and always advances (`nextDirIdx`). |
| Control words | Bytes that only choose slots. A probe that has seen every group stops (`probeSeq.last`), so one that a writer keeps refilling terminates. |
| Key slot | Compared with `Equal`, which reads it whole (§4). A slot whose pointer is nil (indirect key being filled or deleted) is no match. |
| Element slot | Returned as a pointer; the caller loads it whole. An indirect element's nil pointer is no match. |

Old tables, groups and directories stay alive through the collector for as
long as a reader has a pointer to them, so a stale read is a read of real
memory of the right type.

The final check of `seq` is therefore **not what makes a read safe**. It
makes its answer the answer of one state of the map: without it a lookup
could, say, take a key's slot as it is being reused for another key. When
the retries run out the reader returns such an answer, which is stale but
made only of things that were stored.

### Slots and their contents

- **Keys are read whole.** The functions of `internal/runtime/maps` are
  compiled with the guards of ordinary code (`ssagen.runtimeTearFunc`), so
  a string in a slot is one 16-byte load, and so are the runtime's
  `strequal`, `interequal` and `nilinterequal`. The generated `.eq.`
  functions load each string and interface **once**, into a temporary
  (`reflectdata/alg.go`): a second load could find a shorter string than
  the length just compared, or the payload of another interface.
- **An indirect key's memory never changes** once it is in a slot.
  Updating a key that compares equal (+0 and -0, a string with another
  array) stores a new object (`updateKey`), so a reader, or an iterator
  hashing a key from a stale table, reads immutable memory.
- **Stores** of keys go through the same guards, and `typedmemmove` and
  `typedmemclr` copy and clear whole (`tearKind`, `moveWhole`,
  `clearWhole`).
- **A clear never zeroes a control word.** It sets the group empty first
  and then clears the slots (`typedmemclrSlots`). A zero control word
  would say that all eight slots are full. A delete likewise marks the
  slot before clearing it.

### What a racing reader can still see

All of these are memory safe, and none happens without a data race:

- A stale value, or a miss for a key that was just inserted.
- **A slot's element after the slot was reused.** The compiled code reads
  the element after the lookup returned its address, so a lookup of `k`
  that races with `delete(m, k)` and an insert of `k2` in the same slot
  can return the element of `k2`.
- **A zero key or element**, in a slot that a delete or a clear got to
  after it was found.
- A key or element that is plain data larger than a word, torn between two
  values (`[8]int`), as it can be anywhere else in Go. Only the words that
  must go together (strings, slices, interfaces) are guarded.
- An iteration that returns an entry twice or misses one.
- A lost update: a write stores its element after `mapassign` returned, so
  a growth by another goroutine meanwhile can leave it in the old table.

## 3. Memory ordering

- **Writer.** The compare-and-swap, then a store barrier
  (`storeBarrier`, `DMB ISHST` on arm64): `seq` is seen odd before any
  store of the write. (A CAS built from load-exclusive and store-exclusive
  does not order later stores after its store.) `unlockWrite` is a release
  store, so all of a write is seen before `seq` is seen even.
- **Publication.** Before a table, a group or a directory is stored where
  a reader can find it (`replaceTable`, `installTableSplit`,
  `growToSmall`, `growToTable`), there is a store barrier; the reader's
  load through the pointer is ordered by the address dependency. `NewMap`
  and `Clone` have one at the end, since the pointer to the new map may be
  stored racily by the caller.
- **Reader.** `readFields` loads `seq`, `dirLen`, `dirPtr` and
  `globalShift` with atomic loads, and `readOK` loads `seq` with one: on
  arm64 these are `LDAR`s, which stay in order among themselves, so no
  fence is needed there. Before the **final** check, `loadBarrier` orders
  the loads of the lookup before it (`DMB ISHLD` on arm64, an intrinsic; an
  atomic operation on a word of its own on the other weakly ordered
  targets; nothing on amd64, 386 and wasm).

## 4. All the paths

- `Get`, `getWithKey`, `runtime_mapaccess{1,2}` and their `fast32`,
  `fast64` and `faststr` variants: `readFields`, `readOK`, a bounded
  probe, `readRetry`. `reflect` calls these.
- `Put`, `PutSlot`, `Delete`, `Clear`, `runtime_mapassign*`, growth and
  splits: with the map held.
- **The small form** (`dirLen == 0`): `dirPtr` is a group, and is told
  from a directory by the `dirLen` read with it. No `dirPtr` yet is an
  empty map.
- **Iteration.** `Init` and every `Next` read the directory fields as one.
  The iterator keeps its old table as stock Go does. Racing with growth,
  it looks keys up in the new tables; with a clear it finds empty slots.
- **`Clone`** is a read: it copies, and throws the copy away if a write
  overlapped it. After `mapCloneTries` (4) failures it holds the map while
  it copies, since a writer in a loop would otherwise starve it, so a
  write that races with such a clone may panic.
- `len(m)` is one load of `used`.
- The compiler inlines no map lookup. `Map`'s layout is known to
  `reflectdata/map.go`; `seq` took the place of the old `writing` flag and
  of the sharing lock.

## 5. Targets

- **amd64, arm64**: as above. Where a 16-byte access is not atomic (no AVX,
  no LSE2), strings go through the sequence stripes as everywhere else.
- **Other 64-bit targets**: the same, with their atomics; the barriers
  above are the portable ones.
- **32-bit targets**: the map's structure is protected the same way, but
  strings and slices are not loaded whole there (`slice16.md` §6), in map
  slots as anywhere else. A `uint64` key is two words.
- **`GOWASM=gc`** has its own maps in `runtime/wasmgc`, and is not affected.
  Other wasm has no parallel goroutines, and no barriers.
- **`GOEXPERIMENT=sharing`**: the old reader-writer lock of the map is
  gone. Its readers are the readers above. Its writers use `seq` too, but
  on a map that is shared they **wait** for it instead of panicking
  (`lockWriteSlow`), which is what `TestSharedMapRace` expects of the
  experiment.
