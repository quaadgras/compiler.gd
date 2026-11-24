// Go and C sharing memory: an ordinary cgo package, which go build
// compiles with cmd/cc when cgo is off (see TestInterop). C's objects are
// Go variables and its pointers are Go's, so each side uses what the
// other allocated as it is.
package main

// #include <stdlib.h>
// #include "interop.h"
import "C"

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"
)

// A Go type that C writes a word of.
type goThing struct {
	n    int64
	data []int32
}

func collect() {
	for range 3 {
		_ = make([]byte, 1<<20)
		runtime.GC()
	}
}

func main() {
	// C's list, read by Go as Go structs.
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
	collect()
	sum := int64(0)
	for n := long; n != nil; n = n.next {
		sum += int64(n.v)
	}
	fmt.Println("100000 nodes, after 3 collections: Go", sum, "C", C.sum_list(long))

	// Go's stack, passed to C, and C's own locals: nothing is allocated.
	fmt.Println("Go's stack pair summed by C:", sumPair())
	fmt.Println("C's locals:", C.locals())
	fmt.Println("allocations per call: sumPair", testing.AllocsPerRun(100, func() { sumPair() }),
		"locals", testing.AllocsPerRun(100, func() { C.locals() }))

	// Go's slice, indexed and kept by C.
	arr := []int32{10, 20, 30}
	fmt.Println("C reads arr[2] of Go's slice:", C.read_at(&arr[0], 2))
	h := C.new_holder()
	C.keep_array(h, &arr[0])
	arr = nil
	collect()
	fmt.Println("C keeps a slice's array in its int* field:", C.at(h, 2), *h.ints)

	// A pointer kept only in memory that C was given no type for.
	C.hide(C.make_list(1000))
	collect()
	fmt.Println("a list kept in untyped memory, after 3 collections:", C.hidden_sum())

	// void * is unsafe.Pointer, as with cgo.
	fmt.Println("through void *:", (*C.node_t)(C.as_void(golist)).v)
	g := &goThing{n: 1, data: []int32{1, 2}}
	C.poke(unsafe.Pointer(g), 0, 7)
	fmt.Println("C writes a field of a Go value:", g.n, len(g.data))

	// C's variables, enumerations and strings.
	C.counter = 41
	C.count()
	was := C.count()
	fmt.Println("C's variable:", was, C.counter)
	var c C.enum_color = C.GREEN
	fmt.Println("enum:", c, C.next_color(c), C.next_color(C.BLUE) == C.RED)
	cs := C.CString("hello")
	fmt.Println("string:", C.GoString(C.shout(cs)))
	C.free(unsafe.Pointer(cs))

	// The pointer one past the end of an array, of each size up to 64.
	total := 0
	for n := 1; n <= 64; n++ {
		total += int(C.backwards_sum(C.int(n)))
	}
	fmt.Println("arrays summed from one past their end:", total)

	// Unions: Go sees their words, C the pointers it stores in them.
	v := C.make_variant(4)
	collect()
	fmt.Println("variant's list, after 3 collections:", C.variant_sum(v), "type", v._type, "size", unsafe.Sizeof(v.v))
	C.variant_set_long(v, 12)
	fmt.Println("variant's long:", C.variant_sum(v))

	// Objects that C placed in untyped memory.
	C.make_pool()
	collect()
	fmt.Println("objects in a pool, after 3 collections:", C.pool_sum(), C.pool_root.v, C.pool_root.next.v)

	fmt.Println("C frees what malloc and the like returned:", C.free_mallocd())

	// What is put on the heap as the program runs has an end for C as
	// well: a local that a call moves there because the function
	// called keeps its address, and a result that the caller gave no
	// buffer for (doc/gd/escape-bits.md).
	fmt.Println("a local moved to the heap at a call, summed from its end:", movedSum(keep))
	keptResult = fresh()
	collect()
	fmt.Println("a result that no buffer was given for, summed from its end:", C.sum_back((*C.int)(unsafe.Add(unsafe.Pointer(keptResult), 16)), 4))
}

var kept, keptResult *[4]C.int

var keep = func(p *[4]C.int) { kept = p }

//go:noinline
func movedSum(f func(*[4]C.int)) C.long {
	a := [4]C.int{1, 2, 3, 4}
	f(&a)
	collect()
	return C.sum_back((*C.int)(unsafe.Add(unsafe.Pointer(kept), 16)), 4)
}

//go:noinline
func fresh() *[4]C.int { return &[4]C.int{5, 6, 7, 8} }

//go:noinline
func sumPair() C.long {
	b := C.struct_node{v: 2}
	a := C.struct_node{v: 1, next: &b}
	return C.sum_pair(&a)
}
