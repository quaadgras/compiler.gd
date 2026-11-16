// moves copies and zeroes values through the runtime, which knows them
// only by their type: whole objects, values within objects, values with
// nested arrays, and values seen as of another type.
package main

import (
	"fmt"
	"reflect"
	"unsafe"
)

type inner struct {
	Name string
	N    int32
	Ok   bool
	F    float64
}

type outer struct {
	ID    int
	In    inner
	Tags  [3]string
	P     *int
	Small [2]int16
	In2   inner
	Big   [2]inner
}

type words struct {
	A uint64
	P *int
	B uint64
}

type halves struct {
	Lo, Hi uint32
	P      *int
	B      uint64
}

func mk(i int) outer {
	x := i
	return outer{
		ID:    i,
		In:    inner{fmt.Sprint("in", i), int32(i), i%2 == 0, float64(i) / 2},
		Tags:  [3]string{fmt.Sprint("a", i), "b", fmt.Sprint("a long string that is not inline ", i)},
		P:     &x,
		Small: [2]int16{int16(i), int16(-i)},
		In2:   inner{"two", int32(-i), true, 1.5},
		Big:   [2]inner{{Name: "b0", N: 7}, {Name: "b1", F: 2.5}},
	}
}

func show(o outer) string {
	return fmt.Sprint(o.ID, o.In, o.Tags, *o.P, o.Small, o.In2, o.Big)
}

func main() {
	// Elements of a slice: copy, append, and a copy that overlaps.
	s := make([]outer, 4)
	for i := range s {
		s[i] = mk(i)
	}
	d := make([]outer, 4)
	fmt.Println(copy(d, s), show(d[3]), show(d[0]))
	copy(s[1:], s)
	fmt.Println(show(s[0]), show(s[1]), show(s[3]))
	copy(s, s[2:])
	fmt.Println(show(s[0]), show(s[1]))
	clear(d[1:3])
	fmt.Println(d[1].ID, d[1].In, d[1].Tags, d[1].P == nil, d[2].Big, show(d[3]))

	// Through reflect: whole values, and values within them.
	a, b := mk(10), mk(20)
	va, vb := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	va.Field(1).Set(vb.Field(1))
	va.Field(2).Set(vb.Field(2))
	va.Field(6).Set(vb.Field(6))
	va.Field(6).Index(1).Set(vb.Field(5))
	fmt.Println(show(a))
	va.Set(vb)
	fmt.Println(show(a))
	va.Field(5).Set(reflect.Zero(va.Field(5).Type()))
	va.Field(2).SetZero()
	va.Field(6).Index(0).SetZero()
	fmt.Println(show(a))
	reflect.Copy(reflect.ValueOf(d), reflect.ValueOf(s))
	fmt.Println(show(d[2]))

	// Channels and maps.
	c := make(chan outer, 2)
	c <- mk(5)
	c <- mk(6)
	fmt.Println(show(<-c), show(<-c))
	m := map[string]outer{}
	for i := 0; i < 20; i++ {
		m[fmt.Sprint(i)] = mk(i)
	}
	delete(m, "7")
	fmt.Println(len(m), show(m["13"]), m["7"].ID)
	mi := map[inner][2]inner{}
	mi[inner{"k", 1, true, 2}] = [2]inner{{Name: "v"}, {N: 3}}
	fmt.Println(mi[inner{"k", 1, true, 2}], mi[inner{"k", 1, false, 2}])

	// Interfaces hold copies.
	var e any = a
	a.ID = 99
	fmt.Println(e.(outer).ID, a.ID)
	es := []any{a, b, inner{"x", 1, true, 1}}
	es2 := append([]any(nil), es...)
	a.In.Name = "changed"
	fmt.Println(es2[0].(outer).In.Name, es2[2])

	// A value seen as of another type: its bytes, where the fields are
	// not those of the type.
	w := words{A: 0x1122334455667788, B: 0xAABBCCDDEEFF0011}
	h := (*halves)(unsafe.Pointer(&w))
	hv := reflect.ValueOf(h).Elem()
	hv.Field(1).Set(reflect.ValueOf(uint32(0xDEADBEEF)))
	fmt.Printf("%x %x\n", w.A, w.B)
	hv.Field(0).SetZero()
	fmt.Printf("%x %x\n", w.A, w.B)
	var w2 words
	reflect.ValueOf(&w2).Elem().Set(reflect.ValueOf(w))
	fmt.Printf("%x %x %v\n", w2.A, w2.B, w2.P == nil)
	lo := reflect.NewAt(reflect.TypeOf(uint32(0)), unsafe.Pointer(&w2.A)).Elem()
	lo.Set(reflect.ValueOf(uint32(7)))
	fmt.Printf("%x\n", w2.A)
	// Four bytes of one word to four of another, as a struct.
	w3 := w
	half := reflect.TypeOf(struct{ X uint32 }{})
	reflect.NewAt(half, unsafe.Pointer(&w3.B)).Elem().Set(reflect.NewAt(half, unsafe.Pointer(&w2.A)).Elem())
	fmt.Printf("%x %x\n", w3.A, w3.B)
	reflect.NewAt(half, unsafe.Pointer(&w3.A)).Elem().SetZero()
	fmt.Printf("%x %x\n", w3.A, w3.B)
}
