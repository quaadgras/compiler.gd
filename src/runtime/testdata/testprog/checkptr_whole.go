// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"internal/abi"
	"reflect"
	"slices"
	"sync/atomic"
	"unsafe"
)

// Views of strings, slices and interfaces (doc/gd/unsafe.md). A case named
// CheckPtrPartial... must die at the conversion, and one named
// CheckPtrWhole... must not. CheckPtrViewList prints their names.

var (
	viewCases = map[string]func(){}
	viewWord  uint64

	viewString string
	viewSlice  []byte
	viewIface  any
	viewNested viewStruct
	viewArray  [2]string
)

type viewStruct struct {
	n int
	s string
	b []byte
	e any
}

const viewWordSize = unsafe.Sizeof(uintptr(0))

func viewCase(name string, f func()) {
	viewCases[name] = f
	register(name, f)
}

func init() {
	register("CheckPtrViewList", func() {
		names := make([]string, 0, len(viewCases))
		for name := range viewCases {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			fmt.Println(name)
		}
	})
	viewKind[string]("String", &viewString)
	viewKind[[]byte]("Slice", &viewSlice)
	viewKind[any]("Iface", &viewIface)
	viewSame()
	viewNestedCases()
	viewDynamic[string]("String")
	viewDynamic[[]byte]("Slice")
	viewDynamic[any]("Iface")
	viewPuns()
	viewLong()
	viewNil[string]("String")
	viewNil[[]byte]("Slice")
	viewNil[any]("Iface")
}

// viewNil adds the conversions of a nil pointer, which views nothing.
func viewNil[T any](kind string) {
	viewCase("CheckPtrWholeNil"+kind, func() {
		var p *T
		if (*[2]uint64)(unsafe.Pointer(p)) != nil {
			panic("not nil")
		}
	})
}

// viewKind adds the views of a value of type T that are partial, with the
// memory known to the compiler: in the stack, a global and the heap.
func viewKind[T any](kind string, global *T) {
	name := "CheckPtrPartial" + kind
	viewCase(name+"StackWords", func() {
		var x T
		viewWord = (*[2]uint64)(unsafe.Pointer(&x))[0]
	})
	viewCase(name+"StackByte", func() {
		var x T
		viewWord = uint64(*(*byte)(unsafe.Pointer(&x)))
	})
	viewCase(name+"StackSecond", func() {
		var x T
		viewWord = uint64(*(*uintptr)(unsafe.Add(unsafe.Pointer(&x), viewWordSize)))
	})
	viewCase(name+"GlobalWords", func() {
		viewWord = (*[2]uint64)(unsafe.Pointer(global))[0]
	})
	viewCase(name+"GlobalByte", func() {
		viewWord = uint64(*(*byte)(unsafe.Pointer(global)))
	})
	viewCase(name+"GlobalSecond", func() {
		viewWord = uint64(*(*uintptr)(unsafe.Add(unsafe.Pointer(global), viewWordSize)))
	})
	viewCase(name+"HeapWords", func() {
		x := new(T)
		sink2 = x
		viewWord = (*[2]uint64)(unsafe.Pointer(x))[0]
	})
	viewCase(name+"HeapByte", func() {
		x := new(T)
		sink2 = x
		viewWord = uint64(*(*byte)(unsafe.Pointer(x)))
	})
	viewCase(name+"HeapSecond", func() {
		x := new(T)
		sink2 = x
		viewWord = uint64(*(*uintptr)(unsafe.Add(unsafe.Pointer(x), viewWordSize)))
	})
	// A variable that holds the pointer, assigned once.
	viewCase(name+"LocalPointer", func() {
		var x T
		p := unsafe.Pointer(&x)
		viewWord = (*[2]uint64)(p)[0]
	})
}

// viewSame adds the views that are whole.
func viewSame() {
	viewCase("CheckPtrWholeString", func() {
		s := "a string"
		sink2 = *(*string)(unsafe.Pointer(&s))
		sink2 = *(*[1]string)(unsafe.Pointer(&s))
		sink2 = *(*struct{ s string })(unsafe.Pointer(&s))
	})
	viewCase("CheckPtrWholeSlice", func() {
		b := []byte("a slice")
		sink2 = *(*[]int)(unsafe.Pointer(&b))
		sink2 = *(*[]string)(unsafe.Pointer(&b))
	})
	viewCase("CheckPtrWholeIface", func() {
		var e any = 1
		sink2 = (*abi.EmptyInterface)(unsafe.Pointer(&e)).Type
		var err error = fmt.Errorf("an error")
		sink2 = (*abi.NonEmptyInterface)(unsafe.Pointer(&err)).ITab
		sink2 = *(*any)(unsafe.Pointer(&e))
	})
	viewCase("CheckPtrWholeArray", func() {
		a := [3]string{"a", "b", "c"}
		sink2 = *(*[2]string)(unsafe.Pointer(&a[1]))
		sink2 = *(*string)(unsafe.Add(unsafe.Pointer(&a), unsafe.Sizeof("")))
	})
	viewCase("CheckPtrWholeField", func() {
		var v viewStruct
		viewWord = uint64(*(*uint)(unsafe.Pointer(&v.n)))
		sink2 = *(*struct {
			n int
			s string
		})(unsafe.Pointer(&v))
	})
	viewCase("CheckPtrWholePointerWord", func() {
		// A plain pointer is not a whole value.
		p := new(int)
		sink2 = p
		viewWord = uint64(*(*uintptr)(unsafe.Pointer(&p)))
	})
	viewCase("CheckPtrWholeZeroSize", func() {
		var s string
		sink2 = (*struct{})(unsafe.Pointer(&s))
	})
}

// viewNestedCases adds views of values inside structs and arrays.
func viewNestedCases() {
	viewCase("CheckPtrPartialNestedPastField", func() {
		var v viewStruct
		// (A string is aligned to 16 where it is two 8-byte words.)
		viewWord = (*[3]uint64)(unsafe.Pointer(&v.n))[1]
	})
	viewCase("CheckPtrPartialNestedStringSecond", func() {
		viewWord = uint64(*(*uintptr)(unsafe.Add(unsafe.Pointer(&viewNested.s), viewWordSize)))
	})
	viewCase("CheckPtrPartialNestedOffset", func() {
		viewWord = uint64(*(*uintptr)(unsafe.Add(unsafe.Pointer(&viewNested), unsafe.Offsetof(viewNested.b)+viewWordSize)))
	})
	viewCase("CheckPtrPartialNestedIfaceByte", func() {
		viewWord = uint64(*(*byte)(unsafe.Pointer(&viewNested.e)))
	})
	viewCase("CheckPtrPartialArrayElemByte", func() {
		viewWord = uint64(*(*byte)(unsafe.Pointer(&viewArray[1])))
	})
	viewCase("CheckPtrPartialArrayShifted", func() {
		sink2 = *(*string)(unsafe.Add(unsafe.Pointer(&viewArray), viewWordSize))
	})
	viewCase("CheckPtrPartialIfaceAsString", func() {
		var e any = 1
		sink2 = *(*string)(unsafe.Pointer(&e))
	})
	viewCase("CheckPtrPartialIfaceAsTwoStrings", func() {
		var e any = 1
		sink2 = *(*[2]string)(unsafe.Pointer(&e))
	})
	viewCase("CheckPtrPartialStringAsIface", func() {
		a := [2]string{"a", "b"}
		sink2 = *(*any)(unsafe.Pointer(&a))
	})
	viewCase("CheckPtrPartialSliceHeader", func() {
		b := []byte("a slice")
		sink2 = (*struct {
			p unsafe.Pointer
			n int
		})(unsafe.Pointer(&b))
	})
}

//go:noinline
func viewAs[V any](p unsafe.Pointer) {
	sink2 = (*V)(p)
}

// viewDynamic adds the views through a plain unsafe.Pointer, of objects
// that record their type: those with a malloc header and large ones.
func viewDynamic[T any](kind string) {
	sizes := []struct {
		name string
		n    int
	}{
		{"Header", 1024 / int(unsafe.Sizeof(*new(T)))},
		{"Large", 64 << 10 / int(unsafe.Sizeof(*new(T)))},
	}
	for _, size := range sizes {
		make1 := func() unsafe.Pointer {
			x := make([]T, size.n)
			sink2 = x
			return unsafe.Pointer(&x[1])
		}
		name := "CheckPtrPartial" + kind + size.name
		viewCase(name+"Words", func() { viewAs[[2]uint64](make1()) })
		viewCase(name+"Byte", func() { viewAs[byte](make1()) })
		viewCase(name+"Second", func() { viewAs[uintptr](unsafe.Add(make1(), viewWordSize)) })
		viewCase("CheckPtrWhole"+kind+size.name, func() {
			viewAs[T](make1())
			viewAs[[2]T](make1())
		})
	}
}

// viewPuns adds a string viewed as a slice, and a slice as a string,
// which are what they are by the layout of a slice: with 4-byte words a
// string is a slice's first two words (doc/gd/slice16.md).
func viewPuns() {
	name := "CheckPtrPartial"
	if !abi.SlicePacked {
		name = "CheckPtrWhole"
	}
	viewCase(name+"SliceAsString", func() {
		b := []byte("a slice")
		sink2 = *(*string)(unsafe.Pointer(&b))
	})
	viewCase(name+"StringAsSlice", func() {
		s := "a string"
		sink2 = *(*[]byte)(unsafe.Pointer(&s))
	})
}

// viewLong adds views in generic code, where a type is its shape, and
// views whose type is longer than the object.
func viewLong() {
	viewCase("CheckPtrWholeGenericShapes", func() {
		// What generic code has for these types is their shape.
		x := make([]reflect.Value, 40)
		viewAs[reflect.Value](viewPlain(&x[1]))
		y := make([]any, 40)
		viewAs[abi.EmptyInterface](viewPlain(&y[1]))
		viewAs[reflect.Value](viewPlain(new(reflect.Value)))
		var q atomic.Pointer[reflect.Value]
		q.Store(&x[2])
		sink2 = q.Load()
		viewShapeStatic[abi.EmptyInterface]()
	})
	viewCase("CheckPtrWholePastObject", func() {
		// A view whose type is longer than the object is of the
		// object up to its end.
		x := new(struct {
			s   string
			pad [1000]byte
		})
		sink2 = x
		viewAs[[1 << 20]byte](viewPlain(&x.pad[990]))
		y := new(struct {
			s   string
			pad [64]byte
		})
		sink2 = y
		viewAs[[1 << 20]byte](viewPlain(&y.pad[60]))
	})
}

//go:noinline
func viewPlain[T any](p *T) unsafe.Pointer {
	sink2 = p
	return unsafe.Pointer(p)
}

//go:noinline
func viewShapeStatic[T any]() {
	var e any = 1
	sink2 = *(*T)(unsafe.Pointer(&e))
}
