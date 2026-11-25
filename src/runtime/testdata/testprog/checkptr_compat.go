// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"slices"
	"unsafe"
)

// Compatible types (doc/gd/unsafe.md). A case named CheckPtrIncompat...
// must die at the conversion, and one named CheckPtrCompat... must not.
// CheckPtrCompatList prints their names.

var compatCases = map[string]func(){}

func compatCase(name string, f func()) {
	compatCases[name] = f
	register(name, f)
}

type (
	// More than 512 bytes: an object of these records its type.
	compatNode struct {
		next *compatNode
		val  int
		u    unsafe.Pointer
		f    func(int) int
		b    []int32
		r    io.Reader
		s    string
		pad  [64]uintptr
	}
	// The same, under other names.
	compatNode2 struct {
		link *compatNode2
		n    uint
		p    unsafe.Pointer
		g    compatFunc
		c    []uint32
		rd   io.Reader
		str  string
		_    [64]int
	}
	compatFunc func(int) int
	// A pointer to something else.
	compatWrong struct {
		next *[1000]int
		val  int
	}
	compatInts struct {
		a, b int
	}
	// 512 bytes or less: only where the pointers are is known.
	compatSmall struct {
		p *int
		n int
	}
	compatSmall2 struct {
		q *uint
		m uint
	}
)

// compatHide returns p as a pointer that the compiler knows nothing of.
//
//go:noinline
func compatHide[T any](p *T) unsafe.Pointer { return unsafe.Pointer(p) }

func compatAs[T any](p unsafe.Pointer) { sink2 = (*T)(p) }

func init() {
	register("CheckPtrCompatList", func() {
		names := make([]string, 0, len(compatCases))
		for name := range compatCases {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			fmt.Println(name)
		}
	})
	const C, I = "CheckPtrCompat", "CheckPtrIncompat"
	node := func() *compatNode {
		n := new(compatNode)
		checkptrSink = n
		return n
	}
	small := func() *compatSmall {
		s := &compatSmall{p: new(int)}
		checkptrSink = s
		return s
	}

	// An object that records its type.
	compatCase(C+"Same", func() { compatAs[compatNode](compatHide(node())) })
	compatCase(C+"Shape", func() { compatAs[compatNode2](compatHide(node())) })
	compatCase(C+"Bytes", func() { compatAs[[32]byte](compatHide(node())) })
	compatCase(C+"Field", func() { compatAs[int](compatHide(&node().val)) })
	compatCase(C+"FieldPointer", func() { compatAs[*compatNode2](compatHide(&node().next)) })
	compatCase(C+"UnsafeOverPointer", func() { compatAs[unsafe.Pointer](compatHide(node())) })
	compatCase(C+"NoSizeOverPointer", func() { compatAs[*struct{}](compatHide(node())) })
	compatCase(C+"UnsafeOverFunc", func() { compatAs[unsafe.Pointer](compatHide(&node().f)) })
	compatCase(C+"FuncNamed", func() { compatAs[compatFunc](compatHide(&node().f)) })
	compatCase(C+"SliceSameSize", func() { compatAs[[]float32](compatHide(&node().b)) })
	compatCase(C+"Integers", func() { compatAs[[8]float64](compatHide(&node().pad[3])) })
	compatCase(C+"Array", func() {
		a := new([40]compatSmall)
		checkptrSink = a
		compatAs[[3]compatSmall2](compatHide(&a[7]))
	})

	compatCase(I+"Pointee", func() { compatAs[compatWrong](compatHide(node())) })
	compatCase(I+"PointeeSize", func() { compatAs[*[2]compatNode](compatHide(node())) })
	compatCase(I+"IntegersOverPointer", func() { compatAs[compatInts](compatHide(node())) })
	compatCase(I+"WordOverPointer", func() { compatAs[uintptr](compatHide(node())) })
	compatCase(I+"PointerOverInteger", func() { compatAs[*int](compatHide(&node().val)) })
	compatCase(I+"UnsafeOverInteger", func() { compatAs[unsafe.Pointer](compatHide(&node().val)) })
	compatCase(I+"PointerOverUnsafe", func() { compatAs[*int](compatHide(&node().u)) })
	compatCase(I+"Func", func() { compatAs[func()](compatHide(&node().f)) })
	compatCase(I+"PointerOverFunc", func() { compatAs[*int](compatHide(&node().f)) })
	compatCase(I+"SliceElemSize", func() { compatAs[[]int64](compatHide(&node().b)) })
	compatCase(I+"SliceElem", func() { compatAs[[]*int32](compatHide(&node().b)) })
	compatCase(I+"Iface", func() { compatAs[any](compatHide(&node().r)) })
	compatCase(I+"Shifted", func() {
		a := new([40]compatSmall)
		checkptrSink = a
		compatAs[compatSmall](compatHide(&a[7].n))
	})

	// An object that has only a bitmap, or no pointers.
	compatCase(C+"Small", func() { compatAs[compatSmall2](compatHide(small())) })
	compatCase(C+"SmallBytes", func() { compatAs[[16]byte](compatHide(small())) })
	compatCase(C+"SmallUnsafe", func() { compatAs[unsafe.Pointer](compatHide(small())) })
	compatCase(C+"NoPointers", func() {
		b := make([]byte, 64)
		checkptrSink = b
		compatAs[[4]uint64](compatHide(&b[8]))
	})
	compatCase(I+"SmallIntegersOverPointer", func() { compatAs[[2]int](compatHide(small())) })
	compatCase(I+"SmallPointerOverInteger", func() { compatAs[[2]*int](compatHide(small())) })
	compatCase(I+"SmallShifted", func() { compatAs[*int](compatHide(&small().n)) })
	compatCase(I+"NoPointers", func() {
		b := make([]byte, 64)
		checkptrSink = b
		compatAs[*int](compatHide(&b[8]))
	})

	// What the compiler knows: a variable, which the runtime has no
	// type for.
	compatCase(C+"KnownShape", func() {
		var s compatSmall
		sink2 = (*compatSmall2)(unsafe.Pointer(&s)).m
	})
	compatCase(C+"KnownBytes", func() {
		var s compatSmall
		sink2 = (*[16]byte)(unsafe.Pointer(&s))[3]
	})
	compatCase(C+"KnownUnsafe", func() {
		var p *int
		sink2 = *(*unsafe.Pointer)(unsafe.Pointer(&p))
	})
	compatCase(C+"KnownFromBytes", func() {
		// Bytes say nothing of what they are over.
		var s compatSmall
		b := (*[16]byte)(unsafe.Pointer(&s))
		sink2 = (*compatSmall)(unsafe.Pointer(b)).n
	})
	compatCase(I+"KnownIntegerOverPointer", func() {
		var p *int
		q := (*uintptr)(unsafe.Pointer(&p))
		sink2 = *q
	})
	compatCase(I+"KnownStoreIntegerOverPointer", func() {
		var p *int
		*(*uintptr)(unsafe.Pointer(&p)) = 8
		sink2 = p
	})
	compatCase(I+"KnownStoreField", func() {
		var s compatSmall
		(*compatInts)(unsafe.Pointer(&s)).b = 8
		sink2 = s.n
	})

	// What is only loaded through may have integers over pointers.
	compatCase(C+"KnownLoad", func() {
		var p *int
		sink2 = *(*uintptr)(unsafe.Pointer(&p))
	})
	compatCase(C+"KnownLoadField", func() {
		var s compatSmall
		sink2 = (*compatInts)(unsafe.Pointer(&s)).a
	})
	compatCase(C+"KnownLoadIndex", func() {
		var s compatSmall
		sink2 = (*[2]uintptr)(unsafe.Pointer(&s))[0]
	})
	compatCase(C+"Load", func() { sink2 = *(*uintptr)(compatHide(node())) })
	compatCase(C+"LoadField", func() { sink2 = (*compatInts)(compatHide(node())).a })
	compatCase(C+"LoadSmall", func() { sink2 = *(*[2]int)(compatHide(small())) })
	compatCase(I+"Store", func() { *(*uintptr)(compatHide(node())) = 8 })
	compatCase(I+"StoreField", func() { (*compatInts)(compatHide(node())).b = 8 })
	compatCase(I+"StoreIndex", func() { (*[2]uintptr)(compatHide(node()))[1] = 8 })
	compatCase(I+"StoreSmall", func() { *(*[2]int)(compatHide(small())) = [2]int{} })
	compatCase(I+"LoadAddr", func() { sink2 = &(*compatInts)(compatHide(node())).a })
	compatCase(I+"LoadSlice", func() { sink2 = (*[2]uintptr)(compatHide(node()))[:] })
	compatCase(I+"LoadPointerOverInteger", func() { sink2 = *(**int)(compatHide(&node().val)) })
	compatCase(I+"LoadPointee", func() { sink2 = (*compatWrong)(compatHide(node())).next })
	compatCase(C+"Map", func() {
		m := new(map[int]int)
		checkptrSink = m
		compatAs[unsafe.Pointer](compatHide(m))
	})
	compatCase(I+"KnownPointerOverInteger", func() {
		var n uintptr
		sink2 = *(**int)(unsafe.Pointer(&n))
	})
	compatCase(I+"KnownPointee", func() {
		var p *compatSmall
		sink2 = *(**compatWrong)(unsafe.Pointer(&p))
	})
	compatCase(I+"KnownField", func() {
		var s compatSmall
		sink2 = *(**int)(unsafe.Add(unsafe.Pointer(&s), unsafe.Sizeof(uintptr(0))))
	})
	compatCase(I+"KnownStringHeader", func() {
		h := struct {
			data uintptr
			n    int
		}{}
		sink2 = *(*string)(unsafe.Pointer(&h))
	})
}
