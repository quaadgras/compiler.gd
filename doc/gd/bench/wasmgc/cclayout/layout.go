// layout.go: the structs of layout.h that Go can declare, as Go declares
// them for wasm32 (C's long is int32, size_t uintptr, as cgo has them on
// a 32-bit target). check.sh compares what it prints with what cmd/cc
// prints for the C.
package main

import (
	"fmt"
	"reflect"
	"unsafe"
)

type Scalars struct {
	C  int8
	S  int16
	I  int32
	L  int32
	Ll int64
	F  float32
	D  float64
	P  unsafe.Pointer
	Z  uintptr
	Pd int32
	Ip int32
	Up uintptr
	B  bool
	W  int32
}

type LongFirst struct {
	L int32
	C int8
}

type LLAfterChar struct {
	C  int8
	Ll int64
}

type DoubleAfterInt struct {
	I int32
	D float64
}

type Ptrs struct {
	A    *int32
	B    unsafe.Pointer
	Next *Ptrs
	Fn   unsafe.Pointer
}

type Longs struct {
	A int32
	B uint32
	C [3]int32
	D int8
}

type Nested struct {
	C  int8
	In LLAfterChar
	L  int32
	P  Ptrs
}

type Arrays struct {
	C [3]int8
	L [5]int32
	P [2]unsafe.Pointer
	D [2]float64
	S int16
}

type Fixed struct {
	A int8
	B int64
	C int32
	D int32
	E int64
	F uint64
	G int64
}

type Enums struct {
	C int8
	S uint32
	D int8
	B int64
}

type Complex struct {
	C int8
	Z complex128
	W complex64
}

func main() {
	for _, v := range []any{Scalars{}, LongFirst{}, LLAfterChar{}, DoubleAfterInt{}, Ptrs{}, Longs{}, Nested{}, Arrays{}, Fixed{}, Enums{}, Complex{}} {
		t := reflect.TypeOf(v)
		fmt.Printf("struct %s %d %d\n", t.Name(), t.Size(), t.Align())
		for i := 0; i < t.NumField(); i++ {
			fmt.Printf("field %s %d %d\n", t.Name(), i, t.Field(i).Offset)
		}
	}
}
