// Go and C sharing memory with typed capabilities: the Go side of
// interop.c (see TestInterop).
package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"testing"
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

// A Go type with invariants C must not break.
type goThing struct {
	n    int64
	data []int32
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

	// Go's stack, passed to C (escape analysis keeps it there: see
	// TestStackStaysOnStack).
	fmt.Println("Go's stack pair summed by C:", sumPair())
	fmt.Println("C's locals:", c_locals())
	fmt.Println("allocations per call: sumPair", testing.AllocsPerRun(100, func() { sumPair() }),
		"locals", testing.AllocsPerRun(100, func() { c_locals() }))

	arr := []int32{10, 20, 30}
	try("C reads arr[2] of Go's slice", func() { fmt.Print(C_read_at(arr, 2), " ") })
	try("C reads arr[3] of Go's slice", func() { C_read_at(arr, 3) })

	// In C's object, C keeps the capability; in Go's, the word's type is
	// all that is left.
	h := C_new_holder()
	C_store_past(h, arr, 3)
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
	gh := &struct_holder{}
	C_store_past(gh, arr, 3)
	try("...stored in Go's heap object instead", func() { fmt.Print(C_load_back(gh, 4), " ") })
	try("...stored in Go's stack, which then moves", func() { fmt.Print(stackRoundTrip(arr), " ") })
	ga := &struct_holder{}
	C_keep_array(ga, arr)
	try("C keeps a slice's array in Go's int* field", func() { fmt.Print(C_at(ga, 2), " ") })
	try("...and indexes past it", func() { C_at(ga, 3) })
	try("C one-based indexing", func() { fmt.Print(C_one_based_sum(arr, 3), " ") })
	try("C writes an integer over a pointer word", func() { C_pun(h) })
	try("C dereferences an integer", func() { C_forge(int64(uintptr(unsafe.Pointer(&arr[0])))) })
	try("C keeps a pointer in untyped bytes", func() { C_hide(golist) })

	// Unions: Go sees their bytes, as with cgo; C keeps the capabilities
	// of the pointers it stores in them.
	v := C_make_variant(4)
	runtime.GC()
	fmt.Println("variant's list, after a collection:", C_variant_sum(v), "type", v.type_, "bytes", len(v.v.b))
	C_variant_set_double(v, 2.5)
	try("C reads the double as a pointer", func() { C_variant_misread(v) })
	pool := c_in_pool()
	runtime.GC()
	fmt.Println("objects in a pool, after a collection:", c_sum_list(pool))
	try("Go sees the pool's objects only as bytes", func() {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
		_ = C_in_pool().v
	})
	try("C puns a pointer word of a stack local", func() { C_stack_pun() })

	n := C_make_list(1)
	x := new(int32)
	*x = 42
	C_confuse(n, x)
	fmt.Printf("int* stored in a struct node* field: %#x\n", uintptr(unsafe.Pointer(n.next)))
	try("Go follows it", func() {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
		_ = n.next.v
	})
	try("C uses it as the int it is", func() { fmt.Print(C_unconfuse(n), " ") })

	g := &goThing{n: 1, data: []int32{1, 2}}
	try("C writes a plain field of a Go value", func() { c_poke(C_Ptr(g), 0, 7) })
	try("C writes the length of a Go slice", func() { c_poke(C_Ptr(g), 2, 1000) })
	try("C writes past a Go value", func() { c_poke(C_Ptr(g), 4, 1) })
	fmt.Println("Go's value after:", g.n, len(g.data))
}

//go:noinline
func sumPair() int64 {
	b := struct_node{v: 2}
	a := struct_node{v: 1, next: &b}
	return C_sum_pair(&a)
}

// stackRoundTrip has C store an out-of-bounds pointer into a holder on
// this goroutine's stack, moves the stack, and has C bring it back.
//
//go:noinline
func stackRoundTrip(arr []int32) int32 {
	var h struct_holder
	C_store_past(&h, arr, 3)
	before := uintptr(unsafe.Pointer(&h))
	grow(2000)
	if uintptr(unsafe.Pointer(&h)) == before {
		panic("the stack did not move")
	}
	return C_load_back(&h, 4)
}

//go:noinline
func grow(n int) int {
	var buf [512]byte
	buf[n%512] = byte(n)
	if n == 0 {
		return int(buf[0])
	}
	return grow(n-1) + int(buf[n%512])
}
