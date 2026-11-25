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
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	u := uintptr(unsafe.Pointer(&victim)) &^ 0xfff
	p := (*uintptr)(unsafe.Pointer(u - 4096*64))
	fmt.Println(*p)
	unsafeDone("read memory 64 pages before the data")
}
