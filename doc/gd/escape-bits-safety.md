# Escape-bits: safety properties

What escape-bits (`escape-bits.md`) must preserve, what each of its two
mechanisms has to guarantee for that, where the compiler and runtime
establish each guarantee, and what checks it. This is an argument, not
a mechanised proof: it is here so that a change can be checked against
a list instead of against intuition, and so that a miscompile can be
filed under the obligation it broke.

## 1. The properties

Stock Go decides at compile time where each variable and allocation
lives. Its escape analysis guarantees the first two properties below,
and the runtime depends on them (the collector does not trace pointers
into stacks from elsewhere, and a returned frame's memory is reused).
The other two hold trivially in stock Go and stop being trivial once
storage can move or be shared.

**S1. Nothing outside a stack points into a returned frame.** At any
time, no pointer stored in the heap, in a global, or in another
goroutine's stack refers to memory of a frame that has returned.

**S2. A frame is referred to only from itself and from its callees.**
A pointer stored in frame F, or in a variable of F, that points into
F's own stack points into F or into a frame older than F (a caller's).
So while F is live, memory of F is referred to only by F and by frames
younger than F, which return before F does.

S1 and S2 together mean there are no dangling pointers: when F
returns, everything that could refer to its memory has returned.

**S3. One object, one place.** At any time each live variable or
allocation has exactly one storage location, distinct live objects
have disjoint storage, and every read and write of an object, through
its name or through any pointer to it, acts on that location.

**S4. Not observable.** A program behaves as it does when every
variable and allocation that escape-bits treats specially is on the
heap from the start, as stock Go has it, except in how much it
allocates.

S4 is the specification; S1–S3 are what could break it. `-d=gdescbitsoff`
compiles a package the stock way, so S4 can be tested by comparing the
two (§5).

## 2. Re-homing a local (R)

A *candidate* is a local `x` of function F that F keeps in its frame at
`b(x)` and reaches only through a *box* `B(x)`, a pointer in F's frame
that starts as `&b(x)`. At a call `c` of a function value or interface
method (a *dynamic* call) with argument `&x`, F reads the callee's
mask; if the bit for that argument is set, it copies `x` to a new heap
object `h`, sets `B(x) = h`, and passes `h`.

**R1. The mask is complete.** If the function that `c` calls, given a
pointer to memory `s` as argument k, can make a pointer into `s` exist
after it returns, anywhere at all (the heap, a global, another
goroutine, a result, a variable of a function that is still running),
then bit k+1 of the mask that `c` reads is set.

- R1a, *the notes are complete*: escape analysis of the callee records
  each such flow in the parameter's note, as a flow to the heap or to
  a result. This is stock Go's analysis and its soundness is assumed,
  with one addition: a parameter without a name has a variable, and
  flows, in the wrappers made from an interface method's signature
  (`paramTag`).
- R1b, *the mask is the notes*: a bit is set for a flow to the heap or
  to any result, and for a parameter without a note. One function
  computes every mask (`ir.EscMaskOf`).
- R1c, *every callee has its mask where the call site looks*: at
  offset `PtrSize` of every function value (function symbols,
  closures, method values, reflect's `makeFuncImpl` and
  `methodValue`), and in the mask slots of every itab (compiled, and
  built by the runtime from the type's method masks). A mask that is
  not known is all ones; zero means "keeps nothing".
- R1d, *a computed mask is the mask of what will run*: a forwarder's
  mask is read from the inner interface value in its receiver. Between
  that read and the call no code of the program runs (the receiver and
  the other arguments were evaluated before), so the inner value is
  the one the call uses, unless another goroutine writes it, which is
  a data race.
- R1e, *no bit, no candidate*: an argument past the 63 the mask has
  bits for is never `&x` of a candidate (`dynamicArgHole`).

**R2. The box is the only reference.** When `c` re-homes `x`, no
pointer to `b(x)` exists anywhere but in `B(x)`. Statically
(`location.addrHeld`, `noteAddrTaken`): `x` is a candidate only if
every flow of its address goes to a dynamic call as a direct `&x`
argument, or into a statically known callee that does not keep it; no
location of F holds its address (a pointer variable, a field, a
closure that captures `x` by reference, a range or switch temporary,
an inlined parameter); and no statement both passes `&x` to a dynamic
call and takes `x`'s address some other way, since order and walk may
evaluate the other one into a temporary first.

A statically known callee that is handed `&x` holds it only while it
runs, and a re-home of `x` happens only in F's own code, which is
suspended meanwhile. (F's code in a closure would have captured `x` by
reference.)

R2 is about what the compiler can see. A pointer hidden from it
(`abi.NoEscape`, a `uintptr`) is outside the contract, as it is in
stock Go, with one exception that stock Go keeps working by moving
stacks as a whole: a pointer that `x` holds to itself. The copy rebases
those (`rebaseSelfPointers`).

**R3. Every access goes through the box.** All of F's reads and writes
of `x`, and every `&x`, use `B(x)` (`Name.Heapaddr`), so they follow
the move. Each execution of `x`'s declaration starts a new variable:
`B(x) = &b(x)` again (`emitEscapeBoxReset`). The previous variable's
memory is free for that: either it was re-homed, and what refers to it
refers to its heap copy, or it was not, and by R1 and R2 nothing
refers to it.

**R4. The copy is the variable.** The copy is a typed copy made while
nothing else can run on the goroutine's stack (`materializeToHeap` is
nosplit) and nothing else can refer to `x` (R2), and it happens at
most once per variable (a box that already points off the stack is
left alone).

**R5. What a candidate points to may be pointed to from the heap.** An
object whose address is stored in a candidate is not on a stack: it
escapes (the address of such an object reaches the candidate location
without being a direct argument, so it is not itself a candidate; a
call's result buffer is not a candidate at all).

*Why S1–S3 follow.* Before a re-home, `b(x)` is referred to by `B(x)`
in F and by callees given `&x` for the length of a call (R1, R2): S1
and S2. After it, the callee and everything it hands the pointer to
refer to `h`, on the heap. `x` has one location at any time, `b(x)` then
`h`, and no reference to the old one survives the switch (R2, R3, R4):
S3. What `x` holds is valid in either place (R5).

## 3. Result buffers (B)

For a call `c` whose callee has a pointer result of type `*T`, the
caller F may reserve a *buffer* `u(c)` of type `T` in its frame and
pass `&u(c)`; otherwise it passes nil. The callee G may make one
object, the *occupant*, in the buffer it was passed, or pass the
buffer on to one call of its own whose result becomes G's.

**B1. The caller's result dies with the buffer.** If F passes `&u(c)`,
no pointer derived from the result of that execution of `c` is used
after F returns, nor after `c` is executed again in the same
activation of F. Statically: the call has a location of its own, an
allocation at the call's loop depth whose address the result is
(`escape.call`), and F passes a buffer only if that location does not
escape under the stock rules (the heap; a result of F; a variable
declared outside the call's loop; an argument of a dynamic call, where
a pointer variable always goes to the heap; the contents of a
candidate, by R5).

**B2. The buffer is a `T` the collector can scan.** It has the
result's element type, it is a stack object of F that is never merged
with another slot, and it is zeroed on entry to F.

**B3. One occupant.** During one invocation of G, at any time at most
one live object is in the buffer G was passed. G places an allocation
(a `new`, an `&T{}`, a local, the result of a call it forwards the
buffer to) there only if (`escape/phaseg.go`):

- a. it has exactly the type `T`, and is an allocation of G itself, not
  of a closure of G;
- b. its address reaches nothing that outlives the invocation other
  than the result the buffer is for: not the heap (including what is
  stored where something already escapes), not a dynamic call's
  candidate location, not another result of any function analysed
  with G;
- c. no earlier execution of it is live when it executes again: no
  variable declared outside its loop holds it (a local, a parameter,
  or the result itself when G assigns it by name, as in `for ... { r
  = &T{next: r} }`);
- d. it is the only allocation whose address reaches that result, or
  every such allocation is itself the operand of a return statement,
  which ends G as soon as it is made.

**B4. Nil is always right.** G behaves the same with a nil buffer,
allocating on the heap (`maybeInPlace`), so whether a caller passes a
buffer affects nothing but allocation, and a caller that can't see G
(reflect, a `go` or `defer` wrapper, the finalizer goroutine) passes
nil.

**B5. Caller and callee agree that the argument exists.** Every
function type with a pointer result has the buffer parameter, in every
package, and so does every caller's view of it: wrappers, method
values, reflect's `Call` and `MakeFunc`, the runtime's builtins (see
"Builtin/runtime sig parity"), and the runtime's own calls of Go
functions through `reflectcall`, which must leave the argument zero
and count only the declared parameters.

**B6. The buffer is only the occupant's storage.** G zeroes the buffer
when it places the occupant and otherwise does not touch it, so what an
earlier call left there is never seen.

*Why S1–S3 follow.* The occupant's memory is in F's frame. During the
call it is referred to by G and G's callees, younger frames (B3b: by
nothing else); afterwards by F, through the result, and by nothing
that outlives F or the next execution of `c` (B1): S1, S2. No two live
objects are in the buffer (B1 between executions of `c`, B3 within
one, B5 so that the buffer G uses is one a caller reserved), and the
occupant never moves: S3.

## 4. What is assumed

- Stock escape analysis is sound for what it reports (R1a, B1, B3b
  build on its flow graph).
- No data races on what the masks are computed from (R1d).
- No pointers hidden from the compiler other than self pointers (R2).
- Function values and itabs are made by this toolchain or by
  `reflect` (R1c): a function value assembled by hand from a code
  pointer has no mask word, and whatever follows the code pointer is
  read as one.
- Assembly that calls a Go function with a pointer result passes the
  buffer argument (B5).

## 5. What checks what

| | static | at run time | by test |
|---|---|---|---|
| S1 | — | `GODEBUG=stackptrcheck=1`: the collector throws on a pointer, found in the heap, a global or another stack, into the part of a parked goroutine's stack below its stack pointer | std tests under the checker; `TestEscapeBitsRandom` |
| S2 | — | same setting: the collector, and every stack copy, throw on a pointer in a frame or stack object that points below that frame | same |
| S3, S4 | — | — | `TestEscapeBitsRandom`: random programs built with and without `-d=gdescbitsoff` print the same |
| R1a–c | `ir.EscMaskOf`, `paramTag`, `walkMethodValue`, `writeEscMask` | — | `escape_bits_mask_test.go`, `escape_bits_dispatch_test.go` |
| R1d | forwarder shape (`escape/forwarder.go`) | — | `escape_bits_dispatch_test.go`, `escape_bits_forwarder_test.go` |
| R1e | `dynamicArgHole`; walk refuses a candidate past the mask | — | `TestEscapeBitsMaskManyArgs` |
| R2 | `addrHeld`, `noteAddrTaken` | `rebaseSelfPointers` | `escape_bits_alias_test.go`, `TestEscapeBitsSelfPointerMoves` |
| R3, R4 | `promoteEscapeCandidates`, `emitEscapeBoxReset` | `materializeToHeap` | `escape_bits_test.go`, `escape_bits_alias_test.go` |
| R5 | `finish` (a non-candidate that reaches the candidate location escapes) | — | random programs |
| B1, B2 | `escape.call`'s spill, `giveOutBuf` | — | `escape_bits_outbuf_alias_test.go` |
| B3 | `phaseGFunc` | — | `escape_bits_outbuf_return_test.go`, `escape_bits_outbuf_loop_test.go` |
| B4, B6 | — | `maybeInPlace` | `escape_bits_outbuf_test.go` |
| B5 | `types.PhaseGApplies`, `abi.FuncType.NumUserIn` | — | `TestEscapeBitsOutBufFinalizer`, reflect's tests |

The run-time checker is exact about what it reports (it has no false
positives on the std tests) but not complete: it sees a pointer only if
a collection or a stack copy happens while the pointer is there, and
it does not report a pointer from the heap into a frame that is still
live (the runtime has such pointers of its own), which is a violation
of S1 only later, when the frame returns.

Miscompiles found so far, by the obligation they broke: R1b (function
symbols ignored result flows), R1a (parameters without names), R1c
(method values with another package's wrapper; forwarder method
expressions; reflect's function values, before), R1e (arguments past
the mask), R2 (aliases, interior pointers, captures, before; self
pointers), B3c (a result assigned by name in a loop), B3d (two live
calls forwarded the same buffer, before), B5 (`SetFinalizer` counted
the buffer parameter).
