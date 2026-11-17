// layout.go: structs whose sizes, alignments and field offsets are
// compared with clang's for wasm32 (layout.h) by check.sh. Build it
// with GOOS=js GOARCH=wasm (or wasip1 and GOWASM=gc): it prints a line
// for each struct and each field.
package main

import (
	"fmt"
	"reflect"
	"unsafe"
)

type Ptrs struct {
	A    *int
	B    unsafe.Pointer
	Next *Ptrs
}

type Mixed struct {
	P   unsafe.Pointer
	I64 int64
	I   int
	B   bool
	F   float64
	C   byte
}

type Ints struct {
	C  int8
	I  int
	S  int16
	L  int64
	U  uint
	Up uintptr
}

type Floats struct {
	F32  float32
	F64  float64
	F32b float32
}

type Inner struct {
	C byte
	L int64
}

type Nested struct {
	B   bool
	In  Inner
	P   *int
	Arr [2]Inner
}

type Arrays struct {
	C [3]byte
	I [5]int32
	P [2]unsafe.Pointer
	L [1]int64
	Z bool
}

type PtrThenI64 struct {
	P unsafe.Pointer
	L int64
}

type I64ThenPtr struct {
	L int64
	P unsafe.Pointer
	C byte
}

type Bools struct {
	A bool
	B bool
	P unsafe.Pointer
	C bool
}

type Deep struct {
	N  Nested
	M  Mixed
	Ps [3]Ptrs
	D  float64
}

type Small struct {
	A int16
	B byte
}

type WithSmall struct {
	C byte
	S Small
	I int32
	T [3]Small
}

type GoSliceLike struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

type HasSlice struct {
	C byte
	S []byte
	D float64
}

func main() {
	for _, v := range []any{Ptrs{}, Mixed{}, Ints{}, Floats{}, Inner{}, Nested{}, Arrays{}, PtrThenI64{}, I64ThenPtr{}, Bools{}, Deep{}, Small{}, WithSmall{}, GoSliceLike{}, HasSlice{}} {
		t := reflect.TypeOf(v)
		fmt.Printf("struct %s %d %d\n", t.Name(), t.Size(), t.Align())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fmt.Printf("field %s %s %d\n", t.Name(), f.Name, f.Offset)
		}
	}
}
