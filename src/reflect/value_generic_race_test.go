// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect_test

import (
	. "reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

//go:noinline
func raceLoad[T any](p *T) T { return *p }

//go:noinline
func raceStore[T any](p *T, x T) { *p = x }

type definedValue Value

// TestValueRaceGeneric races on a Value through generic code, which has
// its shape, and as a type defined from it. Neither may mix the words of
// two Values.
func TestValueRaceGeneric(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two threads")
	}
	a, b := new(int), new(string)
	A, B := ValueOf(a), ValueOf(b)
	check := func(v Value) {
		typ, p := v.Type(), v.Pointer()
		if typ == A.Type() && p != uintptr(unsafe.Pointer(a)) || typ == B.Type() && p != uintptr(unsafe.Pointer(b)) {
			panic("torn Value")
		}
	}
	t.Run("Shape", func(t *testing.T) {
		p := new(Value)
		*p = A
		raceValues(func(i int) {
			if i%2 == 0 {
				raceStore(p, A)
			} else {
				raceStore(p, B)
			}
		}, func() { check(raceLoad(p)) })
	})
	t.Run("Defined", func(t *testing.T) {
		p := new(definedValue)
		*p = definedValue(A)
		raceValues(func(i int) {
			if i%2 == 0 {
				*p = definedValue(A)
			} else {
				*p = definedValue(B)
			}
		}, func() { check(Value(*p)) })
	})
}

func raceValues(write func(int), read func()) {
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; !stop.Load(); i++ {
			write(i)
		}
	})
	wg.Go(func() {
		for !stop.Load() {
			read()
		}
	})
	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}
