// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"reflect"
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

// cSizes are sizes of every kind of span: small ones of each alignment,
// the largest small one and the one after, and large ones of more than an
// oblet.
var cSizes = []uintptr{0, 1, 7, 8, 15, 16, 17, 23, 24, 31, 32, 100, 511, 512, 513, 4096, 32766, 32767, 32768, 32769, 100000, 1 << 20, 3<<20 + 5}

func TestCSpanSizes(t *testing.T) {
	for _, n := range cSizes {
		for range 3 {
			p := runtime.MallocC(n)
			if p == nil {
				t.Fatalf("MallocC(%d) = nil", n)
			}
			if n >= 16 && uintptr(p)%16 != 0 || uintptr(p)%8 != 0 {
				t.Errorf("MallocC(%d) = %p: not aligned", n, p)
			}
			for i := uintptr(0); i <= n; i += max(n/8, 1) {
				if got, ok := runtime.CUsable(unsafe.Add(p, i)); !ok || got != n-i {
					t.Errorf("CUsable(MallocC(%d)+%d) = %d, %v, want %d", n, i, got, ok, n-i)
				}
			}
			b := unsafe.Slice((*byte)(p), n)
			for i, c := range b {
				if c != 0 {
					t.Fatalf("MallocC(%d): byte %d is %d", n, i, c)
				}
			}
			// What is left there is not there for the next.
			runtime.CMemset(p, 0xa5, n)
			for i, c := range b {
				if c != 0xa5 {
					t.Fatalf("CMemset(%d): byte %d is %d", n, i, c)
				}
			}
		}
		runtime.GC()
	}
	if _, ok := runtime.CUsable(unsafe.Pointer(new(int))); ok {
		t.Errorf("CUsable of a Go object")
	}
	if p := runtime.MallocC(^uintptr(0) - 100); p != nil {
		t.Errorf("MallocC of all memory = %p", p)
	}
}

type cLeaf struct {
	key  int
	pad  [5]int
	self *cLeaf
}

func newLeaf(key int) *cLeaf {
	l := &cLeaf{key: key}
	l.self = l
	return l
}

func (l *cLeaf) check(t *testing.T, key int) {
	t.Helper()
	if l == nil || l.key != key || l.self != l {
		t.Fatalf("leaf %d is gone: %+v", key, l)
	}
}

var cSink []byte

// cChurn makes garbage, so that what is free is used again.
func cChurn() {
	for i := range 2000 {
		cSink = make([]byte, 16+i%900)
		p := runtime.MallocC(uintptr(i % 700))
		if i%700 >= 8 {
			*(*uintptr)(p) = uintptr(i)
		}
	}
	runtime.GC()
}

// TestCSpanPointers keeps Go objects alive from words of C's objects
// only, at every word of objects of every size.
func TestCSpanPointers(t *testing.T) {
	type holder struct {
		p    unsafe.Pointer
		n    uintptr
		keys []int
	}
	var holders []holder
	key := 0
	for _, n := range cSizes {
		if n < 8 {
			continue
		}
		h := holder{p: runtime.MallocC(n), n: n}
		step := max(n/8/50, 1) * 8
		for off := uintptr(0); off+8 <= n; off += step {
			*(**cLeaf)(unsafe.Add(h.p, off)) = newLeaf(key)
			h.keys = append(h.keys, key)
			key++
		}
		// The last whole word too.
		if off := (n - 8) &^ 7; true {
			*(**cLeaf)(unsafe.Add(h.p, off)) = newLeaf(-int(n))
		}
		holders = append(holders, h)
	}
	for range 4 {
		cChurn()
	}
	for _, h := range holders {
		step := max(h.n/8/50, 1) * 8
		i := 0
		last := (h.n - 8) &^ 7
		for off := uintptr(0); off+8 <= h.n; off += step {
			if off != last {
				(*(**cLeaf)(unsafe.Add(h.p, off))).check(t, h.keys[i])
			}
			i++
		}
		(*(**cLeaf)(unsafe.Add(h.p, last))).check(t, -int(h.n))
	}
	runtime.KeepAlive(holders)
}

// TestCSpanChain is a list whose links are in C's objects: each is
// reached from the one before only.
func TestCSpanChain(t *testing.T) {
	type link struct {
		next unsafe.Pointer
		leaf *cLeaf
		n    uintptr
	}
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var head unsafe.Pointer
			const count = 3000
			for i := range count {
				n := unsafe.Sizeof(link{}) + uintptr(i*31%2000)
				if i%500 == 0 {
					n += 70000
				}
				l := (*link)(runtime.MallocC(n))
				l.next, l.leaf, l.n = head, newLeaf(g*count+i), n
				head = unsafe.Pointer(l)
				if i%300 == 0 {
					runtime.GC()
				}
				// And garbage of the same sizes.
				runtime.MallocC(n)
			}
			runtime.GC()
			i := count
			for p := head; p != nil; p = (*link)(p).next {
				i--
				l := (*link)(p)
				l.leaf.check(t, g*count+i)
				if got, _ := runtime.CUsable(p); got != l.n {
					t.Errorf("link %d is %d bytes, was asked for %d", i, got, l.n)
				}
			}
			if i != 0 {
				t.Errorf("%d links are gone", i)
			}
		}()
	}
	wg.Wait()
}

// TestCSpanFree makes much more garbage than the heap may hold.
func TestCSpanFree(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range 20000 {
		p := runtime.MallocC(64 << 10)
		*(*unsafe.Pointer)(p) = runtime.MallocC(uintptr(100 + i%100))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 64<<20 {
		t.Errorf("the heap grew by %d MiB over 1.2 GiB of garbage", grew>>20)
	}
}

// TestCSpanIntegers puts the addresses of objects that are free where
// pointers may be: the collector finds nothing at them.
func TestCSpanIntegers(t *testing.T) {
	const n = 20000
	addrs := make([]uintptr, n)
	for i := range addrs {
		if i%2 == 0 {
			addrs[i] = uintptr(unsafe.Pointer(newLeaf(i))) + uintptr(i%3)*8
		} else {
			addrs[i] = uintptr(runtime.MallocC(uintptr(40+i%300))) + uintptr(i%5)
		}
	}
	runtime.GC()
	runtime.GC()
	raw := runtime.MallocC(n * 8)
	live := newLeaf(-1)
	for round := range 5 {
		for i, a := range addrs {
			*(*uintptr)(unsafe.Add(raw, i*8)) = a
		}
		*(**cLeaf)(unsafe.Add(raw, 8*(round+1))) = live
		// Copies of them, as the collector marks.
		done := make(chan bool)
		go func() {
			for range 200 {
				cp := runtime.MallocC(n * 8)
				runtime.CMemmove(cp, raw, n*8)
				runtime.CMemset(cp, 0, n*4)
			}
			done <- true
		}()
		cChurn()
		<-done
		(*(**cLeaf)(unsafe.Add(raw, 8*(round+1)))).check(t, -1)
	}
	runtime.KeepAlive(raw)
}

// TestCSpanScalars stores integers over the pointers of C's objects as
// the collector marks: what was pointed to is not lost to a goroutine
// that has just loaded it, and the words are claimed for no pointers
// after.
func TestCSpanScalars(t *testing.T) {
	const n = 2000
	raw := runtime.MallocC(n * 8)
	for round := range 20 {
		for i := range n {
			*(**cLeaf)(unsafe.Add(raw, i*8)) = newLeaf(i)
		}
		done := make(chan bool)
		go func() {
			cChurn()
			done <- true
		}()
		for i := range n {
			slot := unsafe.Add(raw, i*8)
			leaf := *(**cLeaf)(slot)
			*(*uintptr)(slot) = uintptr(i)
			if i%64 == 0 {
				runtime.Gosched()
			}
			leaf.check(t, i)
		}
		<-done
		if got := runtime.CClasses(raw, 4); got != "...." {
			t.Fatalf("round %d: words are %s after integers were stored", round, got)
		}
	}
	runtime.KeepAlive(raw)
}

type cPair struct {
	n    int
	leaf *cLeaf
	m    [2]int
	next unsafe.Pointer
}

// The same, under other names.
type cPair2 struct {
	k    uint
	l    *cLeaf2
	f    [2]float64
	link unsafe.Pointer
}

type cLeaf2 struct {
	k    uint
	_    [5]int
	self *cLeaf2
}

// TestCSpanClaims follows the words of C's objects through the
// conversions of pointers into them.
func TestCSpanClaims(t *testing.T) {
	check := func(what string, p unsafe.Pointer, want string) {
		t.Helper()
		if got := runtime.CClasses(p, len(want)); got != want {
			t.Errorf("%s: words are %s, want %s", what, got, want)
		}
	}
	var last [5]uint64
	counts := func(what string, claims, reclaims, narrowed, forged, bytes uint64) {
		t.Helper()
		var now [5]uint64
		now[0], now[1], now[2], now[3], now[4] = runtime.CClaimCounts()
		got := [5]uint64{now[0] - last[0], now[1] - last[1], now[2] - last[2], now[3] - last[3], now[4] - last[4]}
		if want := [5]uint64{claims, reclaims, narrowed, forged, bytes}; got != want {
			t.Errorf("%s: claimed, claimed again, typed, forged, of bytes = %v, want %v", what, got, want)
		}
		last = now
	}
	last[0], last[1], last[2], last[3], last[4] = runtime.CClaimCounts()

	raw := runtime.MallocC(64)
	check("malloc", raw, "--------")
	scalar := runtime.MallocCScalar(64)
	check("malloc of no pointers", scalar, "........")
	typed := runtime.MallocCTyped(3*unsafe.Sizeof(cPair{})+8, cPair{})
	check("malloc of a type", typed, ".p..u"+".p..u"+".p..u"+"-")
	if got := runtime.CClaimType(unsafe.Add(typed, 8)); got != "*runtime_test.cLeaf" {
		t.Errorf("the pointer of a pair is claimed for %q", got)
	}
	check("malloc of bytes", runtime.MallocCTyped(64, [4]byte{}), "--------")
	check("malloc of integers", runtime.MallocCTyped(64, [2]int32{}), "........")
	counts("malloc", 0, 0, 0, 0, 0)

	// The first conversion over a word claims it.
	leaf := newLeaf(1)
	*(**cLeaf)(unsafe.Add(raw, 16)) = leaf
	check("a pointer", raw, "--p-----")
	*(*uint32)(unsafe.Add(raw, 12)) = 5
	check("an integer in half a word", raw, "-.p-----")
	*(*unsafe.Pointer)(unsafe.Add(raw, 24)) = unsafe.Pointer(leaf)
	check("an unsafe.Pointer", raw, "-.pu----")
	(*[64]byte)(raw)[40] = 1
	check("bytes", raw, "-.pu----")
	(*cPair)(unsafe.Add(raw, 24)).n = 3
	check("a struct", raw, "-.p.p..u")
	counts("claims", 7, 1, 0, 0, 0) // the struct's integer is over the unsafe.Pointer

	// What is compatible with the claim changes nothing.
	sink := func(any) {}
	sink(*(**cLeaf)(unsafe.Add(raw, 16)))
	sink((**cLeaf2)(unsafe.Add(raw, 16)))
	sink((*unsafe.Pointer)(unsafe.Add(raw, 16)))
	sink((*float64)(unsafe.Add(raw, 8)))
	sink((*[16]byte)(unsafe.Add(raw, 12)))
	sink(*(*uintptr)(unsafe.Add(raw, 16))) // only loaded
	sink((*cPair2)(unsafe.Add(typed, unsafe.Sizeof(cPair{}))))
	sink((*[2]cPair)(typed))
	check("compatible conversions", raw, "-.p.p..u")
	counts("compatible conversions", 0, 0, 0, 0, 0)

	// What is not claims the word again.
	*(*uintptr)(unsafe.Add(raw, 16)) = 7
	check("an integer over a pointer", raw, "-...p..u")
	counts("an integer over a pointer", 0, 1, 0, 0, 0)
	*(**cLeaf)(unsafe.Add(raw, 16)) = leaf
	check("a pointer over an integer", raw, "-.p.p..u")
	counts("a pointer over an integer", 0, 1, 0, 1, 0)
	sink((**int)(unsafe.Add(raw, 16)))
	counts("a pointer to something else", 0, 1, 0, 0, 0)
	if got := runtime.CClaimType(unsafe.Add(raw, 16)); got != "*int" {
		t.Errorf("the word is claimed for %q", got)
	}
	sink((**cLeaf)(unsafe.Add(typed, 32)))
	check("a typed pointer over an unsafe.Pointer", typed, ".p..p")
	counts("a typed pointer over an unsafe.Pointer", 0, 0, 1, 0, 0)
	written := runtime.MallocC(16)
	(*[8]byte)(written)[0] = 1
	sink((**cLeaf)(written))
	counts("a pointer over bytes that were written", 1, 0, 0, 0, 1)

	// The conversions of C's code claim too, and may be to the end.
	c := runtime.MallocC(40)
	runtime.CConvert(c, cPair{}, 1)
	check("a struct of C's", c, ".p..u")
	runtime.CConvert(unsafe.Add(c, 40), cPair{}, 1)
	runtime.CConvert(unsafe.Add(c, 32), [2]uintptr{}, 1)
	counts("C's conversions", 5, 1, 0, 0, 0)

	// memset claims nothing, and takes no claim away.
	runtime.CMemset(unsafe.Add(raw, 0), 0, 64)
	check("memset", raw, "-.p.p..u")

	// A copy claims what nothing claimed for what it is copied from.
	dst := runtime.MallocC(64)
	*(**cLeaf)(unsafe.Add(raw, 16)) = leaf
	runtime.CMemmove(dst, raw, 64)
	check("a copy", dst, "-.p.p..u")
	if *(**cLeaf)(unsafe.Add(dst, 16)) != leaf {
		t.Errorf("the copy lost its pointer")
	}
	if got := runtime.CClaimType(unsafe.Add(dst, 16)); got != "*runtime_test.cLeaf" {
		t.Errorf("the copy of a pointer is claimed for %q", got)
	}
	runtime.CMemmove(unsafe.Add(dst, 8), dst, 48) // overlapping: the first word gets nothing
	check("a copy over itself", dst, "-.p.p..u")
	dst = runtime.MallocC(64)
	runtime.CMemmove(unsafe.Add(dst, 8), raw, 48)
	check("a copy to other words", dst, "--.p.p.-")
	runtime.CMemmove(unsafe.Add(dst, 1), raw, 40) // no word arrives whole
	check("a copy to other bytes", dst, "--.p.p.-")
	var onStack [4]unsafe.Pointer
	dst = runtime.MallocC(64)
	runtime.CMemmove(unsafe.Add(dst, 8), unsafe.Pointer(&onStack), 32)
	check("a copy of a variable", dst, "--------")
	cSink = make([]byte, 64)
	runtime.CMemmove(dst, unsafe.Pointer(&cSink[0]), 60)
	check("a copy of Go's bytes", dst, ".......-")

	// Go's own stores of what has pointers: the runtime's, and those
	// of a package that converted the pointer.
	pair := cPair{n: 1, leaf: leaf}
	other := runtime.MallocC(64)
	reflect.NewAt(reflect.TypeFor[cPair](), unsafe.Add(other, 16)).Elem().Set(reflect.ValueOf(pair))
	check("a value with pointers stored", other, "--.p..u-")
	pairs := make([]cPair, 1)
	other = runtime.MallocC(64)
	copy(unsafe.Slice((*cPair)(other), 1), pairs)
	check("a slice with pointers copied", other, ".p..u---")
	runtime.KeepAlive(typed)
}

// TestCSpanFlip keeps Go objects alive from words that were allocated
// for no pointers and claimed for one after, and frees what only an
// integer is the address of.
func TestCSpanFlip(t *testing.T) {
	const n = 3000
	blocks := make([]unsafe.Pointer, n)
	for i := range blocks {
		// A buffer that a header is placed in, as an allocator of
		// C's does.
		p := runtime.MallocCScalar(uintptr(64 + i%500))
		runtime.CMemset(p, 0x55, 64)
		*(**cLeaf)(unsafe.Add(p, 8*(i%8))) = newLeaf(i)
		blocks[i] = p
		if i%500 == 0 {
			runtime.GC()
		}
	}
	// Copies of them, as realloc makes.
	for i, p := range blocks {
		if i%2 == 0 {
			q := runtime.MallocC(64)
			runtime.CMemmove(q, p, 64)
			blocks[i] = q
		}
	}
	for range 3 {
		cChurn()
	}
	for i, p := range blocks {
		(*(**cLeaf)(unsafe.Add(p, 8*(i%8)))).check(t, i)
	}

	// An integer is no pointer: what it is the address of is freed.
	freed := make(chan int, n)
	ints := runtime.MallocCScalar(n * 8)
	func() {
		for i := range n {
			l := newLeaf(i)
			runtime.SetFinalizer(l, func(l *cLeaf) { freed <- l.key })
			*(*uintptr)(unsafe.Add(ints, i*8)) = uintptr(unsafe.Pointer(l))
		}
	}()
	for range 5 {
		runtime.GC()
	}
	if len(freed) < n*9/10 {
		t.Errorf("%d of %d objects that only integers were the addresses of are free", len(freed), n)
	}
	runtime.KeepAlive(ints)
}

// TestCSpanRace claims the same words for pointers and for integers
// from several goroutines as the collector runs: a word that holds a
// pointer at the end keeps what it points to.
func TestCSpanRace(t *testing.T) {
	const words = 512
	p := runtime.MallocC(words * 8)
	var wg sync.WaitGroup
	stop := make(chan bool)
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := runtime.MallocC(words * 8)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				w := (i*7 + g*131) % words
				switch (i + g) % 5 {
				case 0, 1:
					*(**cLeaf)(unsafe.Add(p, w*8)) = newLeaf(w)
				case 2:
					runtime.CMemset(unsafe.Add(p, w*8), byte(i), uintptr(8*(1+i%3)%((words-w)*8)+8)&^7)
				case 3:
					runtime.CMemmove(buf, p, words*8)
					runtime.CMemmove(unsafe.Add(p, (w/2)*8), unsafe.Add(buf, (w/3)*8), 8*8)
				case 4:
					*(*uintptr)(unsafe.Add(p, w*8)) = uintptr(i)
				}
			}
		}()
	}
	for range 20 {
		runtime.GC()
	}
	close(stop)
	wg.Wait()
	// Now only pointers, in every other word, and what the others are
	// left as.
	for w := 0; w < words; w += 2 {
		*(**cLeaf)(unsafe.Add(p, w*8)) = newLeaf(w)
	}
	cChurn()
	cChurn()
	for w := 0; w < words; w += 2 {
		if runtime.CScalar(unsafe.Add(p, w*8)) {
			t.Fatalf("word %d holds a pointer and is claimed for none", w)
		}
		(*(**cLeaf)(unsafe.Add(p, w*8))).check(t, w)
	}
}

var cHeaderGlobal [64]byte

var cHeaderSink *[10]int

// TestCSpanHeader checks what the compiled code is told of a pointer
// once, for the checks of the pointers made of it (c23Header).
func TestCSpanHeader(t *testing.T) {
	// An object of C's: bounds as asked for, and the classes of its
	// words.
	p := runtime.MallocC(40)
	d, n, nc, b8, sh, bits := runtime.CHeader(unsafe.Add(p, 8))
	if d != 8 || n != 40 || nc != 41 || sh != 3 {
		t.Errorf("C: d=%d n=%d nc=%d sh=%d, want 8 40 41 3", d, n, nc, sh)
	}
	class := func(off uintptr) uint32 {
		w := (off + b8) >> sh
		return *(*uint32)(unsafe.Pointer(bits + w/16*4)) >> (w % 16 * 2) & 3
	}
	if c := class(16); c != 0 {
		t.Errorf("C: a word of malloc's has class %d", c)
	}
	*(*uintptr)(unsafe.Add(p, 16)) = 1
	*(*unsafe.Pointer)(unsafe.Add(p, 24)) = nil
	if class(16) != 1 || class(24) != 2 || class(8) != 0 || class(32) != 0 {
		t.Errorf("C: the classes do not follow the conversions: %d %d %d %d", class(8), class(16), class(24), class(32))
	}

	// An object of Go's: its slot, and no conversion without a look.
	g := new([10]int)
	cHeaderSink = g
	d, n, nc, _, _, _ = runtime.CHeader(unsafe.Pointer(&g[3]))
	if d != 24 || n+1 < 80 || nc != 0 {
		t.Errorf("Go: d=%d n=%d nc=%d", d, n, nc)
	}

	// The stack and the data: to their ends.
	var local [16]byte
	if d, n, nc, _, _, _ = runtime.CHeader(unsafe.Pointer(&local)); d != 0 || n < 16 || nc == 0 && n >= 4096 {
		t.Errorf("stack: d=%d n=%d nc=%d", d, n, nc)
	}
	if _, n, _, _, _, _ = runtime.CHeader(unsafe.Pointer(&cHeaderGlobal[1])); n < 62 {
		t.Errorf("data: n=%d", n)
	}
	runtime.KeepAlive(p)
	runtime.KeepAlive(g)
}

type cTagHolder struct {
	p    unsafe.Pointer
	next *cTagHolder
}

//go:noinline
func cTagGrow(n int, p unsafe.Pointer) unsafe.Pointer {
	var pad [512]byte
	if n > 0 {
		q := cTagGrow(n-1, p)
		runtime.KeepAlive(&pad)
		return q
	}
	return p
}

// TestCSpanTag keeps objects alive from pointers that have the bit of a
// pointer of C's to a union, and nothing else: in an object of Go's, in
// a word of C's, in a variable of the stack, which moves, and to a
// variable of the stack.
func TestCSpanTag(t *testing.T) {
	const n = 500
	var list *cTagHolder
	raw := runtime.MallocC(n * 8)
	for i := range n {
		list = &cTagHolder{p: runtime.CTag(unsafe.Pointer(newLeaf(i))), next: list}
		*(*unsafe.Pointer)(unsafe.Add(raw, i*8)) = runtime.CTag(unsafe.Pointer(newLeaf(1000 + i)))
	}
	local := runtime.CTag(unsafe.Pointer(newLeaf(-7)))
	var onStack cLeaf
	onStack.key, onStack.self = -9, &onStack
	toStack := runtime.CTag(unsafe.Pointer(&onStack))
	done := make(chan bool)
	go func() {
		// As the collector marks: the barrier sees them too.
		for i := range n {
			*(*unsafe.Pointer)(unsafe.Add(raw, i*8)) = runtime.CTag(unsafe.Pointer(newLeaf(1000 + i)))
			if i%50 == 0 {
				runtime.Gosched()
			}
		}
		done <- true
	}()
	for range 4 {
		cChurn()
	}
	<-done
	cChurn()
	local = cTagGrow(200, local) // the stack moves
	cChurn()
	i := n
	for h := list; h != nil; h = h.next {
		i--
		(*cLeaf)(runtime.CUntag(h.p)).check(t, i)
		(*cLeaf)(runtime.CUntag(*(*unsafe.Pointer)(unsafe.Add(raw, i*8)))).check(t, 1000+i)
	}
	(*cLeaf)(runtime.CUntag(local)).check(t, -7)
	if got := (*cLeaf)(runtime.CUntag(toStack)); got != &onStack || got.key != -9 {
		t.Errorf("a pointer to a variable of the stack did not move with it: %p, %p", got, &onStack)
	}
	runtime.KeepAlive(raw)
}
