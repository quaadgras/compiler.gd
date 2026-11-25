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
	x := new(uintptr)
	*x = uintptr(unsafe.Pointer(&victim))
	f := *(*func())(launder(unsafe.Pointer(&x)))
	f()
	unsafeDone("an integer was called as a func")
}
