// addresses checks pointers that are computed from others: a slice of
// a slice stored as an object is made, pointers into an array that a
// struct starts with, and what is not equal to itself as a map key.
package main

import (
	"fmt"
	"math"
	"unsafe"
)

type image struct {
	Pix    []uint8
	Stride int
}

//go:noinline
func sub(pix []uint8, i int) *image {
	return &image{Pix: pix[i:], Stride: 10}
}

//go:noinline
func (p *image) sub(i int) *image {
	return &image{Pix: p.Pix[i:], Stride: p.Stride}
}

type digest struct {
	a    [200]byte
	n    int
	rate int
	next *digest
}

//go:noinline
func same(x, y []byte) bool { return &x[0] == &y[0] }

//go:noinline
func overlap(x, y []byte) bool {
	return len(x) > 0 && len(y) > 0 &&
		uintptr(unsafe.Pointer(&x[0])) <= uintptr(unsafe.Pointer(&y[len(y)-1])) &&
		uintptr(unsafe.Pointer(&y[0])) <= uintptr(unsafe.Pointer(&x[len(x)-1]))
}

//go:noinline
func (d *digest) window() []byte { return d.a[d.n:d.rate] }

func main() {
	// The offset of a slice is known after the object it is stored in
	// is made.
	p := make([]uint8, 100)
	p[36] = 0xff
	im := sub(p, 36)
	fmt.Println(im.Pix[0], len(im.Pix), im.sub(1).Pix[0], im.sub(0).Pix[0], sub(im.Pix, 0).sub(0).Pix[0])

	// The first element of an array a struct starts with is at the
	// address of the struct.
	d := &digest{rate: 136}
	d.next = d
	x, y := d.window(), d.window()
	q := []byte("tmp")
	fmt.Println(same(x, y), overlap(x, y), overlap(x, q), overlap(q, x), overlap(x[:3], x[3:]), overlap(x[:4], x[3:]))
	a0, a5 := uintptr(unsafe.Pointer(&x[0])), uintptr(unsafe.Pointer(&x[5]))
	fmt.Println(a5-a0, unsafe.Pointer(d) == unsafe.Pointer(&d.a[0]), unsafe.Pointer(d) == unsafe.Pointer(&d.a), unsafe.Pointer(&d.a[1]) == unsafe.Pointer(d))
	d.n = 3
	x = d.window()
	fmt.Println(uintptr(unsafe.Pointer(&x[0]))-a0, same(x, d.a[3:]), same(x, d.a[:]))

	// A NaN is not equal to itself: each is another key.
	m := map[float64]int{}
	nan := math.NaN()
	for i := 0; i < 100; i++ {
		m[nan] = i
	}
	m[1.5] = 7
	_, ok := m[nan]
	sum := 0
	for k, v := range m {
		if k != k {
			sum += v
		}
	}
	delete(m, nan)
	fmt.Println(len(m), ok, sum, m[1.5])
	mi := map[any]int{}
	mi[nan], mi[float32(nan)] = 1, 2
	mi[nan] = 3
	fmt.Println(len(mi), mi[nan])
}
