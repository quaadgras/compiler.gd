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
	var x [8]int64
	var y [8]int64
	vn = 12
	p := (*int64)(unsafe.Add(unsafe.Pointer(&x), vn*8))
	*p = 1
	_ = y
	unsafeDone("stored past a local")
}
