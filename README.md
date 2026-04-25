# compiler.gd

A compiler for the Go programming language that aggressively avoids allocations. 

### Optimisation Goals

1. Interfaces include an additional 128bits for storing values directly.
2. Small string optimization (up to 15 characters + inline hash for heap strings).
3. Dynamic escape bits for closures and interfaces.
4. Returned pointers to known types, allocated within a function, often stay on the stack.
5. Fat closures, that store an additional 32 bytes of inline storage.
6. Reduced CGO overhead.
