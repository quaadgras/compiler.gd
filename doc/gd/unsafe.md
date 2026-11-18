# Checked unsafe.Pointer (gd fork)

Goal: a program that misuses `unsafe.Pointer` stops, instead of reading
or writing memory that is not its own. The rules are Go's (package
unsafe's six patterns); the fork checks them in every build.

> **Status:** in: the checks Go has under the race detector
> (`-d=checkptr`) are the default, made cheaper for the usual cases, and
> extended with a check of where the pointers are; and an integer that
> was kept (a `uintptr` field, an argument of a callback, which C-style
> APIs have) is a pointer again if its object is exposed. Not yet: the
> integer keeping its object alive, bounds of stack objects and globals,
> and exact sizes in the heap (an object's bound is its size class).

## What is checked

The compiler adds a call where a pointer comes out of `unsafe`
(`cmd/compile/internal/walk`, `ssagen`), and the runtime looks at the
memory (`runtime/checkptr.go`). A check that fails is a fatal error.

- **`(*T)(p)`**, `p` an `unsafe.Pointer` (`checkptrAlignment`): `p` is
  aligned for `T` if `T` has pointers; the `T` at `p` is inside one
  allocation; and where `T` has a pointer, the object has one too, or
  has a word that does not hold the address of anything in the heap. The
  last is new: an integer that happens to be an address is not read as a
  pointer. (The first word of an interface is a word the collector does
  not look at that points to a type or a method table, outside the heap,
  and reflect reads it as a pointer: that stays allowed.)
- **`unsafe.Pointer(u)`**, `u` a `uintptr` (`checkptrArithmetic`): if the
  result is in a heap object, one of the pointers the expression was
  computed from is in the same object, or the object is *exposed*.

  An object is exposed when the program makes an integer of its address
  and keeps it: compiled code calls `runtime.exposeptr` where it converts
  a pointer to a `uintptr`, unless the integer only lives in an
  expression that makes a pointer of it again, or the pointer is plainly
  to a variable of the stack. The mark is a bit for the object, kept as
  the shared bit is. So `h := uintptr(unsafe.Pointer(p))` kept in a field
  and `(*T)(unsafe.Pointer(h))` later work, and `(*T)(unsafe.Pointer(h +
  64))` does if it is still in `p`'s object, and not if it is in the
  next one, which never gave its address away. This is what C calls an
  exposed address (PNVI-ae). The integer does not keep the object alive:
  once it is freed the integer is the address of nothing exposed.
- **`unsafe.Add`**: as the arithmetic.
- **`unsafe.Slice`, `unsafe.String`**: the result is inside one
  allocation. (What the language says panics still panics, and is not a
  fatal error: `unsafeslicecheckptr` uses the caller's pc.)

## Where it is not

- The runtime's packages and `internal/c23` (C's runtime, which keeps
  pointers the checks would refuse and has its own) are compiled without
  the checks. A function of theirs that is inlined in checked code has
  its conversions marked not to be checked (`ir.NoCheckPtrIn`), rather
  than not being inlined, as it was with `-d=checkptr`: `abi.TypeOf` and
  `stringslite.HasSuffix` inline as before. The same for a function
  marked `//go:nocheckptr`.
- Package reflect is compiled without the checks too: its pointers are
  to values of the types it says they are, by construction, but for
  `reflect.NewAt`, which is `(*T)(p)` and is checked as that
  (`reflect.checkptr`). Its functions inline in checked code as the
  runtime's do.
- The compiler's own conversions: a result built in the caller's buffer,
  the element pointer of a range loop, the key and element of a map's
  iterator (anything made with `typecheck.ConvNop`), the equality and
  hash functions it writes. These were checked on every iteration with
  `-d=checkptr`, which nobody ran in production.
- A function marked `//go:uintptrescapes` or `//go:uintptrkeepalive` is
  given pointers as integers, which its callers keep alive for it: the
  pointers it makes of integers are not checked.
- wasm, for now.

`-gcflags=all=-d=checkptr=0` turns the checks off.

## What it does not see

- A pointer stored, through a converted pointer, in a word the collector
  does not look at; an integer stored where the object has a pointer. A
  conversion cannot tell what will be stored.
- A pointer one past the end of an object is, to these checks, a pointer
  to the next object.
- The stack is one allocation, and so is each of data and bss.

- An integer that outlives its object and is then the address of
  another exposed object is a pointer to that one.

These need the capability to travel with the pointer, as it does for C
(`cc.md`): the plan is the thin representation agreed on 2026-11-18, the
address in the word and the capability beside it only for a word that
cannot give it back (an integer, a pointer out of bounds). Exposure is
the part of it that needs no side table.

## What it costs

The checks are calls. With reflect and the compiler's own conversions
out of them, `encoding/json`'s encoder and decoder and `fmt` cost what
they do without the checks; `net/http`'s header writer, which makes
strings of bytes with `unsafe.String`, is a fifth slower. The usual
cases are found without looking for the allocation of each end: the
goroutine's stack, one object of the heap, a pointer converted back to
the type its object was allocated with, and memory outside the heap.

## Tests

`runtime/checkptr_test.go`, with `CheckPtrPointers*` for the new check;
the standard library's tests pass with the checks on. Six tests of
`test/` that count instructions or diagnostics, or that misuse `unsafe`
on purpose, run with `-d=checkptr=0`.
