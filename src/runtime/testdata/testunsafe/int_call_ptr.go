package main

import (
	"fmt"
	"os"
	"unsafe"
)

// An integer that a function returns is made a pointer: the address of
// an object that never gave its address away, less a constant.

var other = new([16]byte)
var victim = new([16]byte)
var delta uintptr

//go:noinline
func addr() uintptr { return uintptr(unsafe.Pointer(other)) + delta }

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }

func main() {
	// (Objects of a size that are allocated one after the other are
	// next to each other.)
	delta = 16
	p := (*byte)(unsafe.Pointer(addr()))
	*p = 88
	if victim[0] == 88 {
		unsafeDone("an integer that a function returned was used as a pointer to another object")
	}
	fmt.Println("OK")
}
