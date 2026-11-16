# Contributing to compiler.gd

compiler.gd is a fork of the Go toolchain that allocates aggressively on the
stack (see [README.md](README.md)). Thank you for your interest in it.

## Before you start

Authentic individual contributions are a better fit for the upstream `gc`
compiler at https://go.dev. If a change makes sense for Go in general,
please propose it there.

For this fork, open or comment on an
[issue](https://github.com/quaadgras/compiler.gd/issues) before opening a
pull request, so that any contention can be coordinated.

## Filing issues

Please include:

1. The output of `go version`.
2. The operating system and architecture, and for `GOARCH=wasm` the host
   (Node.js version or browser) and `GOWASM` setting.
3. What you did, ideally as a small program.
4. What you expected to see, and what you saw instead.

If the same program behaves the same way with the upstream Go release this
fork is based on, the issue probably belongs upstream.

## Contributing changes

AI or LLM assistance is recommended, given how the fork is built. Whatever
the tools, you are responsible for the change and for checking it.

- Keep a change to one concern, with a commit message in Go's style
  (`pkg/path: what changed`, then why).
- Design notes for the fork's larger changes are in [doc/gd](doc/gd); update
  them when a change alters a design.
- Run the tests that cover the change before sending it: `all.bash` for the
  toolchain and runtime, and for the WebAssembly ports the standard library
  tests under Node.js (`GOOS=js` or `GOOS=wasip1`, with
  `PATH=$GOROOT/lib/wasm:$PATH`).

Unless otherwise noted, the source files are distributed under the BSD-style
license found in the [LICENSE](LICENSE) file.
