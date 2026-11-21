// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"sync/atomic"
	"testing"
	"unsafe"
)

//go:noinline
//go:nosplit
func firstOfTwo(a, b uintptr) uintptr { return a }

// addrAtCall returns the address of the stack that p holds as the callee
// got it, and as it is on return. h points into the heap.
//
//go:noinline
func addrAtCall(p *int, h *[64]int) (got, now uintptr) {
	got = firstOfTwo(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(h)))
	now = uintptr(unsafe.Pointer(p))
	return
}

//go:noinline
func deep(n int, f func()) {
	var pad [256]byte
	if n > 0 {
		deep(n-1, f)
	} else {
		f()
	}
	runtime.KeepAlive(&pad)
}

// TestExposeptrKeepsStack checks that the stack does not move between the
// conversions of the arguments of a call: a system call is given the
// address of a variable of the stack as an integer, and the conversion of
// a later argument calls runtime.exposeptr.
func TestExposeptrKeepsStack(t *testing.T) {
	var stop atomic.Bool
	done := make(chan bool)
	go func() {
		for !stop.Load() {
			runtime.GC()
		}
		done <- true
	}()
	bad := 0
	for i := 0; i < 300 && bad == 0; i++ {
		c := make(chan int)
		go func() {
			n := 0
			// Grow the stack, so that the collector shrinks it
			// when it next can.
			deep(200, func() {})
			var x int
			for j := 0; j < 20000; j++ {
				// A new object each time: one that is not exposed yet.
				got, now := addrAtCall(&x, new([64]int))
				if got != now {
					n++
				}
			}
			c <- n
		}()
		bad += <-c
	}
	stop.Store(true)
	<-done
	if bad != 0 {
		t.Errorf("the stack moved %d times between the conversion of an argument and the call", bad)
	}
}
