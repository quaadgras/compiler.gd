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

func leak() *[64]byte {
	var buf [64]byte
	u := uintptr(unsafe.Pointer(&buf))
	return (*[64]byte)(unsafe.Pointer(u))
}

//go:noinline
func spray() int {
	var a [128]int
	for i := range a {
		a[i] = 0x41414141
	}
	return a[vn]
}
func main() {
	p := leak()
	spray()
	if p[0] != 0 {
		unsafeDone("a pointer to a local outlived its function")
	}
	p[0] = 1
	ok()
}
