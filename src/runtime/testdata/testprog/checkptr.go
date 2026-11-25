// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"runtime"
	"time"
	"unsafe"
)

func init() {
	register("CheckPtrBytesLonger", CheckPtrBytesLonger)
	register("CheckPtrBytesSliced", CheckPtrBytesSliced)
	register("CheckPtrBytesSlicedLonger", CheckPtrBytesSlicedLonger)
	register("CheckPtrAlignmentNoPtr", CheckPtrAlignmentNoPtr)
	register("CheckPtrAlignmentPtr", CheckPtrAlignmentPtr)
	register("CheckPtrAlignmentNilPtr", CheckPtrAlignmentNilPtr)
	register("CheckPtrArithmetic", CheckPtrArithmetic)
	register("CheckPtrArithmetic2", CheckPtrArithmetic2)
	register("CheckPtrArithmeticUnsafeAdd", CheckPtrArithmeticUnsafeAdd)
	register("CheckPtrSize", CheckPtrSize)
	register("CheckPtrSmall", CheckPtrSmall)
	register("CheckPtrSliceOK", CheckPtrSliceOK)
	register("CheckPtrSliceFail", CheckPtrSliceFail)
	register("CheckPtrStringOK", CheckPtrStringOK)
	register("CheckPtrStringFail", CheckPtrStringFail)
	register("CheckPtrAlignmentNested", CheckPtrAlignmentNested)
}

func CheckPtrAlignmentNoPtr() {
	var x [2]int64
	p := unsafe.Pointer(&x[0])
	sink2 = (*int64)(unsafe.Pointer(uintptr(p) + 1))
}

func CheckPtrAlignmentPtr() {
	var x [2]int64
	p := unsafe.Pointer(&x[0])
	sink2 = (**int64)(unsafe.Pointer(uintptr(p) + 1))
}

// CheckPtrAlignmentNilPtr tests that checkptrAlignment doesn't crash
// on nil pointers (#47430).
func CheckPtrAlignmentNilPtr() {
	var do func(int)
	do = func(n int) {
		// Inflate the stack so runtime.shrinkstack gets called during GC
		if n > 0 {
			do(n - 1)
		}

		var p unsafe.Pointer
		_ = (*int)(p)
	}

	go func() {
		for {
			runtime.GC()
		}
	}()

	go func() {
		for i := 0; ; i++ {
			do(i % 1024)
		}
	}()

	time.Sleep(time.Second)
}

func CheckPtrArithmetic() {
	var x int
	i := uintptr(unsafe.Pointer(&x))
	sink2 = (*int)(unsafe.Pointer(i))
}

func CheckPtrArithmetic2() {
	var x [2]int64
	p := unsafe.Pointer(&x[1])
	var one uintptr = 1
	sink2 = unsafe.Pointer(uintptr(p) & ^one)
}

func CheckPtrArithmeticUnsafeAdd() {
	data := make([]byte, 128)
	sink2 = (*byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(data)), len(data)))
}

func CheckPtrSize() {
	p := new(int64)
	sink2 = p
	sink2 = (*[100]int64)(unsafe.Pointer(p))
}

func CheckPtrSmall() {
	sink2 = unsafe.Pointer(uintptr(1))
}

func CheckPtrSliceOK() {
	p := new([4]int64)
	sink2 = unsafe.Slice(&p[1], 3)
}

func CheckPtrSliceFail() {
	p := new(int64)
	sink2 = p
	sink2 = unsafe.Slice(p, 100)
}

func CheckPtrStringOK() {
	p := new([4]byte)
	sink2 = unsafe.String(&p[1], 3)
}

func CheckPtrStringFail() {
	p := new(byte)
	sink2 = p
	sink2 = unsafe.String(p, 100)
}

// CheckPtrBytesLonger converts to a pointer to bytes that go on past
// the object: an index would go anywhere with it.
func CheckPtrBytesLonger() {
	p := new([8]byte)
	checkptrSink = p
	sink2 = (*[1 << 20]byte)(unsafe.Pointer(checkptrSink.(*[8]byte)))
}

// CheckPtrBytesSliced slices such a pointer at once to the bytes that
// are there, as code did before unsafe.Slice: that is those bytes.
func CheckPtrBytesSliced() {
	p := new([8]byte)
	checkptrSink = p
	n := 8
	sink2 = (*[1 << 20]byte)(unsafe.Pointer(checkptrSink.(*[8]byte)))[:n:n]
}

// CheckPtrBytesSlicedLonger slices it to more.
func CheckPtrBytesSlicedLonger() {
	p := new([8]byte)
	checkptrSink = p
	n := 4096
	sink2 = (*[1 << 20]byte)(unsafe.Pointer(checkptrSink.(*[8]byte)))[:n:n]
}

func CheckPtrAlignmentNested() {
	// (gd: an integer, not a pointer, which integers may not be over.)
	p := new(int64)
	checkptrSink = p
	n := 9
	_ = ((*[10]int8)(unsafe.Pointer((*[10]int64)(unsafe.Pointer(p)))))[:n:n]
}

func init() {
	register("CheckPtrPointersNoscan", CheckPtrPointersNoscan)
	register("CheckPtrPointersScalar", CheckPtrPointersScalar)
	register("CheckPtrPointersOK", CheckPtrPointersOK)
}

type checkptrPair struct {
	p *int
	n uintptr
}

type checkptrRiap struct {
	n uintptr
	p *int
}

var checkptrSink any

// A pointer type over memory that holds no pointers.
func CheckPtrPointersNoscan() {
	b := make([]byte, 64)
	checkptrSink = b
	// Bytes that are the address of something in the heap.
	*(*uintptr)(unsafe.Pointer(&b[8])) = uintptr(unsafe.Pointer(checkptrHeap()))
	pp := (**int)(unsafe.Pointer(&b[8]))
	sink2 = pp
}

// A pointer where the object has an integer.
func CheckPtrPointersScalar() {
	x := new([4]checkptrPair)
	checkptrSink = x
	// An integer that is the address of something in the heap.
	x[1].n = uintptr(unsafe.Pointer(checkptrHeap()))
	y := (*checkptrRiap)(unsafe.Pointer(&x[1]))
	sink2 = y
}

// The same layout under another name, and a part of an array.
func CheckPtrPointersOK() {
	x := new([4]checkptrPair)
	checkptrSink = x
	type pair2 struct {
		q *int
		m uintptr
	}
	sink2 = (*pair2)(unsafe.Pointer(&x[2]))
	sink2 = (*[2]pair2)(unsafe.Pointer(&x[2]))
	sink2 = (**int)(unsafe.Pointer(&x[3].p))
}

//go:noinline
func checkptrHeap() *int {
	p := new(int)
	checkptrSink = p
	return p
}

func init() {
	register("CheckPtrExposedOK", CheckPtrExposedOK)
	register("CheckPtrNotExposed", CheckPtrNotExposed)
}

var checkptrHandle uintptr

// An integer that was kept is a pointer again, to its object.
func CheckPtrExposedOK() {
	x := new([4]int64)
	checkptrSink = x
	checkptrHandle = uintptr(unsafe.Pointer(x))
	runtime.GC()
	p := (*[4]int64)(unsafe.Pointer(checkptrHandle))
	p[3] = 7
	q := (*int64)(unsafe.Pointer(checkptrHandle + 24))
	if *q != 7 || x[3] != 7 {
		panic("exposed object not reached")
	}
}

// But not to an object that never gave its address away.
func CheckPtrNotExposed() {
	objs := make([]*[4]int64, 64)
	for i := range objs {
		objs[i] = new([4]int64)
	}
	checkptrSink = objs
	checkptrHandle = uintptr(unsafe.Pointer(objs[20]))
	sink2 = (*int64)(unsafe.Pointer(checkptrHandle + 32))
}
