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

var gx [8]int64
var gy [8]int64

func main() {
	vn = 1000
	p := (*int64)(unsafe.Add(launder(unsafe.Pointer(&gx)), vn*8))
	*p = 1
	_ = gy
	unsafeDone("stored 1000 words past a global")
}
