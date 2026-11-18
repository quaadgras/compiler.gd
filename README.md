# compiler.gd

`gd` is a memory-safe compiler for C and Go source code. It allocates
aggressively on the stack. Intended to reduce GC-pressure and increase
security for real-time graphics software running on personal devices.

### What's different?

1. Interfaces include 16 bytes of inline storage (strings and slices both fit).
2. Small string optimizations (stores 15 bytes without allocations or a hash).
3. Runtime escape-analysis for dynamic dispatch (closures and interfaces).
4. Return values (no escape) passed by reference can be allocated on the stack.
5. The entire compiler + standard library in a single self-contained binary.
6. Small wasm/js binaries, under 500kb for a fmt hello world, cgo supported.
7. Strict memory-safety checks on unsafe.Pointer usage and undefined behaviour.
8. Goroutine-collection and runtime race-analysis that prevent memory tearing.

### Does it work?

The language + standard library test suites pass on `amd64`, `arm64` & `js/wasm`.

### How? Why?

@Splizard, the project's director, developed an interest (after 10 years of Go)
in exploring what an alternative Go compiler could look like. Recent technology
has made an endeavour of this scale far more accessible. The resulting compiler
is intended to eventually support the https://graphics.gd project.

### Contributing

Contributions may be a better fit for the upstream `gc` compiler (https://go.dev). 
If you prefer to support *this* project,
[consider a sponsorship](https://github.com/sponsors/Splizard).
If you've run into a compiler bug and you've confirmed it's unique to `gd`, you
may open a PR to fix it; include an analysis of the issue + new tests. For larger
changes please don't open a PR unless we've coordinated on a discussion thread.
