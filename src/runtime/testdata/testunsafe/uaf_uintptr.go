package main

import (
	"fmt"
	"os"
	"runtime"
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
	x := new([64]byte)
	u := uintptr(unsafe.Pointer(x))
	x = nil
	for i := 0; i < 4; i++ {
		runtime.GC()
	}
	ys := make([]*[64]byte, 100000)
	for i := range ys {
		ys[i] = new([64]byte)
	}
	p := (*[64]byte)(unsafe.Pointer(u))
	p[0] = 88
	for _, y := range ys {
		if y[0] == 88 {
			unsafeDone("an integer reached an object that was allocated after its own was freed")
		}
	}
	ok()
}
