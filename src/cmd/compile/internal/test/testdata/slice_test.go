// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This test makes sure that t.s = t.s[0:x] doesn't write
// either the slice pointer or the capacity.
// See issue #14855.

package main

import (
	"testing"
	"unsafe"
)

const N = 1000000

type X struct {
	s []int
}

func TestSlice(t *testing.T) {
	if unsafe.Sizeof([]int(nil)) == 16 {
		// gd: the length and the capacity are one word, stored with
		// the pointer, whole: a race keeps one store's slice
		// (doc/gd/slice16.md).
		t.Skip("a slice's length is stored with its pointer")
	}
	done := make(chan struct{})
	a := make([]int, N+10)

	x := &X{a}

	go func() {
		for i := 0; i < N; i++ {
			x.s = x.s[1:9]
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < N; i++ {
			x.s = x.s[0:8] // should only write len
		}
		done <- struct{}{}
	}()
	<-done
	<-done

	if cap(x.s) != cap(a)-N {
		t.Errorf("wanted cap=%d, got %d\n", cap(a)-N, cap(x.s))
	}
	if &x.s[0] != &a[N] {
		t.Errorf("wanted ptr=%p, got %p\n", &a[N], &x.s[0])
	}
}
