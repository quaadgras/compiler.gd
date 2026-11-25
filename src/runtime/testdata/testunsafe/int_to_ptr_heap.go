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
	v := new([16]byte)
	sink = unsafe.Pointer(v)
	u := uintptr(unsafe.Pointer(new(int)))
	vn = int(uintptr(unsafe.Pointer(v)) - u)
	p := (*byte)(unsafe.Pointer(u + uintptr(vn)))
	*p = 88
	if v[0] == 88 {
		unsafeDone("an integer that is the address of another object was used as a pointer")
	}
	ok()
}
