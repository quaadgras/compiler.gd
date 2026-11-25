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

type hdr struct {
	p    unsafe.Pointer
	n, c int
}

func main() {
	s := make([]byte, 8)
	h := (*hdr)(launder(unsafe.Pointer(&s)))
	h.n = 4096
	h.c = 4096
	for i := 8; i < 4096; i++ {
		s[i] = 88
	}
	unsafeDone("wrote 4096 bytes through a slice of 8 whose length was changed by a struct over it")
}
