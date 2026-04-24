# Phase G.2.1 ABI boundary tests

These tests exercise every call-site pattern that routes through
Phase G's outBufK *T param rewrite. The point is to catch ABI
mismatches where one side expects extended (N+K params) and the
other expects stock (N params) for the same underlying function.

Each file is a standalone `go run`-able program that prints
a recognisable line when the outBufs flow through correctly.
Expected runtime behaviour: `Pointer equality matches` / no
corruption / no runtime panic.

When `PhaseGActive = false` in the fork compile tool, the tests
must still compile and run (they're standard Go). When
`PhaseGActive = true`, they must ALSO compile and run, exercising
the extended ABI on the listed boundary.

## Running

```
cd /home/quentin/git/go
for t in test/gd/phaseg/*.go; do
    name=$(basename "$t" .go)
    out=$(GOROOT=$PWD GOTOOLCHAIN=local /usr/lib/go/bin/go run "$t" 2>&1)
    if [ "$out" = "ok" ]; then
        echo "PASS $name"
    else
        echo "FAIL $name: $out"
    fi
done
```

## Case list

| File | Boundary |
|---|---|
| `direct.go` | Plain `func() *T` direct call |
| `variadic.go` | `func(prefix string, xs ...int) *T`, various arities |
| `method.go` | `(*T).Method() *U` direct + via method value |
| `funcval.go` | `var f func() *T = NewT; f()` — function value |
| `named.go` | `type F func() *T; var f F = NewT; f()` — named func type |
| `iface.go` | Interface method returning `*T`, via itab dispatch |
| `closure.go` | Closure captured into `func() *T` |
| `generic_simple.go` | `func Box[T any](x T) *Box[T]` direct call |
| `generic_shape.go` | Same generic instantiated with different `*X` types — shape collapse |
| `generic_closure.go` | `Generic[*int]` used as function value (the motivating case for option A) |

## Known gaps

- cgo boundary — covered separately in a `test/gd/phaseg_cgo/` dir once
  cmd/cgo is updated.
- Asm boundary — covered via existing stdlib tests since few asm
  functions return pointers.
