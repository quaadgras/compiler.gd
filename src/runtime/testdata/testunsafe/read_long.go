package main

import (
	"fmt"
	"os"
	"unsafe"
)

// A file is read into a slice that says it is longer than what it
// points into: the system, or the host, writes at an address.

var vn int

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }
func ok()                    { fmt.Println("OK"); os.Exit(0) }

func main() {
	f, err := os.Open(os.Args[0])
	if err != nil {
		ok()
	}
	x := new([8]byte)
	vn = 4096
	s := unsafe.Slice(&x[0], vn)
	n, _ := f.Read(s)
	if n > 8 {
		unsafeDone("read 4096 bytes of a file into 8")
	}
	ok()
}
