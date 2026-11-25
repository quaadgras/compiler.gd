package main

import (
	"fmt"
	"os"
	"reflect"
	"unsafe"
)

// The Data of a reflect.SliceHeader is set to an integer, and a pointer
// is made of it.

var other = new([16]byte)
var victim = new([16]byte)

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }

func main() {
	var h reflect.SliceHeader
	hp := &h
	hp.Data = uintptr(unsafe.Pointer(other)) + 16
	p := (*byte)(unsafe.Pointer(hp.Data))
	*p = 88
	if victim[0] == 88 {
		unsafeDone("the Data of a slice header that is no slice was used as a pointer")
	}
	fmt.Println("OK")
}
