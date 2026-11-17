//go:build !wasm.split

package main

// Go's use of a word that C tagged panics (natively with SetPanicOnFault,
// as the kernel gives no address; on wasm, the compiler's nil checks).
const derefPanics = true

// stacksMove says whether a goroutine's stack is copied when it grows.
const stacksMove = true
