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
	m := map[int]int{1: 2}
	p := (*uintptr)(launder(unsafe.Pointer(&m)))
	*p = uintptr(unsafe.Pointer(&victim))
	m[3] = 4
	unsafeDone("the pointer of a map was changed as an integer")
}
