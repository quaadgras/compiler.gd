# compiler.gd

A compiler for the Go programming language that aggresively avoids allocations. 

Pass `-compiler=gd` to compiler.gd's Go command to use the compiler, otherwise 
it will fallback to Google's `gc` compiler. All `gd` specific optimizations are
feature flagged under `runtime.Compiler == "gd"` or `go:build gd`.

### Optimisation Goals

1. Interfaces include an additional 128bits for storing values directly.
2. Small string optimization.
3. Dynamic escape bits for closures and interfaces.
4. `func() (A, B, C...)` stored in memory like a tuple.
5. Reduced CGO overhead.

### Compatibility Goals

1. Provide a Go runtime interface, that can be implemented in C or assembly to support bare metal builds.
2. Provide a stable register-based ABI.
