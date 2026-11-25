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
	// What is 64 KB after a variable of the data: another variable,
	// or what the runtime keeps between and after them.
	u := uintptr(unsafe.Pointer(&victim)) + 1<<16
	u ^= uintptr(vn)
	p := (*uintptr)(unsafe.Pointer(u))
	*p = 1
	unsafeDone("stored 64 KB after a variable, through an integer")
}
