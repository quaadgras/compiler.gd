// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
	"unsafe"
)

type stackVarPair struct {
	p *int
	n int
}

var (
	stackVarGlobal  [5]uint32
	stackVarGlobalP stackVarPair
	stackVarString  = "a string that is too long to be kept inline in its value"
)

// stackVarWant checks what the runtime says of the variable that p is
// in, from depth frames below the caller: by the lookup that the checks
// make, which goes by frame pointers where it can and remembers what it
// found, and by unwinding the stack.
//
//go:noinline
func stackVarWant(t *testing.T, name string, p unsafe.Pointer, depth int, off, size uintptr, typ string, ok bool) {
	if depth > 0 {
		stackVarWant(t, name, p, depth-1, off, size, typ, ok)
		return
	}
	for i := 0; i < 2; i++ {
		gotOff, gotSize, gotTyp, gotOK := runtime.CheckptrVarOf(p)
		if gotOK != ok || ok && (gotOff != off || gotSize != size || gotTyp != typ) {
			t.Errorf("%s: checkptrVarOf = %d, %d, %q, %v, want %d, %d, %q, %v", name, gotOff, gotSize, gotTyp, gotOK, off, size, typ, ok)
		}
	}
	if !runtime.StackVarAddr(p) {
		return
	}
	gotOff, gotSize, gotTyp, gotOK := runtime.StackVarOf(p)
	if gotOK != ok || ok && (gotOff != off || gotSize != size || gotTyp != typ) {
		t.Errorf("%s: stackVarOf = %d, %d, %q, %v, want %d, %d, %q, %v", name, gotOff, gotSize, gotTyp, gotOK, off, size, typ, ok)
	}
}

//go:noinline
func stackVarArgs(t *testing.T, a [100]byte, depth int) (r [30]int16) {
	stackVarWant(t, "argument", unsafe.Pointer(&a[40]), depth, 40, 100, "[100]uint8", true)
	stackVarWant(t, "result", unsafe.Pointer(&r[2]), depth, 4, 60, "[30]int16", true)
	return
}

func TestStackVarOf(t *testing.T) {
	var a [10]int32
	var pr stackVarPair
	var one byte
	for _, depth := range []int{0, 1, 7, 40} {
		stackVarWant(t, "array", unsafe.Pointer(&a), depth, 0, 40, "[10]int32", true)
		stackVarWant(t, "array element", unsafe.Pointer(&a[3]), depth, 12, 40, "[10]int32", true)
		stackVarWant(t, "last byte", unsafe.Add(unsafe.Pointer(&a[9]), 3), depth, 39, 40, "[10]int32", true)
		stackVarWant(t, "struct field", unsafe.Pointer(&pr.n), depth, 8, 16, "runtime_test.stackVarPair", true)
		stackVarWant(t, "byte", unsafe.Pointer(&one), depth, 0, 1, "uint8", true)
		// The end of a variable is in none.
		stackVarWant(t, "end of array", unsafe.Add(unsafe.Pointer(&a), 40), depth, 0, 0, "", false)
		stackVarWant(t, "end of struct", unsafe.Add(unsafe.Pointer(&pr), 16), depth, 0, 0, "", false)
		stackVarWant(t, "end of byte", unsafe.Add(unsafe.Pointer(&one), 1), depth, 0, 0, "", false)
		stackVarArgs(t, [100]byte{}, depth)

		stackVarWant(t, "global", unsafe.Pointer(&stackVarGlobal[2]), depth, 8, 20, "[5]uint32", true)
		stackVarWant(t, "end of global", unsafe.Add(unsafe.Pointer(&stackVarGlobal), 20), depth, 0, 0, "", false)
		stackVarWant(t, "global struct", unsafe.Pointer(&stackVarGlobalP.n), depth, 8, 16, "runtime_test.stackVarPair", true)
		// The bytes of a string, and the 0 that the compiler ends
		// them with.
		stackVarWant(t, "string", unsafe.Add(unsafe.Pointer(unsafe.StringData(stackVarString)), 5), depth, 5, uintptr(len(stackVarString))+1, "", true)
	}
	// A closure has the variable by reference: it is in the frame of
	// the function that made the closure.
	func() {
		stackVarWant(t, "closure", unsafe.Pointer(&a[1]), 2, 4, 40, "[10]int32", true)
	}()
	// A deferred call that runs while the goroutine panics, after a
	// fault in a function with no frame of its own.
	func() {
		defer func() {
			recover()
			stackVarWant(t, "panic", unsafe.Pointer(&a[1]), 2, 4, 40, "[10]int32", true)
		}()
		var np *int
		stackVarFault(np)
	}()
	// The stack moves.
	done := make(chan bool)
	go func() {
		var b [7]uint64
		for i := 0; i < 8; i++ {
			stackVarGrow(1 << i)
			stackVarWant(t, "moved", unsafe.Pointer(&b[6]), 3, 48, 56, "[7]uint64", true)
		}
		done <- true
	}()
	<-done
	_ = a[0] + int32(pr.n) + int32(one)
}

//go:noinline
func stackVarFault(p *int) int { return *p }

//go:noinline
func stackVarGrow(n int) int {
	var pad [256]byte
	if n == 0 {
		return int(pad[0])
	}
	return stackVarGrow(n-1) + int(pad[n%len(pad)])
}
