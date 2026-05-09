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

`all.bash` tests are currently passing on `amd64`. Support for `arm64` and `wasm` is roadmapped.

### Disclaimer

Go compilers are large and complex pieces of software, I do not have a comprehensive 
understanding of the `gc` compiler but I do have a deep understanding of the language. 
As such, I directed Claude Opus 4.7 to make partially-supervised & high-level changes 
until the tests passed.

### Contributing

Please consider contributing to the `gc` compiler by Google. If you really really want 
to contribute here, it is advised to direct an LLM to make your intended contributions. 
Please open an issue if you intend to open a PR (in order to reduce contention).
