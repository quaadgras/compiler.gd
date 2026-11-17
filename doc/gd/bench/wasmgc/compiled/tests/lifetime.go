// A test of what keeps a block alive with GOWASM=gc,split: values that
// are only reached from one function's locals, used on both sides of
// calls that allocate, collect and let the host free blocks.
package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"
)

type T struct {
	a, b, c, d int
	next       *T
	arr        [8]int
}

var sink [][]int
var bad int

// churn allocates blocks of many sizes filled with a pattern, collects,
// and yields so that the host frees what is unreachable; then allocates
// again, to take what was freed.
//
//go:noinline
func churn() {
	sink = sink[:0]
	runtime.GC()
	time.Sleep(time.Millisecond)
	runtime.GC()
	time.Sleep(time.Millisecond)
	for i := 0; i < 400; i++ {
		s := make([]int, 1+i%40)
		for j := range s {
			s[j] = -1
		}
		sink = append(sink, s)
	}
}

func check(name string, got, want int) {
	if got != want {
		bad++
		fmt.Printf("FAIL %s: got %d, want %d\n", name, got, want)
	}
}

//go:noinline
func mk(n int) *T { return &T{a: n, b: n + 1, c: n + 2, d: n + 3} }

// local: a new value that only this function has.
func local(n int) {
	p := new(T)
	p.a, p.b, p.c, p.d = n, n+1, n+2, n+3
	churn()
	check("local.a", p.a, n)
	p.d = n + 30
	churn()
	check("local.b", p.b, n+1)
	check("local.d", p.d, n+30)
}

// slice: a slice written across calls.
func slice(n int) {
	s := make([]int, 24)
	for i := range s {
		if i%8 == 0 {
			churn()
		}
		s[i] = n + i
	}
	churn()
	for i := range s {
		check("slice", s[i], n+i)
	}
}

// interior: a pointer into the middle of a value, the value itself not
// used again.
func interior(n int) {
	q := &mk(n).arr[3]
	*q = n
	churn()
	check("interior", *q, n)
	*q = n + 1
	churn()
	check("interior2", *q, n+1)
}

// loaded: a pointer read from another value, which then forgets it.
func loaded(n int) {
	h := mk(0)
	h.next = mk(n)
	p := h.next
	h.next = nil
	churn()
	check("loaded.a", p.a, n)
	check("loaded.d", p.d, n+3)
}

// field: the address of a field, as a pointer to an int.
func field(n int) {
	p := &mk(n).c
	churn()
	check("field", *p, n+2)
	*p = 7
	churn()
	check("field2", *p, 7)
}

// unsafeAdd: a pointer moved by unsafe.Add.
func unsafeAdd(n int) {
	p := (*int)(unsafe.Add(unsafe.Pointer(mk(n)), 8))
	churn()
	check("unsafeAdd", *p, n+1)
}

// counts: the case of the bug that was found (counts[f()]++).
//
//go:noinline
func f(i int) int {
	if i%16 == 0 {
		churn()
	}
	return i % 10
}

func counts() {
	c := make([]int, 10)
	for i := 0; i < 200; i++ {
		c[f(i)]++
	}
	for i := range c {
		check("counts", c[i], 20)
	}
}

// str: a string built at run time and a map entry.
func str(n int) {
	s := fmt.Sprint("value-", n)
	b := []byte(s)
	churn()
	check("str", len(s), len(b))
	if string(b) != s || s[:6] != "value-" {
		bad++
		fmt.Println("FAIL str:", s, string(b))
	}
}

func closure(n int) {
	x := n
	g := func() int { x++; return x }
	churn()
	check("closure", g(), n+1)
	churn()
	check("closure2", g(), n+2)
}

func main() {
	rounds := 6
	for r := 0; r < rounds; r++ {
		local(r * 100)
		slice(r * 100)
		interior(r * 100)
		loaded(r * 100)
		field(r * 100)
		unsafeAdd(r * 100)
		counts()
		str(r)
		closure(r)
	}
	// And in goroutines, which park.
	done := make(chan bool)
	for g := 0; g < 4; g++ {
		go func(g int) {
			for r := 0; r < 3; r++ {
				local(g*1000 + r)
				slice(g*1000 + r)
				interior(g*1000 + r)
				loaded(g*1000 + r)
			}
			done <- true
		}(g)
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	if bad != 0 {
		fmt.Println("FAILED:", bad)
		os.Exit(1)
	}
	fmt.Println("ok")
}
