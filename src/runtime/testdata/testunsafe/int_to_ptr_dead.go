package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// An object whose address the program kept as an integer dies, and the
// integer is made a pointer again after the collector found the object
// dead and before its memory is swept: the pointer would be to memory
// that is free, and used again.

type obj struct{ a, b, c, d, e, f uint64 }

const magic = 0x5afe5afe5afe5afe

var keepU uintptr
var esc *obj
var live [][]byte
var others []*obj

//go:noinline
func hide(u uintptr) { keepU = u }

//go:noinline
func make1() {
	o := new(obj)
	o.a = magic
	esc = o
	hide(uintptr(unsafe.Pointer(o)))
	esc = nil
}

//go:noinline
func back() *obj { return (*obj)(unsafe.Pointer(keepU)) }

func unsafeDone(what string) { fmt.Println("UNSAFE:", what); os.Exit(0) }

var junk []byte

func main() {
	runtime.GOMAXPROCS(1)
	// A heap of many spans, which takes the sweeper a while.
	for i := 0; i < 4000; i++ {
		live = append(live, make([]byte, 8192))
	}
	var ms runtime.MemStats
	for iter := 0; iter < 200; iter++ {
		runtime.GC()
		make1()
		runtime.ReadMemStats(&ms)
		n := ms.NumGC
		for {
			for i := 0; i < 20; i++ {
				junk = make([]byte, 4096)
			}
			runtime.ReadMemStats(&ms)
			if ms.NumGC != n {
				break
			}
		}
		// The collector is done with a cycle in which nothing pointed
		// to the object.
		p := back()
		others = others[:0]
		for i := 0; i < 5000; i++ {
			o := new(obj)
			o.a = 1
			others = append(others, o)
		}
		if p.a != magic {
			unsafeDone("a pointer made of an integer is to an object that was freed and is another's now")
		}
	}
	fmt.Println("OK")
}
