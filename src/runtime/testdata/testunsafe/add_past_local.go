package main

import (
	"fmt"
	"os"
	"unsafe"
)

var victim = [16]byte{115, 97, 102, 101}
var sink unsafe.Pointer
var vn int

//go:noinline
func launder(p unsafe.Pointer) unsafe.Pointer { sink = p; return sink }

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }
func ok()                    { fmt.Println("OK"); os.Exit(0) }

var _ = vn

// idx returns vn, where the compiler does not see that it does: what is
// loaded at an index of it is loaded, and what was stored there is not a
// store that nothing reads.
//
//go:noinline
func idx() int { return vn }

func main() {
	var x [8]int64
	var y [8]int64
	vn = 12
	p := (*int64)(unsafe.Add(unsafe.Pointer(&x), vn*8))
	*p = 1
	_ = y
	vn = int(*(*int64)(unsafe.Add(unsafe.Pointer(&x), idx()*8)))
	unsafeDone("stored past a local")
}
