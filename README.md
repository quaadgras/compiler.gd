# compiler.gd

`gd` is a compiler for the Go programming language that aggressively allocates
on the stack. Intended to reduce GC-pressure in real-time graphics and
to enable carefully designed programs to run with the GC turned off. Try it!

### What's different?

1. Interfaces extended with 16 bytes of inline storage (directly store strings, slices and any numeric type).
2. Small string optimizations (up to 15 bytes inline or a hash for strings that are on the heap).
3. Runtime escape-analysis for dynamic dispatch (closures and interfaces).
4. Pointers returned from functions may be allocated on the stack (if they don't escape).
5. The `go` command embeds the standard library as well as `asm`, `cgo`, `compile` and `link`.

### Does it work?

`all.bash` tests are currently passing on `amd64`, and the standard library tests pass on
`arm64` and on `js/wasm` (under Node.js).

On `js/wasm` every goroutine runs on its own WebAssembly stack, which needs an engine with
JavaScript Promise Integration (Chrome 137, Firefox 153, Safari 27, Node.js 24), and functions
use structured control flow rather than a dispatch loop. `wasip1` is untested.

### How?

I directed the high level design and an LLM performed the implementation work. 
I have a deep understanding of the language but not a comprehensive understanding 
of the `gc` compiler, AI made it possible to explore a different set of compiler 
tradeoffs without significant time & attention cost (I have other things to do).

### Contributing

Authentic individual contributions are a better fit for the upstream `gc` compiler 
at https://go.dev. If instead, you would like to contribute to this fork, AI or LLM
assistance is recommended given how it is built. Please open/comment on an issue 
before opening any PRs so that any contention can be coordinated.
