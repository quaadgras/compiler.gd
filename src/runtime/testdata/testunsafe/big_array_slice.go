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

func main() {
	x := new([8]byte)
	vn = 4096
	s := (*[1 << 30]byte)(launder(unsafe.Pointer(x)))[:vn:vn]
	for i := range s {
		s[i] = 88
	}
	unsafeDone("sliced a pointer to a large array over 8 bytes to 4096")
}
