// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/abi"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

const wholeWord = unsafe.Sizeof(uintptr(0))

// wholeLayout returns the states of a word of each kind of value.
func wholeLayout[T any](first string) string {
	return first + strings.Repeat(".", int(unsafe.Sizeof(*new(T))/wholeWord)-1)
}

type wholeMixed struct {
	n int
	s string
	b []byte
	e any
	p *int
}

func wholeMixedStates() string {
	var v wholeMixed
	w := make([]byte, unsafe.Sizeof(v)/wholeWord)
	for i := range w {
		w[i] = '-'
	}
	put := func(off uintptr, s string) { copy(w[off/wholeWord:], s) }
	put(unsafe.Offsetof(v.s), wholeLayout[string]("F"))
	put(unsafe.Offsetof(v.b), wholeLayout[[]byte]("S"))
	put(unsafe.Offsetof(v.e), wholeLayout[any]("F"))
	return string(w)
}

func TestWholeBits(t *testing.T) {
	if runtime.GOARCH == "wasm" {
		t.Skip("the checks are off")
	}
	str, sli, ifc := wholeLayout[string]("F"), wholeLayout[[]byte]("S"), wholeLayout[any]("F")
	check := func(name string, p unsafe.Pointer, want string) {
		t.Helper()
		if got := runtime.WholeStates(p, len(want)); got != want {
			t.Errorf("%s: states %q, want %q", name, got, want)
		}
	}

	keep := make([]any, 0, 16)
	s := new(string)
	check("string", unsafe.Pointer(s), str)
	b := new([]byte)
	check("slice", unsafe.Pointer(b), sli)
	e := new(any)
	check("interface", unsafe.Pointer(e), ifc)
	m := new(wholeMixed)
	check("struct", unsafe.Pointer(m), wholeMixedStates())
	ae := new(abi.EmptyInterface)
	check("abi.EmptyInterface", unsafe.Pointer(ae), ifc)
	rv := new(reflect.Value)
	check("reflect.Value", unsafe.Pointer(rv), "F"+strings.Repeat(".", int(unsafe.Sizeof(*rv)/wholeWord)-1))
	keep = append(keep, s, b, e, m, ae, rv)

	// Arrays: make, and what append grows.
	a := make([]string, 3)
	check("make", unsafe.Pointer(&a[0]), str+str+str)
	var g []any
	for range 3 {
		g = append(g, nil)
	}
	check("append", unsafe.Pointer(&g[0]), strings.Repeat(ifc, cap(g)))
	keep = append(keep, a, g)

	// Types made at run time.
	rt := reflect.StructOf([]reflect.StructField{
		{Name: "N", Type: reflect.TypeFor[int]()},
		{Name: "S", Type: reflect.TypeFor[string]()},
		{Name: "B", Type: reflect.TypeFor[[]byte]()},
	})
	r := reflect.New(rt)
	want := make([]byte, rt.Size()/wholeWord)
	for i := range want {
		want[i] = '-'
	}
	copy(want[rt.Field(1).Offset/wholeWord:], str)
	copy(want[rt.Field(2).Offset/wholeWord:], sli)
	check("reflect.StructOf", r.UnsafePointer(), string(want))
	ra := reflect.New(reflect.ArrayOf(2, reflect.TypeFor[any]()))
	check("reflect.ArrayOf", ra.UnsafePointer(), ifc+ifc)
	rs := reflect.MakeSlice(reflect.SliceOf(reflect.TypeFor[string]()), 2, 2)
	check("reflect.MakeSlice", rs.UnsafePointer(), str+str)
	keep = append(keep, r.Interface(), ra.Interface(), rs.Interface())

	// They survive the collector.
	runtime.GC()
	runtime.GC()
	check("string after GC", unsafe.Pointer(s), str)
	check("struct after GC", unsafe.Pointer(m), wholeMixedStates())
	check("make after GC", unsafe.Pointer(&a[0]), str+str+str)

	// Objects of the goroutine alone are in spans of their own.
	if runtime.LocalSpans {
		l := runtime.NewLocalTyped(abi.TypeFor[string](), 3)
		if !runtime.InLocalSpan(l) {
			t.Errorf("not in a span of local objects")
		}
		check("local", l, str+str+str)
		li := runtime.NewLocalTyped(abi.TypeFor[wholeMixed](), 1)
		check("local struct", li, wholeMixedStates())
		runtime.GC()
		check("local after GC", l, str+str+str)
		runtime.KeepAlive(l)
		runtime.KeepAlive(li)
	}

	// No pointers, no bitmap.
	n := new([4]uint64)
	wholeBenchSink = unsafe.Pointer(n)
	if got := runtime.WholeStates(unsafe.Pointer(n), 4); got != "" {
		t.Errorf("noscan object has states %q", got)
	}
	runtime.KeepAlive(keep)
	runtime.KeepAlive(n)
}

type (
	wholeReuseA struct {
		s string
		e string
	}
	wholeReuseRegular struct {
		p *int
		a [3]uintptr
	}
	wholeReusePadded struct {
		p *int
		f float64
		g float64
		b bool
	}
)

// A slot that held a whole value and is allocated again as a type with
// none must not keep its states.
func TestWholeBitsReuse(t *testing.T) {
	if runtime.GOARCH == "wasm" || unsafe.Sizeof(wholeReuseA{}) != unsafe.Sizeof(wholeReuseRegular{}) || unsafe.Sizeof(wholeReuseA{}) != unsafe.Sizeof(wholeReusePadded{}) {
		t.Skip("needs three types of one size")
	}
	words := int(unsafe.Sizeof(wholeReuseA{}) / wholeWord)
	plain := strings.Repeat("-", words)
	reused := 0
	for range 20 {
		fill := make([]*wholeReuseA, 4096)
		for i := range fill {
			fill[i] = new(wholeReuseA)
		}
		runtime.KeepAlive(fill)
		fill = nil
		runtime.GC()
		runtime.GC()
		next := make([]unsafe.Pointer, 0, 4096)
		for i := range 4096 {
			var p unsafe.Pointer
			if i%2 == 0 {
				p = unsafe.Pointer(new(wholeReuseRegular))
			} else {
				p = unsafe.Pointer(new(wholeReusePadded))
			}
			next = append(next, p)
			switch got := runtime.WholeStates(p, words); got {
			case "":
			case plain:
				reused++
			default:
				t.Fatalf("new object without whole values has states %q", got)
			}
		}
		runtime.KeepAlive(next)
	}
	if reused == 0 {
		t.Errorf("no object was allocated in a span with a bitmap")
	}
}

var wholeBenchSink unsafe.Pointer
