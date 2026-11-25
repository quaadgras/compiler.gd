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
	u := uintptr(unsafe.Pointer(&victim))
	u ^= uintptr(vn)
	p := (*byte)(unsafe.Pointer(u))
	*p = 88
	if victim[0] == 88 {
		unsafeDone("an integer that was kept was used as a pointer")
	}
	ok()
}
