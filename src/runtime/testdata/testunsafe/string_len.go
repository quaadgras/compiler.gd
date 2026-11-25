package main

import (
	"fmt"
	"os"
	"reflect"
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
	s := string(make([]byte, 8))
	h := (*reflect.StringHeader)(launder(unsafe.Pointer(&s)))
	h.Len = 1 << 20
	n := 0
	for i := 0; i < len(s); i++ {
		n += int(s[i])
	}
	fmt.Println(n)
	unsafeDone("read a megabyte of a string of 8 whose length was changed")
}
