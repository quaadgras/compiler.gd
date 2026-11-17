// Go and C sharing memory with derived capabilities.
package main

//go:generate go run ../.. -o interop_c.go ../../testdata/interop.c

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"unsafe"
)

func try(what string, f func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("%-44s trapped: %v\n", what, r)
		}
	}()
	f()
	fmt.Printf("%-44s ok\n", what)
}

func main() {
	// C's list, read by Go as Go structs: no checks, no conversions.
	list := C_make_list(5)
	runtime.GC()
	fmt.Print("C's list in Go:")
	for n := list; n != nil; n = n.next {
		fmt.Print(" ", n.v)
	}
	fmt.Println()

	// Go's list, read by C.
	golist := &struct_node{v: 10, next: &struct_node{v: 20, next: &struct_node{v: 30}}}
	fmt.Println("Go's list summed by C:", C_sum_list(golist))

	// A long list built by C and kept only by C's memory, across collections.
	long := C_make_list(100000)
	for range 3 {
		_ = make([]byte, 1<<20)
		runtime.GC()
	}
	sum := int64(0)
	for n := long; n != nil; n = n.next {
		sum += int64(n.v)
	}
	fmt.Println("100000 nodes, after 3 collections: Go", sum, "C", C_sum_list(long))

	arr := []int32{10, 20, 30}
	try("C reads arr[2] of Go's slice", func() { fmt.Print(C_read_at(&arr[0], 2), " ") })
	try("C reads arr[3] (inside the 16-byte size class)", func() { fmt.Print(C_read_at(&arr[0], 3), " ") })
	try("C reads arr[4] of Go's slice", func() { C_read_at(&arr[0], 4) })

	h := &struct_holder{}
	C_store_past(h, &arr[0], 3)
	runtime.GC()
	fmt.Printf("word C stored past the end: %#x\n", uintptr(unsafe.Pointer(h.random_int)))
	// A fault at a non-canonical address is fatal in Go unless the
	// goroutine asks for a panic.
	try("Go dereferences it", func() {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
		_ = *h.random_int
	})
	try("C brings it back in bounds (arr[2])", func() { fmt.Print(C_load_back(h, 4), " ") })
	try("C dereferences it out of bounds", func() { C_load_back(h, 0) })
	try("C one-based indexing", func() { fmt.Print(C_one_based_sum(&arr[0], 3), " ") })
	try("C writes an integer over a pointer word", func() { C_pun(h) })
	try("C dereferences an integer", func() { C_forge(int64(uintptr(unsafe.Pointer(&arr[0])))) })
	try("C hides a pointer in bytes", func() { C_hide(golist) })
}
