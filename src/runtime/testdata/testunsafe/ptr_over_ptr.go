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

type big struct{ a [4096]int64 }

func main() {
	ip := new(*int32)
	*ip = new(int32)
	bp := (**big)(launder(unsafe.Pointer(ip)))
	vn = 1000
	(*bp).a[vn] = 1
	unsafeDone("a *int32 was used as a pointer to a large struct")
}
