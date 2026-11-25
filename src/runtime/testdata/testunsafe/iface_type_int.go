package main

import (
	"fmt"
	"os"
	"unsafe"
)

// The word of an interface that holds its type is written as an
// integer, the type of another interface: the value, a pointer to one
// byte, would be a pointer to many.

var sink unsafe.Pointer

//go:noinline
func launder(p unsafe.Pointer) unsafe.Pointer { sink = p; return sink }

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }
func ok()                    { fmt.Println("OK"); os.Exit(0) }

func main() {
	var src any = new([1 << 16]byte)
	var dst any = new(byte)
	sp := (*[2]uintptr)(launder(unsafe.Pointer(&src)))
	dp := (*[2]uintptr)(launder(unsafe.Pointer(&dst)))
	dp[0] = sp[0]
	if p, isBig := dst.(*[1 << 16]byte); isBig {
		p[4096] = 88
		if p[4096] == 88 {
			unsafeDone("the type of an interface was changed as an integer, and its value used as the other type")
		}
	}
	ok()
}
