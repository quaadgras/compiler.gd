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

type cPair struct {
	n    int
	leaf *cLeaf
	m    [2]int
	next unsafe.Pointer
}

func cKinds(p unsafe.Pointer, words int) string {
	b := make([]byte, words)
	for i := range b {
		b[i] = 'p'
		if runtime.CScalar(unsafe.Add(p, i*8)) {
			b[i] = '.'
		}
	}
	return string(b)
}

// TestCSpanBits follows the words of C's objects through what is stored
// in them.
func TestCSpanBits(t *testing.T) {
	check := func(what string, p unsafe.Pointer, want string) {
		t.Helper()
		if got := cKinds(p, len(want)); got != want {
			t.Errorf("%s: words are %s, want %s", what, got, want)
		}
	}
	raw := runtime.MallocC(64)
	check("malloc", raw, "pppppppp")
	scalar := runtime.MallocCScalar(64)
	check("malloc of no pointers", scalar, "........")
	typed := runtime.MallocCTyped(3*unsafe.Sizeof(cPair{})+8, cPair{})
	check("malloc of a type", typed, ".p..p"+".p..p"+".p..p"+"p")

	// A pointer stored where none was.
	leaf := newLeaf(1)
	runtime.CStorePtr((*unsafe.Pointer)(unsafe.Add(scalar, 16)), unsafe.Pointer(leaf))
	check("a pointer stored", scalar, "..p.....")
	runtime.CStorePtr((*unsafe.Pointer)(unsafe.Add(scalar, 24)), nil)
	// (Go's conversion of the address to a pointer to a pointer is what
	// says here that one may be stored.)
	check("nil stored", scalar, "..pp....")

	// memset, of whole words and of parts of them.
	runtime.CMemset(unsafe.Add(raw, 8), 0, 16)
	check("memset", raw, "p..ppppp")
	runtime.CMemset(unsafe.Add(raw, 28), 1, 22)
	check("memset of parts", raw, "p..p..pp")

	// Copies take the words as they are.
	dst := runtime.MallocC(64)
	runtime.CMemmove(dst, scalar, 64)
	check("a copy", dst, "..pp....")
	if *(**cLeaf)(unsafe.Add(dst, 16)) != leaf {
		t.Errorf("the copy lost its pointer")
	}
	runtime.CMemmove(unsafe.Add(dst, 8), dst, 48) // overlapping
	check("a copy over itself", dst, "...pp...")
	runtime.CMemmove(dst, raw, 64)
	check("a copy of words of both kinds", dst, "p..p..pp")
	runtime.CMemmove(unsafe.Add(dst, 1), raw, 40) // no word arrives whole
	check("a copy to other bytes", dst, "p.....pp")
	runtime.CMemmove(dst, unsafe.Add(raw, 3), 8) // a whole word, of parts of two
	check("a copy from other bytes", dst, "......pp")
	var onStack [4]unsafe.Pointer
	runtime.CMemmove(unsafe.Add(dst, 8), unsafe.Pointer(&onStack), 32)
	check("a copy of a variable", dst, ".pppp.pp")
	cSink = make([]byte, 64)
	runtime.CMemmove(dst, unsafe.Pointer(&cSink[0]), 60)
	check("a copy of Go's bytes", dst, ".......p")

	// Go's own stores of what has pointers: the runtime's, and those
	// of a package that converted the pointer.
	pair := cPair{n: 1, leaf: leaf}
	reflect.NewAt(reflect.TypeFor[cPair](), unsafe.Add(scalar, 16)).Elem().Set(reflect.ValueOf(pair))
	check("a value with pointers stored", scalar, "..pp..p.")
	pairs := make([]cPair, 1)
	copy(unsafe.Slice((*cPair)(dst), 1), pairs)
	check("a slice with pointers copied", dst, ".p..p..p")
	other := runtime.MallocCScalar(64)
	(*cPair)(unsafe.Add(other, 8)).leaf = leaf
	check("a pointer stored by Go", other, "..p..p..")
	runtime.KeepAlive(typed)
}

// TestCSpanFlip keeps Go objects alive from words that held no pointers
// when they were allocated, and frees what only an integer is the
// address of.
func TestCSpanFlip(t *testing.T) {
	const n = 3000
	blocks := make([]unsafe.Pointer, n)
	for i := range blocks {
		// A buffer of bytes that a header is placed in, as an
		// allocator of C's does.
		p := runtime.MallocCScalar(uintptr(64 + i%500))
		runtime.CMemset(p, 0x55, 64)
		runtime.CStorePtr((*unsafe.Pointer)(unsafe.Add(p, 8*(i%8))), unsafe.Pointer(newLeaf(i)))
		blocks[i] = p
		if i%500 == 0 {
			runtime.GC()
		}
	}
	// Copies of them, through memmove and through realloc's way.
	for i, p := range blocks {
		if i%2 == 0 {
			q := runtime.MallocCScalar(64)
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

// TestCSpanRace stores pointers and integers in the same words from
// several goroutines as the collector runs: a word that holds a pointer
// at the end keeps what it points to.
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
				switch (i + g) % 4 {
				case 0, 1:
					runtime.CStorePtr((*unsafe.Pointer)(unsafe.Add(p, w*8)), unsafe.Pointer(newLeaf(w)))
				case 2:
					runtime.CMemset(unsafe.Add(p, w*8), byte(i), uintptr(8*(1+i%3)%((words-w)*8)+8)&^7)
				case 3:
					runtime.CMemmove(buf, p, words*8)
					runtime.CMemmove(unsafe.Add(p, (w/2)*8), unsafe.Add(buf, (w/3)*8), 8*8)
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
		runtime.CStorePtr((*unsafe.Pointer)(unsafe.Add(p, w*8)), unsafe.Pointer(newLeaf(w)))
	}
	cChurn()
	cChurn()
	for w := 0; w < words; w += 2 {
		if runtime.CScalar(unsafe.Add(p, w*8)) {
			t.Fatalf("word %d holds a pointer and is known to hold none", w)
		}
		(*(**cLeaf)(unsafe.Add(p, w*8))).check(t, w)
	}
}
