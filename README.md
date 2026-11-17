# compiler.gd

`gd` is a compiler for the Go programming language that aggressively allocates
on the stack. Intended to reduce GC-pressure in real-time graphics and
to enable carefully designed programs to run without a GC. Try it!

### What's different?

1. Interfaces include 16 bytes of inline storage (strings and slices both fit).
2. Small string optimizations (stores 15 bytes without allocating or a hash).
3. Runtime escape-analysis for dynamic dispatch (closures and interfaces).
4. Pointer results can be allocated on the stack (if they don't escape).
5. The entire compiler + standard library in a single self-contained binary.
6. Small wasm/js binaries, under 500kb for a fmt hello world, no dispatch loop.

### Does it work?

The language + standard library test suites pass on `amd64`, `arm64` & `js/wasm`.

### How? Why?

@Splizard, the project's director, developed an interest (after 10 years of Go)
in exploring what an alternative Go compiler could look like. Recent technology
has made an endeavour of this scale far more accessible. The resulting compiler
is intended to support the https://graphics.gd project.

### Contributing

Contributions may be a better fit for the upstream `gc` compiler (https://go.dev). 
If you prefer to support *this* project,
[consider a sponsorship](https://github.com/sponsors/Splizard).
If you've run into a compiler bug and you've confirmed it's unique to `gd`, you
may open a PR to fix it; include an analysis of the issue + new tests. For larger
changes please don't open a PR unless we've coordinated on a discussion thread.
