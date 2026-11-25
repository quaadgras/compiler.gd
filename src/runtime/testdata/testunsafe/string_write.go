package main

import (
	"fmt"
	"os"
	"unsafe"
)

// The bytes of a string constant are stored in.

var vn int

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }
func ok()                    { fmt.Println("OK"); os.Exit(0) }

//go:noinline
func text() string { return "a string that is longer than fifteen bytes" }

func main() {
	s := text()
	p := unsafe.StringData(s)
	*(*byte)(unsafe.Add(unsafe.Pointer(p), vn)) = 'X'
	if text()[0] == 'X' {
		unsafeDone("changed the bytes of a string constant")
	}
	ok()
}
