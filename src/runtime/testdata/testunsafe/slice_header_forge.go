package main

import (
	"fmt"
	"os"
	"reflect"
	"unsafe"
)

// The header of a slice is written through a *reflect.SliceHeader: its
// Data is the address of another object, and its Len longer than that.

var victim = new([16]byte)
var after = new([16]byte)
var sink []byte

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }

func main() {
	s := make([]byte, 4)
	sink = s
	h := (*reflect.SliceHeader)(unsafe.Pointer(&s))
	h.Data = uintptr(unsafe.Pointer(victim))
	h.Len = 1 << 20
	h.Cap = 1 << 20
	for i := 16; i < 4096; i++ {
		s[i] = 88
	}
	if after[0] == 88 || s[0] == 88 {
		unsafeDone("a slice header written as integers reaches past its object")
	}
	fmt.Println("OK")
}
