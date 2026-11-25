// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"runtime"
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
	// 512 bytes or less: the span has what it was allocated for.
	compatSmall struct {
		p *int
		n int
	}
	compatSmall2 struct {
		q *uint
		m uint
	}
	// The pointers where compatSmall has its own, to something else.
	compatSmallWrong struct {
		p *[1000]int
		n int
	}
	compatSmallString struct {
		p *byte
		n int
	}
)

// compatHide returns p as a pointer that the compiler knows nothing of.
//
//go:noinline
func compatHide[T any](p *T) unsafe.Pointer { return unsafe.Pointer(p) }

func compatAs[T any](p unsafe.Pointer) { sink2 = (*T)(p) }

// compatNew returns a new T that generic code allocated: as T's shape.
//
//go:noinline
func compatNew[T any]() *T {
	p := new(T)
	checkptrSink = p
	return p
}

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
	compatCase(C+"BytesOverIntegers", func() { compatAs[[40]byte](compatHide(&node().pad[3])) })
	compatCase(C+"BytesLoaded", func() { sink2 = (*[32]byte)(compatHide(node()))[3] })
	compatCase(C+"StringOfBytes", func() { sink2 = unsafe.String((*byte)(compatHide(&node().val)), 24) })
	compatCase(C+"SliceOfBytes", func() { sink2 = unsafe.Slice((*byte)(compatHide(&node().pad[3])), 40) })
	compatCase(C+"SliceOfElements", func() {
		a := new([40]compatSmall)
		checkptrSink = a
		sink2 = unsafe.Slice(&a[7], 3)
		sink2 = unsafe.Slice((*compatSmall2)(compatHide(&a[7])), 3)
	})
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

	// Bytes that are kept may be stored through: they are integers.
	compatCase(I+"Bytes", func() { compatAs[[32]byte](compatHide(node())) })
	compatCase(I+"ByteSlice", func() { sink2 = (*[32]byte)(compatHide(node()))[:] })
	compatCase(I+"ByteStore", func() { (*[32]byte)(compatHide(node()))[3] = 1 })
	compatCase(I+"SliceOfBytes", func() { sink2 = unsafe.Slice((*byte)(compatHide(&node().val)), 24) })
	compatCase(I+"SliceOfWords", func() {
		a := new([40]compatSmall)
		checkptrSink = a
		sink2 = unsafe.Slice((*uintptr)(compatHide(&a[7].n)), 3)
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

	// A small object, which has no header, and one with no pointers.
	compatCase(C+"SmallSame", func() { compatAs[compatSmall](compatHide(small())) })
	compatCase(C+"SmallPointer", func() {
		p := new(*compatSmall)
		checkptrSink = p
		compatAs[*compatSmall2](compatHide(p))
		compatAs[unsafe.Pointer](compatHide(p))
	})
	compatCase(C+"SmallSlice", func() {
		a := make([]compatSmall, 5)
		checkptrSink = a
		compatAs[[2]compatSmall2](compatHide(&a[3]))
		sink2 = unsafe.Slice((*compatSmall2)(compatHide(&a[1])), 4)
	})
	compatCase(C+"SmallReused", func() {
		// A slot is what it was last allocated for.
		for i := range 5000 {
			if i%2 == 0 {
				compatAs[compatSmall2](compatHide(small()))
			} else {
				w := new(compatSmallWrong)
				checkptrSink = w
				compatAs[compatSmallWrong](compatHide(w))
			}
			if i%1000 == 0 {
				runtime.GC()
			}
		}
	})
	compatCase(C+"SmallOfGeneric", func() {
		// What generic code allocates has a shape for its type, in
		// which a pointer is to anything.
		compatAs[*compatSmall](compatHide(compatNew[*compatSmall]()))
		compatAs[map[int32]int32](compatHide(compatNew[map[int32]int32]()))
		compatAs[chan *int](compatHide(compatNew[chan *int]()))
		compatAs[func(*int) int32](compatHide(compatNew[func(*int) int32]()))
		compatAs[compatSmall2](compatHide(compatNew[compatSmall]()))
		compatAs[io.Reader](compatHide(compatNew[io.Reader]()))
	})
	compatCase(I+"SmallOfGenericKind", func() { compatAs[uintptr](compatHide(compatNew[*compatSmall]())) })
	compatCase(I+"SmallOfGenericMap", func() { compatAs[map[int64]int32](compatHide(compatNew[map[int32]int32]())) })
	compatCase(I+"SmallPointee", func() { compatAs[compatSmallWrong](compatHide(small())) })
	compatCase(I+"SmallPointerPointee", func() {
		p := new(*compatSmall)
		checkptrSink = p
		compatAs[*compatSmallWrong](compatHide(p))
	})
	compatCase(I+"SmallPointerOverUnsafe", func() {
		p := new(unsafe.Pointer)
		checkptrSink = p
		compatAs[*int](compatHide(p))
	})
	compatCase(I+"SmallStringOverPlain", func() {
		p := new(compatSmallString)
		checkptrSink = p
		compatAs[string](compatHide(p))
	})
	compatCase(I+"SmallFunc", func() {
		f := new(func(int) int)
		checkptrSink = f
		compatAs[func()](compatHide(f))
	})
	compatCase(I+"SmallSlicePointee", func() {
		a := make([]compatSmall, 5)
		checkptrSink = a
		compatAs[[2]compatSmallWrong](compatHide(&a[3]))
	})
	compatCase(C+"Small", func() { compatAs[compatSmall2](compatHide(small())) })
	compatCase(C+"SmallBytesLoaded", func() { sink2 = *(*[16]byte)(compatHide(small())) })
	compatCase(C+"SmallBytesOverInteger", func() { compatAs[[8]byte](compatHide(&small().n)) })
	compatCase(I+"SmallBytes", func() { compatAs[[16]byte](compatHide(small())) })
	compatCase(I+"SmallSliceOfBytes", func() { sink2 = unsafe.Slice((*byte)(compatHide(small())), 8) })
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
		s := new(compatSmall)
		checkptrSink = s
		b := (*byte)(unsafe.Pointer(&s.n))
		sink2 = (*compatSmall)(unsafe.Add(unsafe.Pointer(b), -8)).p
	})
	compatCase(C+"KnownBytesOverInteger", func() {
		var s compatSmall
		sink2 = (*[8]byte)(unsafe.Pointer(&s.n))[:]
	})
	compatCase(I+"KnownBytesKept", func() {
		var s compatSmall
		sink2 = (*[16]byte)(unsafe.Pointer(&s))[:]
	})
	compatCase(I+"KnownByteStore", func() {
		var p *int
		(*[8]byte)(unsafe.Pointer(&p))[0] = 1
		sink2 = p
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
