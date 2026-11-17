package main

// Strings kept in their words (doc/gd/sso-string.md): which strings
// made at run time are, and that making them allocates nothing. With
// 4-byte words (wasm32) a string kept in its words has text where a
// string kept elsewhere has the pointer to its bytes: such a string
// whose first bytes are the address of a live object must stay text,
// and keep nothing alive.

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

// inline reports whether s is kept in its words: the top 4 bits of its
// last byte, the length tag, are not 0.
func inline(s string) bool {
	p := unsafe.Pointer(&s)
	last := *(*byte)(unsafe.Add(p, unsafe.Sizeof(s)-1))
	return last>>4 != 0
}

var sink any
var ssink string

func main() {
	src := []byte(strings.Repeat("abcdefghijklmnopqrstuvwxyz0123456789", 3))
	for _, n := range []int{0, 1, 7, 8, 12, 15, 16, 100} {
		b := src[:n]
		half := string(b[:n/2])
		rest := string(b[n/2:])
		var made [6]string
		allocs := [6]float64{
			testing.AllocsPerRun(100, func() { made[0] = string(b) }),
			testing.AllocsPerRun(100, func() { made[1] = half + rest }),
			testing.AllocsPerRun(100, func() { made[2] = made[0][:n] }),
			testing.AllocsPerRun(100, func() { made[3] = strings.Repeat("x", n) }),
			testing.AllocsPerRun(100, func() { sink = made[0] }),
			testing.AllocsPerRun(100, func() { made[5] = strconv.Itoa(n * 1000) }),
		}
		ok := made[0] == string(b) && made[1] == string(b) && made[2] == string(b) && len(made[3]) == n
		ok = ok && sink.(string) == string(b) && made[5] == strconv.Itoa(n*1000)
		println(n, "inline", inline(made[0]), inline(made[1]), inline(made[3]), "ok", ok)
		// The allocations of string(b), concatenation, slicing,
		// strings.Repeat, any(s) and strconv.Itoa: none up to 15
		// bytes (Repeat and Itoa build in a buffer of their own).
		println(n, "allocs", int(allocs[0]), int(allocs[1]), int(allocs[2]), int(allocs[4]))
	}

	// Map keys made at run time, of each length.
	m := map[string]int{}
	for n := 0; n <= 40; n++ {
		m[string(src[:n])] = n
	}
	good := len(m) == 41
	for n := 0; n <= 40; n++ {
		k := string(src[:n])
		if m[k] != n || m[strings.Clone(k)] != n {
			good = false
		}
	}
	key := string(src[:9])
	println("map", good, int(testing.AllocsPerRun(100, func() { _ = m[string(src[:9])] })), m[key])

	// Bytes of a string kept in its words that are the address of a
	// live object: the string is text, and the object is collected.
	type obj struct {
		a [64]byte
		n int
	}
	var w [16]byte
	collected := false
	func() {
		o := &obj{n: 7}
		runtime.AddCleanup(o, func(*bool) { collected = true }, &collected)
		a := uint64(uintptr(unsafe.Pointer(o)))
		for i := 0; i < 8; i++ {
			w[i] = byte(a >> (8 * i))
		}
		copy(w[8:], "xyzw")
	}()
	s := string(w[:12])
	for i := 0; i < 5 && !collected; i++ {
		runtime.GC()
		runtime.Gosched()
	}
	same := true
	for i := range 12 {
		if s[i] != w[i] {
			same = false
		}
	}
	ssink = s + "!"
	println("address", inline(s), same, len(ssink), ssink[8:], collected)
}
