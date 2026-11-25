// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"internal/abi"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
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
	viewSmall()
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
		// A slice of elements of the same size, with pointers where
		// they are: its length is in those (checkptr_compat.go).
		b := []byte("a slice")
		sink2 = *(*[]int8)(unsafe.Pointer(&b))
		sink2 = *(*[]bool)(unsafe.Pointer(&b))
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
		// A plain pointer is not a whole value: its bytes may be
		// looked at. (As an integer it may not: checkptr_compat.go.)
		p := new(int)
		sink2 = p
		viewWord = uint64((*[8]byte)(unsafe.Pointer(&p))[0])
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
		{"Small", 4},
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

type (
	viewPlainWords struct {
		p    *int
		a, b uint64
		c    uint64
	}
	// What a string's words are: a pointer and an integer.
	viewPlainPairs struct {
		p *byte
		n uint64
		q *byte
		m uint64
	}
	viewPadded struct {
		p *int
		f float64
		g float64
		b bool
	}
	viewRaceA struct {
		s string
		b []byte
	}
	viewRaceB struct {
		b []byte
		s string
	}
)

// viewSmall adds the views, through a plain unsafe.Pointer, of objects of
// 512 bytes or less, which have the states of their words in a bitmap of
// their span.
func viewSmall() {
	const P, W = "CheckPtrPartialSmall", "CheckPtrWholeSmall"
	viewCase(P+"IfaceAsString", func() { viewAs[string](viewPlain(new(any))) })
	viewCase(P+"IfaceSecondAsString", func() {
		viewAs[string](unsafe.Add(viewPlain(new(any)), viewWordSize))
	})
	viewCase(P+"IfaceAsTwoStrings", func() { viewAs[[2]string](viewPlain(new(any))) })
	viewCase(P+"IfaceShifted", func() {
		x := make([]any, 4)
		viewAs[any](unsafe.Add(viewPlain(&x[1]), viewWordSize))
	})
	viewCase(P+"StringSecondAsInt", func() {
		viewAs[int](unsafe.Add(viewPlain(new(string)), viewWordSize))
	})
	viewCase(P+"StringsAsIface", func() { viewAs[any](viewPlain(new([3]string))) })
	viewCase(P+"NestedField", func() {
		v := new(viewStruct)
		viewAs[uintptr](unsafe.Add(viewPlain(&v.s), viewWordSize))
	})
	viewCase(P+"Append", func() {
		var x []string
		x = append(x, "a", "b", "c")
		viewAs[[2]uint64](viewPlain(&x[1]))
	})
	viewCase(P+"Reflect", func() {
		t := reflect.StructOf([]reflect.StructField{
			{Name: "N", Type: reflect.TypeFor[int]()},
			{Name: "S", Type: reflect.TypeFor[string]()},
		})
		v := reflect.New(t)
		sink2 = v.Interface()
		viewAs[uintptr](unsafe.Add(v.UnsafePointer(), unsafe.Offsetof(viewReflectWhole{}.S)+viewWordSize))
	})
	viewCase(P+"ReflectArray", func() {
		v := reflect.New(reflect.ArrayOf(3, reflect.TypeFor[any]()))
		sink2 = v.Interface()
		viewAs[string](v.UnsafePointer())
	})
	viewCase(P+"AfterGC", func() {
		x := new(string)
		p := viewPlain(x)
		runtime.GC()
		runtime.GC()
		viewAs[[2]uintptr](p)
		runtime.KeepAlive(x)
	})
	viewCase(P+"ABIInterface", func() { viewAs[[2]uintptr](viewPlain(new(abi.EmptyInterface))) })
	viewCase(P+"ReflectValue", func() { viewAs[[2]uintptr](viewPlain(new(reflect.Value))) })

	if abi.SlicePacked {
		// Two words each, which are not the same two.
		viewCase(P+"StringAsSlice", func() { viewAs[[]byte](viewPlain(new(string))) })
		viewCase(P+"SliceAsString", func() { viewAs[string](viewPlain(new([]byte))) })
	}
	viewCase(P+"SliceOverIfaceEnd", func() {
		viewAs[[]byte](unsafe.Add(viewPlain(new(any)), unsafe.Sizeof(any(nil))-unsafe.Sizeof([]byte(nil))))
	})
	viewCase(P+"SliceAtStringSecond", func() {
		v := new(viewStringPlain)
		viewAs[[]byte](unsafe.Add(viewPlain(&v.s), viewWordSize))
	})
	viewCase(P+"SliceAtSliceSecond", func() {
		v := new(viewSlicePlain)
		viewAs[[]byte](unsafe.Add(viewPlain(&v.b), viewWordSize))
	})
	viewCase(P+"IfaceOverStringAndPlain", func() { viewAs[any](viewPlain(new(viewStringPlain))) })

	viewCase(W+"PastLastObject", func() {
		// Where a span has no object, up to and past its end.
		const pageSize, size = 8192, unsafe.Sizeof([3]string{})
		x := new([3]string)
		p := viewPlain(x)
		viewAs[[size]byte](unsafe.Add(p, pageSize/size*size-uintptr(p)%pageSize))
		runtime.KeepAlive(x)
	})
	viewCase(W+"ABIInterface", func() {
		viewAs[abi.EmptyInterface](viewPlain(new(any)))
		viewAs[any](viewPlain(new(abi.EmptyInterface)))
		var err error
		viewAs[abi.NonEmptyInterface](viewPlain(&err))
	})
	viewCase(W+"Array", func() {
		x := make([]string, 6)
		viewAs[[2]string](viewPlain(&x[2]))
		y := make([]any, 3)
		viewAs[any](viewPlain(&y[2]))
	})
	viewCase(W+"PlainField", func() {
		v := new(viewStruct)
		viewAs[uintptr](viewPlain(&v.n))
		viewAs[byte](viewPlain(&v.n))
	})
	viewCase(W+"PlainMemory", func() {
		// A string's type over memory that holds no whole value, in
		// a span that has a bitmap.
		sink2 = new([2]string)
		viewAs[[2]string](viewPlain(new(viewPlainPairs)))
	})
	viewCase(W+"Reflect", func() {
		t := reflect.StructOf([]reflect.StructField{
			{Name: "N", Type: reflect.TypeFor[int]()},
			{Name: "S", Type: reflect.TypeFor[string]()},
		})
		v := reflect.New(t)
		sink2 = v.Interface()
		viewAs[viewReflectWhole](v.UnsafePointer())
		viewAs[int](v.UnsafePointer())
	})
	viewCase(W+"ReflectValue", func() { viewAs[reflect.Value](viewPlain(new(reflect.Value))) })
	viewCase(W+"Reused", func() {
		// The slots of whole values, freed, and allocated again as
		// types without any, regular memory or not.
		for range 8 {
			keep := make([]*[2]string, 2000)
			for i := range keep {
				keep[i] = new([2]string)
			}
			sink2 = keep
			sink2 = nil
			keep = nil
			runtime.GC()
			runtime.GC()
			for range 2000 {
				viewAs[[unsafe.Sizeof(viewPlainWords{})]byte](viewPlain(new(viewPlainWords)))
				viewAs[[unsafe.Sizeof(viewPadded{})]byte](viewPlain(new(viewPadded)))
			}
		}
	})
	viewCase(W+"Race", viewSmallRace)
}

// A whole value, and plain words after it.
type viewStringPlain struct {
	s string
	n [4]uintptr
}

type viewSlicePlain struct {
	b []byte
	n [4]uintptr
}

type viewReflectWhole struct {
	N int
	S string
}

// viewSmallRace has goroutines publish new objects whose slots had
// another layout, and others convert them as they arrive: a reader that
// saw the object before its states would die.
func viewSmallRace() {
	var slot atomic.Pointer[viewRaceA]
	var stop atomic.Bool
	var wg sync.WaitGroup
	n := max(runtime.GOMAXPROCS(0)/2, 1)
	for range n {
		wg.Go(func() {
			for !stop.Load() {
				a := new(viewRaceA)
				slot.Store(a)
				for range 4 {
					viewRaceSink.Store(new(viewRaceB))
				}
			}
		})
		wg.Go(func() {
			for !stop.Load() {
				if p := slot.Load(); p != nil {
					viewAs[viewRaceA](viewHide(unsafe.Pointer(p)))
				}
			}
		})
	}
	time.Sleep(300 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

var viewRaceSink atomic.Pointer[viewRaceB]

//go:noinline
func viewHide(p unsafe.Pointer) unsafe.Pointer { return p }
