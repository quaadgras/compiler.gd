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
	pp := new(*byte)
	*pp = new(byte)
	b := unsafe.Slice((*byte)(launder(unsafe.Pointer(pp))), 8)
	a := uintptr(unsafe.Pointer(&victim))
	for i := 0; i < 8; i++ {
		b[i] = byte(a >> (8 * i))
	}
	**pp = 88
	if victim[0] == 88 {
		unsafeDone("a pointer was written byte by byte through unsafe.Slice")
	}
	ok()
}
