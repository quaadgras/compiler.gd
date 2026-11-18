# Goroutine collection (gd fork)

A goroutine that waits on a channel, a `select`, a mutex, a wait group or
a condition variable that no goroutine that can run reaches will wait
forever: nothing can wake it. Go keeps it, its stack and everything it
holds for the life of the program. This fork collects it.

## How

The runtime already finds such goroutines for the goroutine leak profile
(`runtime/mgc.go`, `findGoroutineLeaks`): in a cycle that looks for them,
only the stacks of goroutines that can run are roots; when marking
settles, a blocked goroutine whose channel or semaphore got marked is one
that can be woken, its stack becomes a root, and marking resumes; what is
left when no more are found is blocked forever.

In a collecting cycle those goroutines are not marked and reported but
freed (`gcCollectGoroutine`, with the world stopped):

- a goroutine that waits for a semaphore leaves the table of semaphores,
  which outlives it, with every other goroutine that waits for the same
  one (all of them collected in the same cycle);
- its other sudogs are in the queues of channels or of a condition
  variable that are unreachable as they are, and are garbage together;
- its stack is freed and its `g` goes back to the free list, as when a
  goroutine returns.

Nothing that only it held is marked, so that is freed by the same cycle,
and the finalizers and cleanups of it run. Its deferred functions do not
run, as they would not have.

## When

In every cycle a program forces with `runtime.GC`, and in one automatic
cycle in four (`collectGoroutinesEvery`). A cycle the leak profile asked
for reports and does not collect.

## What is never collected

The main goroutine (if every other goroutine is collected the program
ends with "all goroutines are asleep", as it does now when they are all
blocked), a goroutine locked to a thread, one in a `synctest` bubble, one
in a panic, the runtime's own. A goroutine that is not collected is a
root: what it holds stays, the goroutines blocked on that included.

Not on wasm, and not under the race detector, msan, asan, valgrind or
execution tracing. `GODEBUG=collectgoroutines=0` turns it off.

## What can hide a reference

The collector trusts what the garbage collector sees. A channel kept only
in a `uintptr`, or in memory C owns, is unreachable to it. A small object
without pointers (a `sync.Mutex` alone is 8 bytes) shares its block of
memory with others, and is reachable while any of them is: its waiters
are kept.

## Tests

`runtime/gccollect_test.go`: fifty goroutines blocked forever in six
ways are collected and what they held is finalized; goroutines that can
still be woken, directly or through another blocked goroutine, are kept
and run when they are; collections run while goroutines are started,
leaked and woken.
