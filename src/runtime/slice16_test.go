// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x)

package runtime_test

import (
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Huge slices (doc/gd/slice16.md): with 8-byte words a slice whose
// capacity does not fit in 32 bits is the address of a header and a
// marker. These tests make slices of more than 2^32 bytes over memory
// that is only reserved, and touch a few pages of it.

const hugeLen = 5 << 30 // bytes

// hugeBytes returns a []byte of hugeLen bytes over a mapping that the
// kernel commits as it is touched, and a function that unmaps it.
func hugeBytes(t *testing.T) ([]byte, func()) {
	if unsafe.Sizeof(uintptr(0)) != 8 || runtime.GOARCH == "wasm" {
		t.Skip("slices are not packed")
	}
	if runtime.GOOS != "linux" {
		t.Skip("needs MAP_NORESERVE")
	}
	b, err := syscall.Mmap(-1, 0, hugeLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE|syscall.MAP_NORESERVE)
	if err != nil {
		t.Skipf("mmap of %d bytes: %v", hugeLen, err)
	}
	return b, func() { syscall.Munmap(b) }
}

// hugeHolder holds a slice in the heap, where a huge one's header is
// reached only through it.
type hugeHolder struct {
	pad [3]int
	B   []byte
	S   string
	V   any
}

var hugeGlobal []byte

//go:noinline
func hugePass(b []byte) []byte { return b }

//go:noinline
func hugeLenCap(b []byte) (int, int) { return len(b), cap(b) }

//go:noinline
func hugeAt(b []byte, i int) byte { return b[i] }

func TestSliceHuge(t *testing.T) {
	b, unmap := hugeBytes(t)
	defer unmap()
	const hi = 1<<32 + 12345 // an index above 2^32
	if len(b) != hugeLen || cap(b) != hugeLen {
		t.Fatalf("len %d cap %d, want %d", len(b), cap(b), hugeLen)
	}
	b[0], b[hi], b[hugeLen-1] = 1, 2, 3

	// Calls, results and the heap.
	c := hugePass(b)
	if l, k := hugeLenCap(c); l != hugeLen || k != hugeLen {
		t.Fatalf("passed: len %d cap %d", l, k)
	}
	if hugeAt(c, hi) != 2 || hugeAt(c, hugeLen-1) != 3 {
		t.Fatalf("passed: wrong bytes")
	}
	h := runtime.HeapNew[hugeHolder]()
	h.B = b
	hugeGlobal = b
	b = nil
	c = nil
	runtime.GC() // the header is kept alive by h.B and hugeGlobal alone
	runtime.GC()
	if len(h.B) != hugeLen || h.B[hi] != 2 || len(hugeGlobal) != hugeLen || hugeGlobal[hugeLen-1] != 3 {
		t.Fatalf("after GC: len %d", len(h.B))
	}
	b = h.B
	hugeGlobal = nil

	// Slicing: huge to huge, huge to small, small to small.
	s := b[1:]
	if len(s) != hugeLen-1 || cap(s) != hugeLen-1 || s[hi-1] != 2 {
		t.Fatalf("b[1:]: len %d cap %d", len(s), cap(s))
	}
	s = b[:hi+1]
	if len(s) != hi+1 || cap(s) != hugeLen || s[hi] != 2 {
		t.Fatalf("b[:hi+1]: len %d cap %d", len(s), cap(s))
	}
	s = b[hi:]
	if len(s) != hugeLen-hi || s[0] != 2 {
		t.Fatalf("b[hi:]: len %d", len(s))
	}
	s = b[hi : hi+10 : hi+20]
	if len(s) != 10 || cap(s) != 20 || s[0] != 2 {
		t.Fatalf("b[hi:hi+10:hi+20]: len %d cap %d", len(s), cap(s))
	}
	s = b[0 : 0 : 1<<32-1] // the least huge capacity
	if len(s) != 0 || cap(s) != 1<<32-1 {
		t.Fatalf("b[0:0:2^32-1]: len %d cap %d", len(s), cap(s))
	}
	h.B = s
	if len(h.B) != 0 || cap(h.B) != 1<<32-1 {
		t.Fatalf("stored b[0:0:2^32-1]: len %d cap %d", len(h.B), cap(h.B))
	}
	s = b[0 : 0 : 1<<32-2] // the greatest that is not
	h.B = s
	if cap(h.B) != 1<<32-2 {
		t.Fatalf("stored b[0:0:2^32-2]: cap %d", cap(h.B))
	}

	// Append in place to a huge slice (the length changes, so does the
	// header), and one that grows past 2^32.
	s = b[:hi]
	s = append(s, 7, 8)
	if len(s) != hi+2 || cap(s) != hugeLen || b[hi] != 7 || b[hi+1] != 8 {
		t.Fatalf("append: len %d cap %d", len(s), cap(s))
	}
	h.B = b[:hi]
	h.B = append(h.B, 9)
	if len(h.B) != hi+1 || b[hi] != 9 {
		t.Fatalf("append in a field: len %d", len(h.B))
	}

	// copy, and range over a part.
	src := []byte{4, 5, 6}
	if n := copy(b[hi:], src); n != 3 || b[hi+2] != 6 {
		t.Fatalf("copy: %d", n)
	}
	if n := copy(b[2:5], b[hi:]); n != 3 || b[2] != 4 {
		t.Fatalf("copy from huge: %d", n)
	}
	sum := 0
	for i, x := range b[hi : hi+3] {
		sum += i * int(x)
	}
	if sum != 5+12 {
		t.Fatalf("range: %d", sum)
	}
	n := 0
	for range b[hugeLen-5:] {
		n++
	}
	if n != 5 {
		t.Fatalf("range count %d", n)
	}

	// Interfaces, maps, channels.
	var e any = b
	if bb := e.([]byte); len(bb) != hugeLen || bb[hi] != 4 {
		t.Fatalf("interface: len %d", len(bb))
	}
	h.V = b
	m := map[int][]byte{1: b}
	if len(m[1]) != hugeLen || m[1][hugeLen-1] != 3 {
		t.Fatalf("map: len %d", len(m[1]))
	}
	ch := make(chan []byte, 1)
	ch <- b
	if r := <-ch; len(r) != hugeLen || r[hi] != 4 {
		t.Fatalf("chan: len %d", len(r))
	}
	a := [2][]byte{b, src}
	a2 := a
	if len(a2[0]) != hugeLen || len(a2[1]) != 3 {
		t.Fatalf("array copy")
	}

	// reflect.
	rv := reflect.ValueOf(b)
	if rv.Len() != hugeLen || rv.Cap() != hugeLen || rv.Index(hi).Uint() != 4 {
		t.Fatalf("reflect: len %d cap %d", rv.Len(), rv.Cap())
	}
	if rs := rv.Slice(hi, hugeLen); rs.Len() != hugeLen-hi || rs.Index(0).Uint() != 4 {
		t.Fatalf("reflect Slice: len %d", rs.Len())
	}
	if rs := rv.Slice3(1, hi, hugeLen); rs.Len() != hi-1 || rs.Cap() != hugeLen-1 {
		t.Fatalf("reflect Slice3: len %d cap %d", rs.Len(), rs.Cap())
	}
	rf := reflect.ValueOf(h).Elem().Field(1)
	rf.Set(reflect.ValueOf(b))
	if len(h.B) != hugeLen {
		t.Fatalf("reflect Set: len %d", len(h.B))
	}
	rf.SetLen(hi)
	if len(h.B) != hi || cap(h.B) != hugeLen {
		t.Fatalf("reflect SetLen: len %d cap %d", len(h.B), cap(h.B))
	}
	rf.SetCap(hi + 1)
	if len(h.B) != hi || cap(h.B) != hi+1 {
		t.Fatalf("reflect SetCap: len %d cap %d", len(h.B), cap(h.B))
	}
	if !reflect.DeepEqual(b[hi:hi+3], []byte{4, 5, 6}) {
		t.Fatalf("DeepEqual")
	}
	if bs := reflect.ValueOf(h).Elem().Field(1).Bytes(); len(bs) != hi {
		t.Fatalf("reflect Bytes: len %d", len(bs))
	}
	runtime.KeepAlive(h)
}

// TestSliceHugeZeroSize has huge slices of a zero-size type, whose arrays
// take no memory.
func TestSliceHugeZeroSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("needs 8-byte words")
	}
	s := make([]struct{}, 1<<33)
	if len(s) != 1<<33 || cap(s) != 1<<33 {
		t.Fatalf("len %d cap %d", len(s), cap(s))
	}
	s = s[1<<32:]
	if len(s) != 1<<32 {
		t.Fatalf("resliced: len %d", len(s))
	}
	s = append(s, struct{}{})
	if len(s) != 1<<32+1 {
		t.Fatalf("appended: len %d", len(s))
	}
	h := &struct{ s []struct{} }{s}
	runtime.GC()
	if len(h.s) != 1<<32+1 {
		t.Fatalf("after GC: len %d", len(h.s))
	}
}
