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
	x := new([8]byte)
	vn = 1 << 20
	s := unsafe.String(&x[0], vn)
	n := 0
	for i := 0; i < len(s); i++ {
		n += int(s[i])
	}
	fmt.Println(n)
	unsafeDone("unsafe.String of a megabyte over 8 bytes")
}
