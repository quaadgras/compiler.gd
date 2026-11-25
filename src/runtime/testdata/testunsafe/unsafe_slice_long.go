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
	x := new([8]byte)
	vn = 4096
	s := unsafe.Slice(&x[0], vn)
	for i := range s {
		s[i] = 88
	}
	vn = int(s[idx()-1])
	unsafeDone("unsafe.Slice of 4096 bytes over 8")
}
