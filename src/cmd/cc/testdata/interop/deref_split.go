//go:build wasm.split

package main

// With GOWASM=gc,split, Go's use of a word that C tagged traps: the
// program stops (safely), so the demo does not try it.
const derefPanics = false

// stacksMove says whether a goroutine's stack is copied when it grows.
const stacksMove = false
