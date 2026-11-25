package main

import (
	"fmt"
	"os"
	"unsafe"
)

// A constant is added to the address of a local and of a global, past
// each: the compiler has the sum for an address of its own.

var g [8]int64
var after [8]int64
var sink int64

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }
func ok()                    { fmt.Println("OK"); os.Exit(0) }

//go:noinline
func local() int64 {
	var x [8]int64
	var y [8]int64
	*(*int64)(unsafe.Add(unsafe.Pointer(&x), 96)) = 77
	use(&y)
	return y[0] + y[4]
}

// use keeps y in memory: what was stored past x is stored, if it is
// stored anywhere.
//
//go:noinline
func use(p *[8]int64) { sink += p[1] }

func main() {
	if local() == 77 {
		unsafeDone("stored a constant offset past a local")
	}
	*(*int64)(unsafe.Add(unsafe.Pointer(&g), 96)) = 78
	if after[0] == 78 || after[4] == 78 {
		unsafeDone("stored a constant offset past a global")
	}
	ok()
}
