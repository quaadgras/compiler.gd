// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"slices"
	"unsafe"
)

// The variables of the stack and of the program's data
// (doc/gd/unsafe.md): each is an allocation, with its type. A case named
// CheckPtrVarBad... must die at the arithmetic or the conversion, and
// one named CheckPtrVarOK... must not. CheckPtrVarList prints their
// names.

var varCases = map[string]func(){}

func varCase(name string, f func()) {
	varCases[name] = f
	register(name, f)
}

// varHide returns p as a pointer that the compiler knows nothing of.
//
//go:noinline
func varHide(p unsafe.Pointer) unsafe.Pointer { return p }

// varAdd is unsafe.Add in a frame of its own, below the variable's.
//
//go:noinline
func varAdd(p unsafe.Pointer, n int) unsafe.Pointer { return unsafe.Add(p, n) }

// varKeep uses p, and keeps it nowhere: what it points to stays where it
// is.
//
//go:noinline
func varKeep(p unsafe.Pointer) {
	if p == nil {
		varUse++
	}
}

// varWalk writes n bytes from p on, one at a time.
//
//go:noinline
func varWalk(p *byte, n int) {
	for i := 0; i < n; i++ {
		*(*byte)(unsafe.Add(unsafe.Pointer(p), i)) = 'A'
	}
}

// varGrow uses n frames of stack, so that the stack is moved.
//
//go:noinline
func varGrow(n int) int {
	var pad [256]byte
	if n == 0 {
		return int(pad[0])
	}
	return varGrow(n-1) + int(pad[n%len(pad)])
}

type varPair struct {
	p *int
	n int
}

type varPair2 struct {
	q *int
	m uint
}

type varWrong struct {
	p *[1000]int
	n int
}

var (
	varUse int

	varG0   = [16]byte{0: 1}
	varG1   = [16]byte{0: 2}
	varG2   = [16]byte{0: 3}
	varB0   [16]byte
	varB1   [16]byte
	varB2   [16]byte
	varGInt uintptr
	varGPtr *int
	varGP   varPair
	varGStr = "a string that is too long to be kept inline in its value"
)

func init() {
	register("CheckPtrVarList", func() {
		for _, name := range slices.Sorted(func(yield func(string) bool) {
			for name := range varCases {
				if !yield(name) {
					return
				}
			}
		}) {
			fmt.Println(name)
		}
	})

	// Arithmetic in a variable of the frame, to its end, and back.
	varCase("CheckPtrVarOKLocal", func() {
		var before, a, after [16]byte
		p := varHide(unsafe.Pointer(&a))
		q := varAdd(p, 15)
		*(*byte)(q) = 1
		e := varAdd(p, 16)
		b := varAdd(e, -16)
		*(*byte)(b) = 2
		varUse += int(before[0]) + int(a[15]) + int(after[0])
	})
	varCase("CheckPtrVarBadLocalOver", func() {
		var before, a, after [16]byte
		p := varHide(unsafe.Pointer(&a))
		varKeep(varAdd(p, 17))
		varUse += int(before[0]) + int(a[15]) + int(after[0])
	})
	varCase("CheckPtrVarBadLocalUnder", func() {
		var before, a, after [16]byte
		p := varHide(unsafe.Pointer(&a))
		varKeep(varAdd(p, -1))
		varUse += int(before[0]) + int(a[15]) + int(after[0])
	})
	varCase("CheckPtrVarBadLocalSameFrame", func() {
		var before, a, after [16]byte
		p := varHide(unsafe.Pointer(&a))
		varKeep(unsafe.Add(p, 40))
		varUse += int(before[0]) + int(a[15]) + int(after[0])
	})
	// A callee that walks a variable of its caller.
	varCase("CheckPtrVarOKCallee", func() {
		var a [64]byte
		varWalk(&a[0], len(a))
		varUse += int(a[63])
	})
	varCase("CheckPtrVarBadCallee", func() {
		var a [64]byte
		varWalk(&a[0], len(a)+200)
		varUse += int(a[63])
	})
	// A field is not an allocation: its struct is.
	varCase("CheckPtrVarOKField", func() {
		var s struct {
			a [8]byte
			b [8]byte
		}
		p := varHide(unsafe.Pointer(&s.a))
		*(*byte)(varAdd(p, 12)) = 1
		varUse += int(s.b[4])
	})
	varCase("CheckPtrVarBadFieldNext", func() {
		var s struct{ a [8]byte }
		var t [8]byte
		p := varHide(unsafe.Pointer(&s.a))
		varKeep(varAdd(p, 9))
		varUse += int(s.a[0]) + int(t[0])
	})
	// The stack moves, and the variables with it.
	varCase("CheckPtrVarOKGrow", func() {
		done := make(chan bool)
		go func() {
			var a [32]byte
			p := varHide(unsafe.Pointer(&a))
			for i := 0; i < 8; i++ {
				varUse += varGrow(1 << i)
				*(*byte)(varAdd(p, 31)) = 1
				*(*uint64)(varAdd(p, 24)) = 2
			}
			varUse += int(a[31])
			done <- true
		}()
		<-done
	})
	varCase("CheckPtrVarBadGrow", func() {
		done := make(chan bool)
		go func() {
			var a [32]byte
			p := varHide(unsafe.Pointer(&a))
			varUse += varGrow(200)
			varKeep(varAdd(p, 33))
			varUse += int(a[31])
			done <- true
		}()
		<-done
	})
	// An argument and a result that are in the frame of the caller.
	varCase("CheckPtrVarOKArg", func() {
		var a [200]byte
		varUse += int(varArg(a, 199)[3])
	})
	varCase("CheckPtrVarBadArg", func() {
		var a [200]byte
		varUse += int(varArg(a, 201)[3])
	})
	// A variable that a closure has by reference.
	varCase("CheckPtrVarOKClosure", func() {
		var a [16]byte
		func() {
			p := varHide(unsafe.Pointer(&a))
			*(*byte)(varAdd(p, 15)) = 1
		}()
		varUse += int(a[15])
	})
	varCase("CheckPtrVarBadClosure", func() {
		var a [16]byte
		func() {
			p := varHide(unsafe.Pointer(&a))
			varKeep(varAdd(p, 24))
		}()
		varUse += int(a[15])
	})
	// A deferred call that runs while the goroutine panics.
	varCase("CheckPtrVarOKPanic", func() {
		var a [16]byte
		defer func() {
			recover()
			p := varHide(unsafe.Pointer(&a))
			*(*byte)(varAdd(p, 15)) = 1
			varUse += int(a[15])
		}()
		var np *int
		varUse += *np
	})
	varCase("CheckPtrVarBadPanic", func() {
		var a [16]byte
		defer func() {
			recover()
			p := varHide(unsafe.Pointer(&a))
			varKeep(varAdd(p, 17))
			varUse += int(a[15])
		}()
		var np *int
		varUse += *np
	})

	// The variables of the data, and of the bss.
	varCase("CheckPtrVarOKGlobal", func() {
		for _, g := range []*[16]byte{&varG1, &varB1} {
			p := varHide(unsafe.Pointer(g))
			*(*byte)(varAdd(p, 15)) = 1
			e := varAdd(p, 16)
			*(*byte)(varAdd(e, -16)) = 2
		}
		varUse += int(varG0[0]) + int(varG2[0]) + int(varB0[0]) + int(varB2[0])
	})
	varCase("CheckPtrVarBadGlobalOver", func() {
		p := varHide(unsafe.Pointer(&varG1))
		varKeep(varAdd(p, 17))
		varUse += int(varG0[0]) + int(varG2[0])
	})
	varCase("CheckPtrVarBadGlobalUnder", func() {
		p := varHide(unsafe.Pointer(&varG1))
		varKeep(varAdd(p, -1))
		varUse += int(varG0[0]) + int(varG2[0])
	})
	varCase("CheckPtrVarBadBssOver", func() {
		p := varHide(unsafe.Pointer(&varB1))
		varKeep(varAdd(p, 17))
		varUse += int(varB0[0]) + int(varB2[0])
	})
	varCase("CheckPtrVarBadBssUnder", func() {
		p := varHide(unsafe.Pointer(&varB1))
		varKeep(varAdd(p, -1))
		varUse += int(varB0[0]) + int(varB2[0])
	})
	varCase("CheckPtrVarBadGlobalWalk", func() {
		varWalk(&varG1[0], 4096)
	})
	// The bytes of a string.
	varCase("CheckPtrVarOKString", func() {
		p := varHide(unsafe.Pointer(unsafe.StringData(varGStr)))
		varUse += int(*(*byte)(varAdd(p, len(varGStr)-1)))
	})
	varCase("CheckPtrVarBadString", func() {
		p := varHide(unsafe.Pointer(unsafe.StringData(varGStr)))
		varKeep(varAdd(p, len(varGStr)+200))
	})

	// Conversions: the type is in the variable, and compatible with
	// the variable's.
	varCase("CheckPtrVarOKConv", func() {
		var x uint64
		var pr varPair
		var ptr *int
		p := varHide(unsafe.Pointer(&x))
		(*[2]uint32)(p)[1] = 1
		*(*float64)(p) = 1
		q := (*varPair2)(varHide(unsafe.Pointer(&pr)))
		q.m = 2
		u := (*unsafe.Pointer)(varHide(unsafe.Pointer(&ptr)))
		varUse += int(x) + pr.n + int(uintptr(*u))
		varUse += int(*(*uintptr)(varHide(unsafe.Pointer(&ptr))))

		g := (*varPair2)(varHide(unsafe.Pointer(&varGP)))
		g.m = 2
		*(*int)(varHide(unsafe.Pointer(&varGInt))) = 3
	})
	varCase("CheckPtrVarBadConvLong", func() {
		var before, x, after [2]uint64
		p := varHide(unsafe.Pointer(&x))
		varUse += int((*[4]uint64)(p)[0])
		varUse += int(before[0] + after[0])
	})
	varCase("CheckPtrVarBadConvLongGlobal", func() {
		p := varHide(unsafe.Pointer(&varGInt))
		varUse += int((*[4]uint64)(p)[0])
	})
	varCase("CheckPtrVarBadConvPtrInt", func() {
		var x uintptr
		pp := (**int)(varHide(unsafe.Pointer(&x)))
		varUse += int(uintptr(unsafe.Pointer(*pp)))
	})
	varCase("CheckPtrVarBadConvIntPtr", func() {
		x := new(int)
		up := (*uintptr)(varHide(unsafe.Pointer(&x)))
		*up += 8
		varUse += *x
	})
	varCase("CheckPtrVarBadConvPointee", func() {
		var pr varPair
		w := (*varWrong)(varHide(unsafe.Pointer(&pr)))
		varUse += w.n
	})
	varCase("CheckPtrVarBadConvPtrIntGlobal", func() {
		pp := (**int)(varHide(unsafe.Pointer(&varGInt)))
		varUse += int(uintptr(unsafe.Pointer(*pp)))
	})
	varCase("CheckPtrVarBadConvIntPtrGlobal", func() {
		up := (*uintptr)(varHide(unsafe.Pointer(&varGPtr)))
		*up += 8
	})
	varCase("CheckPtrVarBadConvPointeeGlobal", func() {
		w := (*varWrong)(varHide(unsafe.Pointer(&varGP)))
		varUse += w.n
	})
	varCase("CheckPtrVarBadConvString", func() {
		pp := (**int)(varHide(unsafe.Pointer(unsafe.StringData(varGStr))))
		varUse += int(uintptr(unsafe.Pointer(*pp)))
	})
	// Bytes that are kept may be stored through: they are over no
	// pointer.
	varCase("CheckPtrVarOKBytes", func() {
		var x [4]uint64
		b := unsafe.Slice((*byte)(varHide(unsafe.Pointer(&x))), 32)
		b[31] = 1
		g := unsafe.Slice((*byte)(varHide(unsafe.Pointer(&varGInt))), 8)
		g[7] = 1
		varUse += int(x[3])
	})
	varCase("CheckPtrVarBadBytes", func() {
		x := new(int)
		b := unsafe.Slice((*byte)(varHide(unsafe.Pointer(&x))), 8)
		b[0] += 8
		varUse += *x
	})
	varCase("CheckPtrVarBadBytesGlobal", func() {
		b := unsafe.Slice((*byte)(varHide(unsafe.Pointer(&varGPtr))), 8)
		b[0] += 8
	})
	varCase("CheckPtrVarBadSliceLong", func() {
		var before, x, after [4]uint64
		b := unsafe.Slice((*uint64)(varHide(unsafe.Pointer(&x))), 5)
		varUse += int(b[4] + before[0] + after[0])
	})
	varCase("CheckPtrVarBadSliceLongGlobal", func() {
		b := unsafe.Slice((*byte)(varHide(unsafe.Pointer(&varG1))), 17)
		varUse += int(b[16])
	})
}

// varArg is given a in the frame of its caller, and returns its result
// there.
//
//go:noinline
func varArg(a [200]byte, n int) (r [200]byte) {
	p := varHide(unsafe.Pointer(&a))
	*(*byte)(varAdd(p, n)) = 1
	q := varHide(unsafe.Pointer(&r))
	*(*byte)(varAdd(q, n)) = 1
	return
}
