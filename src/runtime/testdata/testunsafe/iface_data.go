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

type eface struct{ t, d unsafe.Pointer }
type big struct{ a [4096]int64 }

func main() {
	var i any = new(big)
	e := (*eface)(launder(unsafe.Pointer(&i)))
	e.d = unsafe.Pointer(new(int32))
	vn = 1000
	i.(*big).a[vn] = 1
	unsafeDone("the data pointer of an interface was changed")
}
