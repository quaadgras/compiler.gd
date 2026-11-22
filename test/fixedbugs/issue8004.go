// run

// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"runtime"
	"unsafe"
)

func main() {
	test1()
	test2()
}

// newSlice returns a slice in memory long enough for a reflect.SliceHeader,
// which is longer than a slice with 8-byte words (doc/gd/slice16.md).
func newSlice() *[]int {
	return &new([2][]int)[0]
}

func test1() {
	var all []interface{}
	for i := 0; i < 100; i++ {
		p := newSlice()
		*p = append(*p, 1, 2, 3, 4)
		h := (*reflect.SliceHeader)(unsafe.Pointer(p))
		all = append(all, h, p)
	}
	runtime.GC()
	for i := 0; i < 100; i++ {
		p := *all[2*i+1].(*[]int)
		if p[0] != 1 || p[1] != 2 || p[2] != 3 || p[3] != 4 {
			println("BUG test1: bad slice at index", i, p[0], p[1], p[2], p[3])
			return
		}
	}
}

type T struct {
	H *reflect.SliceHeader
	P *[]int
}

func test2() {
	var all []T
	for i := 0; i < 100; i++ {
		p := newSlice()
		*p = append(*p, 1, 2, 3, 4)
		h := (*reflect.SliceHeader)(unsafe.Pointer(p))
		all = append(all, T{H: h}, T{P: p})
	}
	runtime.GC()
	for i := 0; i < 100; i++ {
		p := *all[2*i+1].P
		if p[0] != 1 || p[1] != 2 || p[2] != 3 || p[3] != 4 {
			println("BUG test2: bad slice at index", i, p[0], p[1], p[2], p[3])
			return
		}
	}
}
