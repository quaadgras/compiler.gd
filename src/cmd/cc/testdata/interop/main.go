// Go and C sharing memory with typed capabilities: an ordinary cgo
// package, which go build compiles with cmd/cc when cgo is off (see
// TestInterop).
package main

// #include <stdlib.h>
// #include "interop.h"
import "C"

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
	list := C.make_list(5)
	runtime.GC()
	fmt.Print("C's list in Go:")
	for n := list; n != nil; n = n.next {
		fmt.Print(" ", n.v)
	}
	fmt.Println()

	// Go's list, read by C.
	golist := &C.struct_node{v: 10, next: &C.struct_node{v: 20, next: &C.struct_node{v: 30}}}
	fmt.Println("Go's list summed by C:", C.sum_list(golist))

	// A long list built by C and kept only by C's memory, across collections.
	long := C.make_list(100000)
	for range 3 {
		_ = make([]byte, 1<<20)
		runtime.GC()
	}
	sum := int64(0)
	for n := long; n != nil; n = n.next {
		sum += int64(n.v)
	}
	fmt.Println("100000 nodes, after 3 collections: Go", sum, "C", C.sum_list(long))

	// Go's stack, passed to C (escape analysis keeps it there: see
	// TestStackStaysOnStack).
	fmt.Println("Go's stack pair summed by C:", sumPair())
	fmt.Println("C's locals:", C.locals())
	fmt.Println("allocations per call: sumPair", testing.AllocsPerRun(100, func() { sumPair() }),
		"locals", testing.AllocsPerRun(100, func() { C.locals() }))

	arr := []int32{10, 20, 30}
	try("C reads arr[2] of Go's slice", func() { fmt.Print(C.read_at(&arr[0], 2), " ") })
	try("C reads arr[3] of Go's slice", func() { C.read_at(&arr[0], 3) })

	// In C's object, C keeps the capability; in Go's, the word's type is
	// all that is left.
	h := C.new_holder()
	C.store_past(h, &arr[0], 3)
	runtime.GC()
	fmt.Printf("word C stored past the end: %#x\n", uintptr(unsafe.Pointer(h.random_int)))
	// A fault at a non-canonical address is fatal in Go unless the
	// goroutine asks for a panic.
	tryDeref("Go dereferences it", func() {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
		sink = *h.random_int
	})
	try("C brings it back in bounds (arr[2])", func() { fmt.Print(C.load_back(h, 4), " ") })
	try("C dereferences it out of bounds", func() { C.load_back(h, 0) })
	gh := &C.struct_holder{}
	C.store_past(gh, &arr[0], 3)
	try("...stored in Go's heap object instead", func() { fmt.Print(C.load_back(gh, 4), " ") })
	try("...stored in Go's stack, which then moves", func() { fmt.Print(stackRoundTrip(arr), " ") })
	ga := &C.struct_holder{}
	C.keep_array(ga, &arr[0])
	try("C keeps a slice's array in Go's int* field", func() { fmt.Print(C.at(ga, 2), " ") })
	try("...and indexes past it", func() { C.at(ga, 3) })
	try("C one-based indexing", func() { fmt.Print(C.one_based_sum(&arr[0], 3), " ") })
	try("C writes an integer over a pointer word", func() { C.pun(h) })
	try("C dereferences an integer", func() { C.forge(C.long(uintptr(unsafe.Pointer(&arr[0])))) })
	try("C keeps a pointer in untyped bytes", func() { C.hide(golist) })

	// Unions: Go sees their bytes, as with cgo; C keeps the capabilities
	// of the pointers it stores in them.
	v := C.make_variant(4)
	runtime.GC()
	fmt.Println("variant's list, after a collection:", C.variant_sum(v), "type", v._type, "bytes", len(v.v.b))
	C.variant_set_double(v, 2.5)
	try("C reads the double as a pointer", func() { C.variant_misread(v) })
	C.make_pool()
	runtime.GC()
	fmt.Println("objects in a pool, after a collection:", C.pool_sum())
	tryDeref("Go sees the pool's objects only as bytes", func() {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
		sink = C.in_pool().v
	})
	try("C puns a pointer word of a stack local", func() { C.stack_pun() })

	n := C.make_list(1)
	x := new(int32)
	*x = 42
	C.confuse(n, x)
	fmt.Printf("int* stored in a struct node* field: %#x\n", uintptr(unsafe.Pointer(n.next)))
	tryDeref("Go follows it", func() {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
		sink = n.next.v
	})
	try("C uses it as the int it is", func() { fmt.Print(C.unconfuse(n), " ") })

	g := &goThing{n: 1, data: []int32{1, 2}}
	try("C writes a plain field of a Go value", func() { C.poke(unsafe.Pointer(g), 0, 7) })
	// poke indexes by longs, which are words.
	word := unsafe.Sizeof(C.long(0))
	try("C writes the length of a Go slice", func() { C.poke(unsafe.Pointer(g), C.int(unsafe.Offsetof(g.data)/word+1), 1000) })
	try("C writes past a Go value", func() { C.poke(unsafe.Pointer(g), C.int(unsafe.Sizeof(*g)/word), 1) })
	fmt.Println("Go's value after:", g.n, len(g.data))

	// free and realloc: only what malloc returned. free lets a pointer
	// with no capability be; realloc cannot copy its object.
	try("C frees what malloc and the like returned", func() { C.free_mallocd() })
	try("C frees a pointer into its object", func() { C.free_interior() })
	try("C frees a local", func() { C.free_local() })
	try("C frees a pointer that went through a long", func() { C.free_lost() })
	try("C reallocs a pointer gone through a long", func() { C.realloc_lost() })
	try("C reallocs a pointer into its object", func() { C.realloc_interior() })
	try("C frees Go's slice", func() { C.free_go(&arr[0]) })
	try("Go frees C's string", func() { C.free(unsafe.Pointer(C.CString("x"))) })
	try("Go frees its own slice", func() { C.free(unsafe.Pointer(&arr[0])) })
}

//go:noinline
func sumPair() C.long {
	b := C.struct_node{v: 2}
	a := C.struct_node{v: 1, next: &b}
	return C.sum_pair(&a)
}

// stackRoundTrip has C store an out-of-bounds pointer into a holder on
// this goroutine's stack, moves the stack, and has C bring it back.
//
//go:noinline
func stackRoundTrip(arr []int32) int32 {
	var h C.struct_holder
	C.store_past(&h, &arr[0], 3)
	before := uintptr(unsafe.Pointer(&h))
	grow(2000)
	if stacksMove && uintptr(unsafe.Pointer(&h)) == before {
		panic("the stack did not move")
	}
	return C.load_back(&h, 4)
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

// tryDeref is try, for Go's use of a word that C tagged.
func tryDeref(what string, f func()) {
	if !derefPanics {
		fmt.Printf("%-44s not tried: a trap would stop the program\n", what)
		return
	}
	try(what, f)
}

// sink makes Go really load what it dereferences.
var sink C.int
