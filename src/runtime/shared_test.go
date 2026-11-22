// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/goexperiment"
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

type sharedNode struct {
	next *sharedNode
	m    map[int]int
	s    []*sharedNode
	i    any
	v    int
}

func isShared[T any](p *T) bool { return runtime.IsShared(unsafe.Pointer(p)) }

func wantShared[T any](t *testing.T, what string, p *T, want bool) {
	t.Helper()
	if got := isShared(p); got != want {
		t.Errorf("%s: shared = %v, want %v", what, got, want)
	}
}

func skipIfNotSharing(t *testing.T) {
	if !goexperiment.Sharing {
		t.Skip("goroutine sharing is off (GOEXPERIMENT=sharing turns it on)")
	}
	if runtime.GOARCH == "wasm" {
		t.Skip("goroutines do not race on wasm")
	}
	sum := 0
	if !linkLocal(func() { sum += *linkNew(1).p }) {
		t.Skip("no local objects here")
	}
}

// list returns a heap list of n nodes, the last of which holds a slice of
// another, and an interface holding a third.
func sharedList(n int) (head, inSlice, inIface *sharedNode) {
	inSlice, inIface = runtime.HeapNew[sharedNode](), runtime.HeapNew[sharedNode]()
	for i := 0; i < n; i++ {
		x := runtime.HeapNew[sharedNode]()
		if head == nil {
			x.s = []*sharedNode{nil, inSlice}
			x.i = inIface
		}
		x.next = head
		head = x
	}
	return
}

func TestSharedPublish(t *testing.T) {
	skipIfNotSharing(t)
	var onStack sharedNode
	wantShared(t, "stack variable", &onStack, false)
	wantShared(t, "global", &sharedGlobal, true)
	wantShared[int](t, "nil", nil, false)

	// Deep enough to need the work list to grow.
	head, inSlice, inIface := sharedList(100000)
	other, _, _ := sharedList(3)
	wantShared(t, "new list", head, false)
	runtime.Publish(unsafe.Pointer(&head.v)) // an interior pointer
	n := 0
	for x := head; x != nil; x = x.next {
		if !isShared(x) {
			t.Fatalf("node %d of a published list is not shared", n)
		}
		n++
	}
	wantShared(t, "node in a slice", inSlice, true)
	wantShared(t, "node in an interface", inIface, true)
	wantShared(t, "slice array", &head.s, true)
	wantShared(t, "another list", other, false)

	// The bits are kept by a collection, and not acquired by one.
	runtime.GC()
	runtime.GC()
	wantShared(t, "list after GC", head, true)
	wantShared(t, "last node after GC", inSlice, true)
	wantShared(t, "another list after GC", other, false)
	runtime.KeepAlive(other)
}

var sharedGlobal sharedNode

func TestSharedGo(t *testing.T) {
	skipIfNotSharing(t)
	head, inSlice, _ := sharedList(10)
	local, _, _ := sharedList(2)
	done := make(chan bool)
	go func() {
		head.v++
		done <- true
	}()
	<-done
	wantShared(t, "captured list", head, true)
	wantShared(t, "captured list's tail", inSlice, true)
	wantShared(t, "uncaptured list", local, false)
}

func TestSharedChan(t *testing.T) {
	skipIfNotSharing(t)
	// A channel only this goroutine has does not share what it is sent.
	a, _, _ := sharedList(2)
	b, _, _ := sharedList(2)
	c := make(chan *sharedNode, 4)
	c <- a
	wantShared(t, "sent on a private channel", a, false)
	if x := <-c; x != a {
		t.Fatal("received another value")
	}
	wantShared(t, "received from a private channel", a, false)

	// Until the channel is shared, with what it holds.
	c <- a
	done := make(chan bool)
	go func() {
		<-c
		done <- true
	}()
	<-done
	wantShared(t, "held by a channel when it was shared", a, true)
	wantShared(t, "tail of the same", a.next, true)

	// And from then on what is sent on it is shared.
	c <- b
	wantShared(t, "sent on a shared channel", b, true)
	<-c

	d, _, _ := sharedList(2)
	select {
	case c <- d:
	default:
		t.Fatal("select did not send")
	}
	wantShared(t, "sent by a select", d, true)
}

func TestSharedStores(t *testing.T) {
	skipIfNotSharing(t)
	dst := runtime.HeapNew[sharedNode]()
	private := runtime.HeapNew[sharedNode]()
	runtime.Publish(unsafe.Pointer(dst))

	// sync/atomic.
	a, _, _ := sharedList(2)
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&private.next)), unsafe.Pointer(a))
	wantShared(t, "atomically stored in a private object", a, false)
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&dst.next)), unsafe.Pointer(a))
	wantShared(t, "atomically stored in a shared object", a, true)
	wantShared(t, "tail of the same", a.next, true)
	b, _, _ := sharedList(2)
	atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&dst.next)), unsafe.Pointer(a), unsafe.Pointer(b))
	wantShared(t, "compared and swapped into a shared object", b, true)
	var ap atomic.Pointer[sharedNode]
	c, _, _ := sharedList(2)
	ap.Store(c)
	wantShared(t, "stored in a private atomic.Pointer", c, false)

	// reflect.
	d, _, _ := sharedList(2)
	reflect.ValueOf(private).Elem().Field(0).Addr() // addressable
	reflect.NewAt(reflect.TypeFor[*sharedNode](), unsafe.Pointer(&private.next)).Elem().Set(reflect.ValueOf(d))
	wantShared(t, "set by reflect in a private object", d, false)
	reflect.NewAt(reflect.TypeFor[*sharedNode](), unsafe.Pointer(&dst.next)).Elem().Set(reflect.ValueOf(d))
	wantShared(t, "set by reflect in a shared object", d, true)

	// copy.
	e, _, _ := sharedList(2)
	arr := runtime.HeapNew[[4]*sharedNode]()
	copy(arr[:], []*sharedNode{e})
	wantShared(t, "copied to a private array", e, false)
	runtime.Publish(unsafe.Pointer(arr))
	f, _, _ := sharedList(2)
	copy(arr[:], []*sharedNode{f})
	wantShared(t, "copied to a shared array", f, true)

	// A timer's function runs on another goroutine.
	g, _, _ := sharedList(2)
	tm := time.AfterFunc(time.Hour, func() { g.v++ })
	tm.Stop()
	wantShared(t, "captured by a timer's function", g, true)

	// As a finalizer does.
	h, _, _ := sharedList(2)
	runtime.SetFinalizer(h, func(*sharedNode) {})
	wantShared(t, "with a finalizer", h, true)
}

type sharedPair struct {
	a, b *sharedNode
	n    int
}

var (
	sharedGlobalPtr   *sharedNode
	sharedGlobalSlice []*sharedNode
	sharedGlobalIface any
	sharedGlobalPair  sharedPair
)

//go:noinline
func sharedStoreField(dst *sharedNode, p *sharedNode) { dst.next = p }

//go:noinline
func sharedStorePair(dst *sharedPair, p sharedPair) { *dst = p }

//go:noinline
func sharedStoreElem(dst []*sharedNode, i int, p *sharedNode) { dst[i] = p }

//go:noinline
func sharedStoreIface(dst *sharedNode, v any) { dst.i = v }

// TestSharedCompiledStores checks the stores that compiled code makes.
func TestSharedCompiledStores(t *testing.T) {
	skipIfNotSharing(t)
	new1 := func() *sharedNode { x, _, _ := sharedList(2); return x }
	check := func(what string, x *sharedNode, want bool) {
		t.Helper()
		wantShared(t, what, x, want)
		wantShared(t, what+" (tail)", x.next, want)
	}
	private := runtime.HeapNew[sharedNode]()
	shared := runtime.HeapNew[sharedNode]()
	runtime.Publish(unsafe.Pointer(shared))

	a := new1()
	sharedGlobalPtr = a
	check("stored in a global", a, true)

	b := new1()
	sharedStoreField(private, b)
	check("stored in a private object", b, false)
	sharedStoreField(shared, b)
	check("stored in a shared object", b, true)

	c, c2 := new1(), new1()
	var onStack sharedPair
	sharedStorePair(&onStack, sharedPair{c, c2, 1})
	check("struct stored in the stack", c, false)
	pp := runtime.HeapNew[sharedPair]()
	sharedStorePair(pp, sharedPair{c, c2, 1})
	check("struct stored in a private object", c2, false)
	sharedGlobalPair = *pp
	check("struct stored in a global", c, true)
	check("struct stored in a global, second field", c2, true)

	d := new1()
	arr := runtime.HeapNew[[4]*sharedNode]()
	sharedStoreElem(arr[:], 1, d)
	check("stored in a private array", d, false)
	sharedGlobalSlice = arr[:]
	wantShared(t, "array of a global slice", arr, true)
	check("in the array of a global slice", d, true)
	e := new1()
	sharedStoreElem(sharedGlobalSlice, 2, e)
	check("stored in a shared array", e, true)
	f := new1()
	sharedGlobalSlice = append(sharedGlobalSlice[:3], f)
	check("appended to a shared array", f, true)
	g := new1()
	sharedGlobalSlice = append(sharedGlobalSlice, g) // grows
	check("appended to a global slice", g, true)

	h := new1()
	sharedStoreIface(private, h)
	check("stored in a private interface", h, false)
	sharedStoreIface(shared, h)
	check("stored in a shared interface", h, true)
	i := new1()
	sharedGlobalIface = sharedPair{a: i}
	check("in a struct in a global interface", i, true)

	// A variable a closure of another goroutine captures.
	var captured *sharedNode
	done := make(chan bool)
	go func() {
		_ = captured
		done <- true
	}()
	<-done
	j := new1()
	captured = j
	check("stored in a captured variable", j, true)

	// An object that has been initialized, published, and stored in.
	k := new1()
	fresh := &sharedNode{v: 1}
	sharedGlobalPtr = fresh
	fresh.next = k
	check("stored in an object after it was published", k, true)

	// A map's keys and values.
	l, l2 := new1(), new1()
	m := map[*sharedNode]*sharedNode{}
	m[l] = l2
	check("key of a private map", l, false)
	check("value of a private map", l2, false)
	shared.s = make([]*sharedNode, 1)
	sharedGlobalIface = m
	check("key of a published map", l, true)
	check("value of a published map", l2, true)
	n, n2 := new1(), new1()
	m[n] = n2
	check("key stored in a shared map", n, true)
	check("value stored in a shared map", n2, true)
	for x := 0; x < 1000; x++ {
		m[new1()] = new1()
	}
	for k, v := range m {
		if !isShared(k) || !isShared(v) {
			t.Fatal("key or value of a shared map that grew is not shared")
		}
	}
}

// TestSharedMapRace races goroutines on maps, with no lock of their own.
// A map two goroutines write is a fatal error without the map's lock.
func TestSharedMapRace(t *testing.T) {
	skipIfNotSharing(t)
	const G, N = 8, 20000
	ints := map[int]int{}
	strs := map[string]*sharedNode{}
	big := map[[4]string][20]int{} // indirect key and element
	ifaces := map[any]any{}
	done := make(chan bool)
	for g := 0; g < G; g++ {
		go func() {
			defer func() { done <- true }()
			for i := 0; i < N; i++ {
				k := (i*7 + g) % 512
				switch i % 8 {
				case 0, 1, 2:
					ints[k] = i
					strs[string(rune('a'+k%26))+string(rune('a'+k/26))] = &sharedNode{v: i}
					big[[4]string{"k", string(rune('a' + k%64))}] = [20]int{i}
					ifaces[k%64] = &sharedNode{v: i}
				case 3:
					delete(ints, k)
					delete(strs, string(rune('a'+k%26))+string(rune('a'+k/26)))
					delete(big, [4]string{"k", string(rune('a' + k%64))})
					delete(ifaces, k%64)
				case 4:
					n := 0
					for range ints {
						if n++; n > 16 {
							break
						}
					}
					// An entry may be seen before its value is
					// stored, or after it is deleted: what the
					// lock gives is a map that is a map.
					for _, v := range strs {
						_ = v
						break
					}
				case 5:
					if i%1024 == 5 {
						clear(ints)
					}
					_ = len(big)
				default:
					_ = ints[k]
					_ = strs["aa"]
					_ = big[[4]string{"k", "a"}]
					_ = ifaces[k%64]
				}
			}
		}()
	}
	for g := 0; g < G; g++ {
		<-done
	}
	for _, v := range strs {
		if !isShared(v) {
			t.Fatal("value of a map goroutines share is not shared")
		}
	}
}

type tearT struct {
	s   []int
	str string
	i   any
	st  struct {
		a []byte
		n int
		b string
	}
	arr [3]string
}

type tearA struct{ a, b int }
type tearB struct{ a, b, c, d, e, f int }

func (x *tearA) sum() int { return x.a + x.b }
func (x *tearB) sum() int { return x.a + x.b + x.c + x.d + x.e + x.f }

var tearGlobal tearT

var (
	tearKeys = map[[3]string]int{{"k"}: 0}
	tearChan = make(chan string, 64)
)

// TestSharedTear races goroutines on slices, strings and interfaces of
// different sizes, with no lock of their own. A reader that got the words
// of two values would index past an array, or call one type's method on
// another's value.
func TestSharedTear(t *testing.T) {
	skipIfNotSharing(t)
	const G = 8
	N := 200000
	if testing.Short() {
		N = 40000
	}
	small, big := make([]int, 2), make([]int, 4096)
	for i := range big {
		big[i] = 7
	}
	small[0], small[1] = 7, 7
	shortStr, longStr := "ab", string(make([]byte, 5000))
	p := runtime.HeapNew[tearT]()
	g := &tearGlobal
	done := make(chan bool)
	bad := make(chan string, G)
	for id := 0; id < G; id++ {
		go func() {
			defer func() {
				if e := recover(); e != nil {
					select {
					case bad <- "panic: " + e.(error).Error():
					default:
					}
				}
				done <- true
			}()
			for i := 0; i < N; i++ {
				for _, x := range [2]*tearT{p, g} {
					if (i+id)%2 == 0 {
						// Writers.
						if i%4 < 2 {
							x.s = small
							x.str = shortStr
							x.i = &tearA{1, 2}
							x.st.a, x.st.b = nil, longStr
							x.arr = [3]string{shortStr, longStr, shortStr}
							x.s = append(x.s[:0], 7)
						} else {
							x.s = big
							x.str = longStr
							x.i = &tearB{1, 2, 3, 4, 5, 6}
							x.st = struct {
								a []byte
								n int
								b string
							}{make([]byte, 100), 1, shortStr}
							x.arr = [3]string{longStr, shortStr, longStr}
							if i%64 == 2 {
								clear(x.arr[:])
							}
							s := x.s
							x.s = s[:len(s)/2+1]
						}
						continue
					}
					// Readers.
					if s := x.s; len(s) > 0 && s[len(s)-1] != 7 {
						panic(errorString("slice element"))
					}
					if s := x.s; cap(s) > 0 && s[:cap(s)][cap(s)-1] != 7 && s[:cap(s)][cap(s)-1] != 0 {
						panic(errorString("slice capacity"))
					}
					if s := x.str; len(s) > 0 && s[len(s)-1] != 0 && s[len(s)-1] != 'b' {
						panic(errorString("string byte"))
					}
					if v, ok := x.i.(interface{ sum() int }); ok {
						if n := v.sum(); n != 3 && n != 21 {
							panic(errorString("interface method"))
						}
					}
					st := x.st
					if len(st.a) > 0 && st.a[len(st.a)-1] != 0 {
						panic(errorString("struct slice"))
					}
					if len(st.b) > 0 && st.b[len(st.b)-1] != 0 && st.b[len(st.b)-1] != 'b' {
						panic(errorString("struct string"))
					}
					// The runtime reads these through an address.
					if a, ok := any(x.arr).([3]string); ok {
						for _, s := range a {
							if len(s) > 0 && s[len(s)-1] != 0 && s[len(s)-1] != 'b' {
								panic(errorString("boxed array string"))
							}
						}
					}
					if x.arr == [3]string{"x", "y", "z"} {
						panic(errorString("array equality"))
					}
					if tearKeys[x.arr] != 0 || tearKeys[[3]string{x.str}] != 0 {
						panic(errorString("map key"))
					}
					tearChan <- x.str
					if s := <-tearChan; len(s) > 0 && s[len(s)-1] != 0 && s[len(s)-1] != 'b' {
						panic(errorString("string sent on a channel"))
					}
					arr := x.arr
					for _, s := range arr {
						if len(s) > 0 && s[len(s)-1] != 0 && s[len(s)-1] != 'b' {
							panic(errorString("array string"))
						}
					}
				}
			}
		}()
	}
	for id := 0; id < G; id++ {
		<-done
	}
	select {
	case msg := <-bad:
		t.Fatal(msg)
	default:
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

//go:noinline
func sharedPublishNext(x *sharedNode) { sharedGlobalPtr = x.next }

// TestSharedStatic checks that what the compiler finds unshared is: a
// store it makes without publishing, in memory another goroutine can
// reach, leaves what it stores unshared.
func TestSharedStatic(t *testing.T) {
	skipIfNotSharing(t)
	check := func(what string, x *sharedNode) {
		t.Helper()
		wantShared(t, what, x, true)
	}

	// Two variables with one pointer: a store through one, a load
	// through the other.
	{
		x := &sharedNode{}
		head := x
		y := &sharedNode{}
		head.next = y
		z := x.next
		sharedGlobalPtr = z
		w := &sharedNode{}
		y.next = w
		check("stored in a node published by way of an alias", w)
	}
	// The address of an element.
	{
		cells := make([]sharedNode, 4)
		c := &cells[1]
		a := &sharedNode{}
		c.next = a
		g := cells[1].next
		sharedGlobalPtr = g
		b := &sharedNode{}
		a.next = b
		check("stored in a node published by way of its slice", b)
	}
	// A slice and a slice of it.
	{
		s := make([]*sharedNode, 4)
		u := s[1:3]
		a := &sharedNode{}
		u[0] = a
		sharedGlobalPtr = s[1]
		b := &sharedNode{}
		a.next = b
		check("stored in a node published by way of a slice of its slice", b)
	}
	// An append, which may or may not move the array.
	{
		var s []*sharedNode
		a := &sharedNode{}
		for i := 0; i < 5; i++ {
			s = append(s, a)
		}
		u := s
		sharedGlobalSlice = u
		b := &sharedNode{}
		a.next = b
		check("stored in a node of a published appended slice", b)
		c := &sharedNode{}
		s[0] = c
		check("stored in a published appended slice", c)
	}
	// A callee that publishes what its argument points to.
	{
		x := &sharedNode{}
		a := &sharedNode{}
		x.next = a
		sharedPublishNext(x)
		b := &sharedNode{}
		a.next = b
		check("stored in a node a callee published", b)
	}
	// A loop: the node published was linked two iterations before.
	{
		var head *sharedNode
		var nodes [8]*sharedNode
		for i := range nodes {
			x := &sharedNode{v: i}
			x.next = head
			head = x
			nodes[i] = x
			if i == 5 {
				sharedGlobalPtr = head.next.next
			}
		}
		b := &sharedNode{}
		nodes[1].next = b
		check("stored in a node published from inside a list", b)
	}
	// A goroutine, by way of a closure.
	{
		x := &sharedNode{}
		a := &sharedNode{}
		x.next = a
		done := make(chan bool)
		go func() {
			_ = x.v
			done <- true
		}()
		<-done
		b := &sharedNode{}
		a.next = b
		check("stored in a node a goroutine was given", b)
	}
	// An interface and a struct value on the way.
	{
		x := &sharedNode{}
		a := &sharedNode{}
		x.i = a
		pair := sharedPair{a: x}
		sharedGlobalPair = pair
		b := &sharedNode{}
		a.next = b
		check("stored in a node published inside a struct and an interface", b)
	}
}

// TestSharedBitsFreed checks that memory that held a shared object is
// not shared when it is allocated again.
func TestSharedBitsFreed(t *testing.T) {
	skipIfNotSharing(t)
	for round := 0; round < 6; round++ {
		for i := 0; i < 20000; i++ {
			runtime.Publish(unsafe.Pointer(runtime.HeapNew[sharedNode]()))
			runtime.Publish(unsafe.Pointer(runtime.HeapNew[[3]*int]()))
		}
		if round%2 == 0 {
			runtime.GC()
		}
		stale := 0
		for i := 0; i < 40000; i++ {
			if isShared(runtime.HeapNew[sharedNode]()) {
				stale++
			}
			if isShared(runtime.HeapNew[[3]*int]()) {
				stale++
			}
		}
		if stale != 0 {
			t.Fatalf("round %d: %d new objects are shared", round, stale)
		}
	}
}

// What is allocated as local is in pages that say so, what is not is
// in pages that do not, as the stacks and the globals, and the collector
// frees and reuses both.
func TestLocalRange(t *testing.T) {
	if !runtime.LocalSpans {
		t.Skip("no spans of local objects")
	}
	var onStack int
	if runtime.IsLocal(unsafe.Pointer(&onStack)) {
		t.Errorf("the stack is local")
	}
	if runtime.IsLocal(unsafe.Pointer(&sharedSink)) || runtime.IsLocal(nil) {
		t.Errorf("a global or nil is local")
	}
	// A local object may be in a page that has the place in the table
	// of a page of something else, far from it: then nothing says that
	// it is local. In a heap that is in one piece none is.
	notKnown := 0
	type node struct {
		next *node
		pad  [6]uintptr
	}
	for round := 0; round < 4; round++ {
		var keep []unsafe.Pointer
		var list *node
		for _, words := range []int{1, 2, 3, 8, 100, 1000, 5000, 100000} {
			for i := 0; i < 200; i++ {
				for _, pointers := range []bool{false, true} {
					if pointers && words != 1 {
						continue
					}
					p := runtime.NewLocal(words, pointers)
					if !runtime.IsLocal(p) {
						notKnown++
					}
					if i%16 == 0 {
						keep = append(keep, p)
						*(*uintptr)(p) = uintptr(words)
					}
				}
				n := &node{next: list}
				sharedSink = n
				if runtime.IsLocal(unsafe.Pointer(n)) {
					t.Fatalf("object at %p is local", n)
				}
				if i%8 == 0 {
					list = n
				}
			}
		}
		runtime.GC()
		for _, p := range keep {
			if w := *(*uintptr)(p); w != 1 && w != 2 && w != 3 && w != 8 && w != 100 && w != 1000 && w != 5000 && w != 100000 {
				t.Fatalf("kept local object at %p holds %#x", p, w)
			}
		}
		runtime.KeepAlive(list)
	}
	if notKnown != 0 {
		t.Logf("%d local objects were not known to be", notKnown)
	}
}

var sharedSink any

type sharedAppendBox struct {
	pad   [3]int
	names []string
	trace []any
}

// TestSharedAppend appends in place to slices in shared memory that other
// goroutines replace and load: every slice loaded is one of its arrays
// with a length that array has, and every element is whole. (An append
// that grew once stored the new array before the length, with the locks
// given back in between: a longer slice stored there meanwhile left its
// length with the new, shorter array.)
func TestSharedAppend(t *testing.T) {
	skipIfNotSharing(t)
	strs := []string{"a", "bb", "ccc", "dddd-dddd-dddd-dddd"}
	check := func(x string) {
		if len(x) > 19 || (len(x) > 0 && x[0] < 'a') {
			panic("torn string")
		}
	}
	n := 200000
	if testing.Short() {
		n = 40000
	}
	b := &sharedAppendBox{}
	done := make(chan bool)
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- true }()
			for i := 0; i < n; i++ {
				switch i % 6 {
				case 0:
					b.names = nil
					b.trace = nil
				case 1:
					b.names = append(b.names, strs[i%4])
				case 2:
					b.trace = append(b.trace, strs[i%4], i)
				case 3:
					b.names = append(b.names, strs[i%4], strs[(i+1)%4], strs[(i+2)%4])
				case 4:
					if s := b.names; len(s) > 0 {
						check(s[len(s)-1])
						check(s[0])
					}
				case 5:
					for _, x := range b.trace {
						switch v := x.(type) {
						case string:
							check(v)
						case int, nil:
						default:
							panic("torn interface")
						}
					}
				}
			}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}

var handGlobal *sharedNode

// handSend sends a node it allocates and does not use again: the send
// hands it over.
//
//go:noinline
func handSend(c chan *sharedNode, tail *sharedNode) {
	n := &sharedNode{next: tail, v: 1}
	n.i = "x"
	c <- n
}

// handSendKept sends a node that it also returns.
//
//go:noinline
func handSendKept(c chan *sharedNode) *sharedNode {
	n := &sharedNode{v: 2}
	c <- n
	return n
}

// handSendHeld sends a node that another object points to.
//
//go:noinline
func handSendHeld(c chan *sharedNode, h *sharedNode) {
	n := &sharedNode{v: 3}
	h.next = n
	c <- n
}

// The senders below keep a way to the node they send, each in its own
// manner: their sends do not hand over. They give the node the value 4.

//go:noinline
func handUsedAfter(c chan *sharedNode) {
	n := &sharedNode{v: 4}
	c <- n
	n.m = nil
}

//go:noinline
func handAlias(c chan *sharedNode) {
	n := &sharedNode{v: 4}
	p := n
	c <- n
	p.m = nil
}

//go:noinline
func handInterior(c chan *sharedNode) {
	n := &sharedNode{v: 4}
	p := &n.m
	c <- n
	*p = nil
}

//go:noinline
func handDeferred(c chan *sharedNode) {
	n := &sharedNode{v: 4}
	defer func(n *sharedNode) { n.m = nil }(n)
	c <- n
}

//go:noinline
func handClosure(c chan *sharedNode) func() {
	n := &sharedNode{v: 4}
	f := func() { n.m = nil }
	c <- n
	return f
}

//go:noinline
func handTwice(c chan *sharedNode) {
	n := &sharedNode{v: 4}
	for i := 0; i < 2; i++ {
		c <- n
	}
}

//go:noinline
func handInSlice(c chan *sharedNode) []*sharedNode {
	n := &sharedNode{v: 4}
	l := []*sharedNode{n}
	c <- n
	return l
}

//go:noinline
func handInInterface(c chan *sharedNode) any {
	n := &sharedNode{v: 4}
	var i any = n
	c <- n
	return i
}

// TestSharedHandOver checks that an object whose only pointer is sent on
// a channel is the receiver's alone, and not shared, while what it
// points to is; and that it is shared if the sender keeps a way to it,
// or when the receiver puts it where others reach it.
func TestSharedHandOver(t *testing.T) {
	skipIfNotSharing(t)
	type result struct {
		v            int
		root, tail   bool
		after, store bool
	}
	c := make(chan *sharedNode)
	res := make(chan result)
	shared := new(sharedNode)
	handGlobal = shared
	go func() {
		for n := range c {
			r := result{v: n.v, root: isShared(n), tail: n.next != nil && isShared(n.next)}
			// It is this goroutine's to change.
			for i := 0; i < 100; i++ {
				n.s = append(n.s, nil)
				n.i = i
			}
			if len(n.s) != 100 || n.i != 99 {
				r.v = -1
			}
			r.after = isShared(n)
			handGlobal = n
			r.store = isShared(n)
			res <- r
		}
		// Received straight into memory that is shared.
		shared.next = <-c
		res <- result{root: isShared(shared.next)}
	}()

	handSend(c, new(sharedNode))
	if r := <-res; r.v != 1 || r.root || !r.tail || r.after || !r.store {
		t.Errorf("handed over: %+v, want an unshared node with a shared tail, shared once stored in a global", r)
	}
	kept := handSendKept(c)
	if r := <-res; r.v != 2 || !r.root {
		t.Errorf("sent and returned: %+v, want a shared node", r)
	}
	wantShared(t, "sent and returned", kept, true)
	holder := new(sharedNode)
	handSendHeld(c, holder)
	if r := <-res; r.v != 3 || !r.root {
		t.Errorf("sent and held by another object: %+v, want a shared node", r)
	}
	runtime.KeepAlive(holder)
	for i, f := range []func(){
		func() { handUsedAfter(c) },
		func() { handAlias(c) },
		func() { handInterior(c) },
		func() { handDeferred(c) },
		func() { runtime.KeepAlive(handClosure(c)) },
		func() { handTwice(c) },
		func() { runtime.KeepAlive(handInSlice(c)) },
		func() { runtime.KeepAlive(handInInterface(c)) },
	} {
		go f()
		if r := <-res; r.v != 4 || !r.root {
			t.Errorf("sender %d that keeps a way to its node: %+v, want a shared node", i, r)
		}
		if i == 5 {
			<-res
		}
	}
	close(c)
	if r := <-res; r.root {
		t.Errorf("nil received from a closed channel is shared")
	}
}

var handPool = sync.Pool{New: func() any { return new(sharedNode) }}

//go:noinline
func handPoolMake() *sharedNode { return new(sharedNode) }

// handPoolOther's New does more than allocate: what Get returns is shared.
var handPoolOther = sync.Pool{New: func() any { return handPoolMake() }}

// handPoolCycle gets a node, fills it in and puts it back, without
// keeping a way to it: the put hands it over.
//
//go:noinline
func handPoolCycle(tail *sharedNode) (shared, tailShared bool) {
	n := handPool.Get().(*sharedNode)
	shared = isShared(n)
	tailShared = n.next != nil && isShared(n.next)
	n.next = tail
	n.i = "x"
	n.s = append(n.s[:0], nil)
	handPool.Put(n)
	return
}

// handPoolKeep puts a node back and returns it.
//
//go:noinline
func handPoolKeep() *sharedNode {
	n := handPool.Get().(*sharedNode)
	handPool.Put(n)
	return n
}

// handPoolGet and handPoolFree are the helpers that code which uses a
// pool has: one returns what the pool gave, the other puts it back.
//
//go:noinline
func handPoolGet() *sharedNode {
	n := handPool.Get().(*sharedNode)
	n.v = 0
	return n
}

//go:noinline
func (n *sharedNode) handPoolFree() {
	if n.v < 0 {
		return
	}
	handPool.Put(n)
}

// handPoolHelpers uses the helpers, and keeps no way to the node.
//
//go:noinline
func handPoolHelpers() bool {
	n := handPoolGet()
	shared := isShared(n)
	n.i = "y"
	n.handPoolFree()
	return shared
}

// handPoolDeferred puts the node back with a deferred call.
//
//go:noinline
func handPoolDeferred() bool {
	n := handPoolGet()
	defer n.handPoolFree()
	n.i = "z"
	return isShared(n)
}

// handPoolDeferredKept returns the node that a deferred call puts back.
//
//go:noinline
func handPoolDeferredKept() *sharedNode {
	n := handPoolGet()
	defer n.handPoolFree()
	return n
}

// TestSharedPool checks that an object that is put in a pool by a
// function that keeps no way to it is not shared when the pool gives it
// out again, while what it points to is; that it is shared if the
// function keeps one; and that a pool whose New does not just allocate
// gives out shared objects.
func TestSharedPool(t *testing.T) {
	skipIfNotSharing(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // the pool keeps what it has
	runtime.LockOSThread()                           // and gives it back to this goroutine
	defer runtime.UnlockOSThread()

	tail := new(sharedNode)
	if shared, _ := handPoolCycle(tail); shared {
		t.Errorf("a new object from a pool whose New only allocates is shared")
	}
	wantShared(t, "pointed to by an object handed to a pool", tail, true)
	for i := 0; i < 3; i++ {
		if shared, tailShared := handPoolCycle(tail); shared || !tailShared {
			t.Errorf("round %d: an object handed back to the pool is shared (%v), or what it points to is not (%v)", i, shared, !tailShared)
		}
	}
	for i := 0; i < 3; i++ {
		if handPoolHelpers() {
			t.Errorf("round %d: an object handed back to the pool by a helper is shared", i)
		}
		if handPoolDeferred() {
			t.Errorf("round %d: an object handed back to the pool by a deferred call is shared", i)
		}
	}
	wantShared(t, "put in a pool by a deferred call and returned", handPoolDeferredKept(), true)
	handPool.Get() // not the one that is shared
	kept := handPoolKeep()
	wantShared(t, "put in a pool and kept", kept, true)
	if n := handPool.Get().(*sharedNode); n == kept {
		wantShared(t, "from the pool, after a put that kept it", n, true)
	}
	if n := handPoolOther.Get().(*sharedNode); !isShared(n) {
		t.Errorf("an object from a pool whose New calls a function is not shared")
	}
}

// A keptNode is what a pool of its own gives out; it points to itself,
// as fmt's printer does.
type keptNode struct {
	self *int
	v    int
	id   int
	s    []*int
}

var keptPool = sync.Pool{New: func() any { return new(keptNode) }}

var keptIDs int

// A keptUser is passed a node by a function that puts the node back in
// the pool afterwards.
type keptUser interface {
	use(n *keptNode)
}

type keptReader struct{ sum int }

func (r *keptReader) use(n *keptNode) { r.sum += n.v }

type keptKeeper struct{ n *keptNode }

func (k *keptKeeper) use(n *keptNode) { k.n = n }

//go:noinline
func keptGet() *keptNode {
	n := keptPool.Get().(*keptNode)
	n.self = &n.v
	if n.id == 0 {
		keptIDs++
		n.id = keptIDs
	}
	return n
}

// keptPass passes the node on to u, which is only known when the
// program runs.
//
//go:noinline
func (n *keptNode) keptPass(u keptUser) {
	if u != nil {
		u.use(n)
	}
}

// keptCycle gets a node, lets u use it and puts it back. It reports
// whether the node was shared when the pool gave it, and which node it
// was.
//
//go:noinline
func keptCycle(u keptUser) (shared bool, id int) {
	n := keptGet()
	shared = isShared(n)
	id = n.id
	n.v++
	n.s = append(n.s[:0], nil)
	n.keptPass(u)
	keptPool.Put(n)
	return
}

// TestSharedKeptCheck checks that an object which is passed to a
// function that is only known when the program runs is still handed
// over to its pool if that function does not keep it, though it points
// to itself; and that it is shared, and not given out by the pool
// again, if the function keeps it.
func TestSharedKeptCheck(t *testing.T) {
	skipIfNotSharing(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // the pool keeps what it has
	runtime.LockOSThread()                           // and gives it back to this goroutine
	defer runtime.UnlockOSThread()

	r := new(keptReader)
	_, first := keptCycle(r)
	for i := 0; i < 3; i++ {
		shared, addr := keptCycle(r)
		if shared {
			t.Errorf("round %d: an object passed to a function that does not keep it is shared", i)
		}
		if addr != first {
			t.Errorf("round %d: the pool gave another object", i)
		}
	}
	k := new(keptKeeper)
	if shared, addr := keptCycle(k); shared || addr != first {
		t.Errorf("before it is kept: shared %v, another object %v", shared, addr != first)
	}
	wantShared(t, "kept by a function that is only known when the program runs", k.n, true)
	for i := 0; i < 3; i++ {
		shared, addr := keptCycle(r)
		if shared {
			t.Errorf("round %d after one was kept: the pool gave a shared object", i)
		}
		if addr == first {
			t.Errorf("round %d after one was kept: the pool gave the object that was kept", i)
		}
	}
	runtime.KeepAlive(k)
}

// A recvSink is written to through an interface by a function that does
// not know what it is.
type recvSink interface {
	put(v int) error
}

// recvQuiet's methods keep nothing of their receiver.
type recvQuiet struct {
	vs []int
	i  any
}

func (q *recvQuiet) put(v int) error {
	q.vs = append(q.vs, v)
	return nil
}

// recvLoud's method keeps its receiver.
type recvLoud struct {
	vs []int
}

var recvLoudKept *recvLoud

func (q *recvLoud) put(v int) error {
	q.vs = append(q.vs, v)
	recvLoudKept = q
	return nil
}

var (
	recvQuietPool = sync.Pool{New: func() any { return new(recvQuiet) }}
	recvLoudPool  = sync.Pool{New: func() any { return new(recvLoud) }}
)

// recvFill calls a method of s, and returns what that returns.
//
//go:noinline
func recvFill(s recvSink, n int) error {
	for i := 0; i < n; i++ {
		if err := s.put(i); err != nil {
			return err
		}
	}
	return nil
}

//go:noinline
func recvQuietCycle() (shared bool, err error) {
	q := recvQuietPool.Get().(*recvQuiet)
	shared = isShared(q)
	q.vs = q.vs[:0]
	q.i = "x"
	err = recvFill(q, 3)
	recvQuietPool.Put(q)
	return
}

//go:noinline
func recvLoudCycle() (err error) {
	q := recvLoudPool.Get().(*recvLoud)
	q.vs = q.vs[:0]
	err = recvFill(q, 3)
	recvLoudPool.Put(q)
	return
}

// recvFreer has a method that puts it back in its pool, as pooled types
// have, besides the one of recvSink, which keeps nothing.
type recvFreer struct {
	vs []int
}

var recvFreerPool = sync.Pool{New: func() any { return new(recvFreer) }}

func (q *recvFreer) put(v int) error {
	q.vs = append(q.vs, v)
	return nil
}

//go:noinline
func (q *recvFreer) free() { recvFreerPool.Put(q) }

// recvFillWider calls a method of s, and one that recvSink does not
// have if what s holds has it.
//
//go:noinline
func recvFillWider(s recvSink, n int) {
	s.put(n)
	if f, ok := s.(interface{ free() }); ok && n < 0 {
		f.free()
	}
}

//go:noinline
func recvFreerWiderCycle() (shared bool) {
	q := recvFreerPool.Get().(*recvFreer)
	shared = isShared(q)
	recvFillWider(q, 3)
	q.free()
	return
}

//go:noinline
func recvFreerPlainCycle() (shared bool) {
	q := recvFreerPool.Get().(*recvFreer)
	shared = isShared(q)
	recvFill(q, 3)
	q.free()
	return
}

// TestSharedRecvKeptNothing checks that an object which is passed, as an
// interface, to a function that calls a method of it is handed over to
// its pool if no method of its type keeps the receiver, and is shared
// by the put if one does.
func TestSharedRecvKeptNothing(t *testing.T) {
	skipIfNotSharing(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // the pool keeps what it has
	runtime.LockOSThread()                           // and gives it back to this goroutine
	defer runtime.UnlockOSThread()

	for i := 0; i < 4; i++ {
		if shared, _ := recvQuietCycle(); shared {
			t.Errorf("round %d: an object whose methods keep nothing is shared after it was passed as an interface", i)
		}
	}
	recvLoudCycle()
	wantShared(t, "kept by a method of its own, and put in a pool", recvLoudKept, true)

	// A type with a method that hands it over: only the methods of
	// the interface type count, until it is asserted to have others.
	for i := 0; i < 4; i++ {
		if recvFreerPlainCycle() {
			t.Errorf("round %d: an object whose interface methods keep nothing is shared", i)
		}
	}
	recvFreerWiderCycle()
	if !recvFreerWiderCycle() {
		t.Errorf("an object that a function asserted to have a method that hands it over was handed over")
	}
}

// A keptRelay passes the node on, by a call that checks what the
// function it calls keeps: its escape mask says that it keeps the node,
// but only so (ir.EscMaskHeld).
type keptRelay struct{ to keptUser }

func (r *keptRelay) use(n *keptNode) { n.keptPass(r.to) }

// A heldUser is passed a sink, as an interface, by a function that puts
// the sink back in its pool afterwards.
type heldUser interface {
	fill(s recvSink)
}

// heldCaller only calls the method that recvSink has.
type heldCaller struct{}

func (heldCaller) fill(s recvSink) { s.put(1) }

// heldFiller passes the sink to a function that only does that.
type heldFiller struct{}

func (heldFiller) fill(s recvSink) { recvFill(s, 2) }

// heldKeeper keeps the sink.
type heldKeeper struct{ s recvSink }

func (k *heldKeeper) fill(s recvSink) { k.s = s }

// A heldSink's method keeps nothing of its receiver.
type heldSink struct {
	vs []int
	id int
}

func (q *heldSink) put(v int) error {
	q.vs = append(q.vs, v)
	return nil
}

var (
	heldPool = sync.Pool{New: func() any { return new(heldSink) }}
	heldIDs  int
)

// heldCycle gets a sink, lets u fill it and puts it back. It reports
// whether the sink was shared when the pool gave it, and which it was.
//
//go:noinline
func heldCycle(u heldUser) (shared bool, id int) {
	q := heldPool.Get().(*heldSink)
	shared = isShared(q)
	if q.id == 0 {
		heldIDs++
		q.id = heldIDs
	}
	id = q.id
	q.vs = q.vs[:0]
	u.fill(q)
	heldPool.Put(q)
	return
}

// TestSharedHeldBit checks that an object which is passed to a function
// that is only known when the program runs is handed over to its pool
// if that function keeps it only by a call that checks in turn, or by
// calling the methods that the parameter's interface type has, which
// keep nothing; and that it is shared if the function, or the one at
// the end of such calls, keeps it.
func TestSharedHeldBit(t *testing.T) {
	skipIfNotSharing(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // the pool keeps what it has
	runtime.LockOSThread()                           // and gives it back to this goroutine
	defer runtime.UnlockOSThread()

	relay := &keptRelay{to: new(keptReader)}
	_, first := keptCycle(relay)
	for i := 0; i < 3; i++ {
		shared, id := keptCycle(relay)
		if shared {
			t.Errorf("round %d: an object passed on to a function that does not keep it is shared", i)
		}
		if id != first {
			t.Errorf("round %d: the pool gave another object", i)
		}
	}
	k := new(keptKeeper)
	keptCycle(&keptRelay{to: k})
	wantShared(t, "passed on to a function that keeps it", k.n, true)
	if _, id := keptCycle(relay); id == first {
		t.Errorf("the pool gave the object that was kept")
	}

	_, sink := heldCycle(heldCaller{})
	for _, u := range []heldUser{heldCaller{}, heldFiller{}} {
		for i := 0; i < 3; i++ {
			shared, id := heldCycle(u)
			if shared {
				t.Errorf("%T, round %d: an object of which only methods that keep nothing were called is shared", u, i)
			}
			if id != sink {
				t.Errorf("%T, round %d: the pool gave another object", u, i)
			}
		}
	}
	hk := new(heldKeeper)
	heldCycle(hk)
	wantShared(t, "kept, as an interface, by a function that is only known when the program runs", hk.s.(*heldSink), true)
	if shared, id := heldCycle(heldCaller{}); shared || id == sink {
		t.Errorf("the pool gave the object that was kept")
	}
	runtime.KeepAlive(k)
	runtime.KeepAlive(hk)
}

// Objects that are passed to a function that is only known when the
// program runs, after they are allocated and in another function than
// the one that allocates them: the linker knows every function that it
// may be (cmd/link/internal/ld/sharemask.go), and the object is local
// if none shares it.

type linkObj struct {
	p    *int
	next *linkObj
}

type linkReader interface{ ReadLinkObj(o *linkObj) int }

type linkKeeper interface{ KeepLinkObj(o *linkObj) }

type linkPasser interface{ PassLinkObj(o *linkObj) int }

type linkReads struct{}

func (linkReads) ReadLinkObj(o *linkObj) int { return *o.p }

type linkCounts struct{ n int }

func (c *linkCounts) ReadLinkObj(o *linkObj) int { c.n += *o.p; return c.n }

var linkKept *linkObj

type linkKeeps struct{}

func (linkKeeps) KeepLinkObj(o *linkObj) { linkKept = o }

type linkDrops struct{}

func (linkDrops) KeepLinkObj(o *linkObj) {}

// A method that passes its argument on to another class, and to its own.
type linkPassesOn struct {
	r    linkReader
	k    linkKeeper
	more linkPasser
}

func (p *linkPassesOn) PassLinkObj(o *linkObj) int {
	if p.k != nil {
		p.k.KeepLinkObj(o)
	}
	if p.more != nil {
		return p.more.PassLinkObj(o)
	}
	return p.r.ReadLinkObj(o)
}

type linkPassesRead struct{ r linkReader }

func (p *linkPassesRead) PassLinkObj(o *linkObj) int { return linkRead(p.r, o) }

//go:noinline
func linkRead(r linkReader, o *linkObj) int { return r.ReadLinkObj(o) }

//go:noinline
func linkKeep(k linkKeeper, o *linkObj) { k.KeepLinkObj(o) }

//go:noinline
func linkCall(f func(*linkObj, bool) int, o *linkObj) int { return f(o, true) }

//go:noinline
func linkCallKeep(f func(*linkObj), o *linkObj) { f(o) }

//go:noinline
func linkNew(v int) *linkObj {
	return &linkObj{p: &v}
}

// linkLocal reports whether the object that f makes, with linkNew, is
// local.
func linkLocal(f func()) bool { return runtime.CountLocalAllocs(f) != 0 }

func TestSharedLink(t *testing.T) {
	skipIfNotSharing(t)
	sum := 0
	if !linkLocal(func() { sum += *linkNew(1).p }) {
		t.Skip("no local objects here")
	}

	// Read by a method of an interface: no method with that name
	// shares its argument.
	for _, r := range []linkReader{linkReads{}, new(linkCounts)} {
		if !linkLocal(func() { sum += linkRead(r, linkNew(2)) }) {
			t.Errorf("%T: an object that no method that may be called shares is not local", r)
		}
	}

	// One of the methods with that name keeps it where another
	// goroutine reaches it: none of the objects is local, whichever
	// is called.
	for _, k := range []linkKeeper{linkDrops{}, linkKeeps{}} {
		if linkLocal(func() { linkKeep(k, linkNew(3)) }) {
			t.Errorf("%T: an object that a method that may be called shares is local", k)
		}
	}
	done := make(chan int)
	go func() { done <- *linkKept.p }()
	sum += <-done

	// Function values: read, and kept. (No method has the signature
	// of the first: reflect makes function values of methods.)
	if !linkLocal(func() { sum += linkCall(func(o *linkObj, _ bool) int { return *o.p }, linkNew(4)) }) {
		t.Errorf("an object that no function that may be called shares is not local")
	}
	if linkLocal(func() { linkCallKeep(func(o *linkObj) { linkKept = o }, linkNew(5)) }) {
		t.Errorf("an object that the function that is called shares is local")
	}
	if linkLocal(func() { linkCallKeep(func(o *linkObj) { sum += *o.p }, linkNew(6)) }) {
		t.Errorf("an object that a function that may be called shares is local")
	}

	// Passed on by a method to the methods of another class: local
	// only if nothing at the end shares, and a method with that name
	// passes it on to one that keeps it.
	passer := &linkPassesRead{r: linkReads{}}
	if linkLocal(func() { sum += linkPass(passer, linkNew(7)) }) {
		t.Errorf("an object that a method passes on to one that shares it is local")
	}
	// The same, of a class whose methods only pass it on to readers.
	relayer := &linkRelays{r: linkReads{}}
	if !linkLocal(func() { sum += linkRelay(relayer, linkNew(8)) }) {
		t.Errorf("an object that a method passes on to one that only reads it is not local")
	}

	// Converted to an interface by a function that does not know the
	// type: local if no method with those names, of any type, shares
	// its receiver. The node, the object and its number are three.
	if n := runtime.CountLocalAllocs(func() { sum += linkSum(linkConv(&linkLeaf{o: linkNew(9)})) }); n < 3 {
		t.Errorf("%d local objects, want 3, of a value whose methods keep their receivers", n)
	}
	if n := runtime.CountLocalAllocs(func() {
		sum += linkSum(linkConv(&linkTree{kids: []linkNoder{&linkLeaf{o: linkNew(10)}}}))
	}); n < 3 {
		t.Errorf("%d local objects, want at least 3, of a tree whose methods call each other", n)
	}
	for i := 0; i < 2; i++ {
		var n uint64
		if i == 0 {
			n = runtime.CountLocalAllocs(func() { linkHold(linkConvHolder(&linkHoldsNot{o: linkNew(11)})) })
		} else {
			n = runtime.CountLocalAllocs(func() { linkHold(linkConvHolder(&linkHolds{o: linkNew(12)})) })
		}
		if n != 0 {
			t.Errorf("%d local objects of a value of which a method with that name shares its receiver", n)
		}
	}
	go func() { done <- *linkHeld.o.p }()
	sum += <-done
	if sum == 0 {
		t.Errorf("no sum")
	}
}

type linkNoder interface{ LinkNodeSum() int }

type linkLeaf struct{ o *linkObj }

func (n *linkLeaf) LinkNodeSum() int { return *n.o.p }

type linkTree struct{ kids []linkNoder }

func (n *linkTree) LinkNodeSum() int {
	s := 0
	for _, k := range n.kids {
		s += k.LinkNodeSum()
	}
	return s
}

//go:noinline
func linkConv[T linkNoder](x T) linkNoder { return x }

//go:noinline
func linkSum(n linkNoder) int { return n.LinkNodeSum() }

type linkHolder interface{ LinkHold() }

type linkHoldsNot struct{ o *linkObj }

func (n *linkHoldsNot) LinkHold() {}

type linkHolds struct{ o *linkObj }

var linkHeld *linkHolds

func (n *linkHolds) LinkHold() { linkHeld = n }

//go:noinline
func linkConvHolder[T linkHolder](x T) linkHolder { return x }

//go:noinline
func linkHold(h linkHolder) { h.LinkHold() }

type linkRelayer interface{ RelayLinkObj(o *linkObj) int }

type linkRelays struct {
	r    linkReader
	more linkRelayer
}

func (p *linkRelays) RelayLinkObj(o *linkObj) int {
	if p.more != nil {
		return p.more.RelayLinkObj(o)
	}
	return p.r.ReadLinkObj(o)
}

//go:noinline
func linkRelay(p linkRelayer, o *linkObj) int { return p.RelayLinkObj(o) }

//go:noinline
func linkPass(p linkPasser, o *linkObj) int { return p.PassLinkObj(o) }

var _ linkPasser = (*linkPassesOn)(nil)

func init() {
	// The program has a value of each type.
	var p linkPasser = &linkPassesOn{r: linkReads{}, k: linkDrops{}}
	if p.PassLinkObj(&linkObj{p: new(int)}) != 0 {
		panic("PassLinkObj")
	}
}

// A structure that one function builds and another annotates, storing
// in objects that it reaches from its parameters (deep stores): all of
// it is local if nothing of it is shared, and none of what is stored
// is if something is (cmd/compile/internal/escape, groups.go).

type deepNode struct {
	name *deepIdent
	kids []*deepNode
}

type deepIdent struct{ obj *deepObj }

type deepObj struct{ decl *deepNode }

//go:noinline
func deepLeaf() *deepNode { return &deepNode{name: &deepIdent{}} }

//go:noinline
func deepBuild(n int) *deepNode {
	root := &deepNode{name: &deepIdent{}}
	for i := 0; i < n; i++ {
		root.kids = append(root.kids, deepLeaf())
	}
	return root
}

//go:noinline
func deepDeclare(decl *deepNode, idents ...*deepIdent) {
	for _, id := range idents {
		id.obj = &deepObj{decl: decl}
	}
}

//go:noinline
func deepResolve(root *deepNode) {
	deepDeclare(root, root.name)
	for _, k := range root.kids {
		deepDeclare(k, k.name)
	}
}

var deepSink *deepNode

//go:noinline
func deepCount(root *deepNode) int {
	n := 0
	for _, k := range root.kids {
		if k.name.obj != nil && k.name.obj.decl == k {
			n++
		}
	}
	return n
}

func TestSharedDeep(t *testing.T) {
	skipIfNotSharing(t)
	sum := 0
	if !linkLocal(func() { sum += *linkNew(1).p }) {
		t.Skip("no local objects here")
	}
	// Built and annotated: the root, its name, three leaves with
	// theirs, and the slice of the leaves as it grows. (The objects
	// of the four names are local if the page of each name says that
	// it is, which in a large heap it does not always.)
	n := runtime.CountLocalAllocs(func() {
		root := deepBuild(3)
		deepResolve(root)
		sum += deepCount(root)
	})
	t.Logf("%d local objects in a structure that is built and annotated", n)
	if n < 8 {
		t.Errorf("%d local objects, want at least 8", n)
	}
	// The same, and a leaf is stored where another goroutine reaches
	// it: what is stored in it must not be local.
	n = runtime.CountLocalAllocs(func() {
		root := deepBuild(3)
		deepSink = root.kids[0]
		deepResolve(root)
		sum += deepCount(root)
	})
	t.Logf("%d local objects in a structure of which a part is shared", n)
	if n != 0 {
		t.Errorf("%d local objects in a structure that takes stores and of which a part is shared", n)
	}
	done := make(chan bool)
	go func() { done <- deepSink.name.obj.decl == deepSink }()
	if !<-done {
		t.Errorf("the shared leaf is not annotated")
	}

	// Passed, before it is annotated, to a method of an interface
	// that only reads it, whichever it is: the linker says that no
	// method of that name stores in what its argument reaches.
	for i, r := range []deepReader{new(deepKidCounter), deepNameCounter{}} {
		n = runtime.CountLocalAllocs(func() {
			root := deepBuild(3)
			sum += deepRead(r, root)
			deepResolve(root)
			sum += deepCount(root)
		})
		if n < 8 {
			t.Errorf("reader %d: %d local objects, want at least 8, in a structure that a method only reads", i, n)
		}
	}
	// And to one of which a method with that name stores in it an
	// object that it allocates, as local as what it stores it in:
	// the call passes its bits.
	for i, w := range []deepWriter{deepSkips{}, deepMarks{}} {
		n = runtime.CountLocalAllocs(func() {
			root := deepBuild(3)
			deepWrite(w, root)
			deepResolve(root)
			sum += deepCount(root)
		})
		if n < 8 {
			t.Errorf("writer %d: %d local objects, want at least 8, in a structure that a method stores its own objects in", i, n)
		}
	}
	// And to one of which a method with that name stores in it what
	// nobody knows where it is from, or stores it where another
	// goroutine reaches it.
	for i, w := range []deepSpoiler{deepSpoilsNot{}, deepPlants{}} {
		n = runtime.CountLocalAllocs(func() {
			root := deepBuild(3)
			deepSpoil(w, root)
			deepResolve(root)
			sum += deepCount(root)
		})
		if n != 0 {
			t.Errorf("spoiler %d: %d local objects in a structure that a method may have stored something unknown in", i, n)
		}
	}
	go func() { done <- deepPlanted.decl == nil }()
	<-done
	// A function that returns what it has stored in what its
	// parameter reaches: its caller knows where the result is.
	n = runtime.CountLocalAllocs(func() {
		root := deepBuild(0)
		for range 3 {
			k := deepAdd(root)
			sum += len(k.kids)
		}
		deepResolve(root)
		sum += deepCount(root)
	})
	t.Logf("%d local objects in a structure that a function adds its results to", n)
	if n < 8 {
		t.Errorf("%d local objects, want at least 8", n)
	}
	// And if the result is stored where another goroutine reaches it,
	// it is not local, nor what is stored in it afterwards: the two
	// of the leaf, and the object of its name. (The root, its name,
	// the slice of its leaves and the object of its name are: the
	// leaf does not lead to them.)
	n = runtime.CountLocalAllocs(func() {
		root := deepBuild(0)
		deepSink = deepAdd(root)
		deepResolve(root)
		sum += deepCount(root)
	})
	t.Logf("%d local objects in a structure of which a function's result is shared", n)
	if n > 4 {
		t.Errorf("%d local objects, want at most 4", n)
	}
	go func() { done <- deepSink.name.obj.decl == deepSink }()
	<-done
	if sum == 0 {
		t.Errorf("no sum")
	}
}

type deepReader interface{ ReadDeep(n *deepNode) int }

type deepKidCounter struct{ n int }

func (c *deepKidCounter) ReadDeep(n *deepNode) int { c.n += len(n.kids); return c.n }

type deepNameCounter struct{}

func (deepNameCounter) ReadDeep(n *deepNode) int {
	if n.name != nil {
		return 1
	}
	return 0
}

type deepWriter interface{ WriteDeep(n *deepNode) }

type deepMarks struct{}

func (deepMarks) WriteDeep(n *deepNode) { n.name.obj = &deepObj{decl: n} }

type deepSkips struct{}

func (deepSkips) WriteDeep(n *deepNode) {}

//go:noinline
func deepRead(r deepReader, n *deepNode) int { return r.ReadDeep(n) }

//go:noinline
func deepWrite(w deepWriter, n *deepNode) { w.WriteDeep(n) }

type deepSpoiler interface{ SpoilDeep(n *deepNode) }

type deepSpoilsNot struct{}

func (deepSpoilsNot) SpoilDeep(n *deepNode) {}

// An object that holds a pointer to a shared one, and other things
// beside: the shared one, and what is stored in its place later, must
// not be local.

type typedFile struct {
	mu sync.Mutex
	n  int
}

type typedHolder struct {
	file *typedFile
	buf  *[4]*int
	next *typedHolder
}

// use shares h.file: a mutex is where every goroutine may reach it.
//
//go:noinline
func (h *typedHolder) use() int {
	h.file.mu.Lock()
	h.file.n++
	h.file.mu.Unlock()
	return h.file.n
}

//go:noinline
func (h *typedHolder) setBuf(b *[4]*int) { h.buf = b }

//go:noinline
func (h *typedHolder) setFile(f *typedFile) { h.file = f }

var typedSink *typedFile

func TestSharedTyped(t *testing.T) {
	skipIfNotSharing(t)
	sum := 0
	if !linkLocal(func() { sum += *linkNew(1).p }) {
		t.Skip("no local objects here")
	}
	// The holder and its buffer are local; the file is not.
	n := runtime.CountLocalAllocs(func() {
		h := &typedHolder{file: new(typedFile)}
		h.setBuf(new([4]*int))
		sum += h.use() + len(h.buf)
	})
	t.Logf("%d local objects beside a shared file", n)
	if n < 1 {
		t.Errorf("%d local objects, want the buffer", n)
	}
	if n > 2 {
		// (The holder need not be in the heap.)
		t.Errorf("%d local objects: the file is one, and its mutex is used", n)
	}
	// A file that is stored in the holder later is shared as well:
	// another goroutine reads it.
	n = runtime.CountLocalAllocs(func() {
		h := &typedHolder{file: new(typedFile)}
		f := new(typedFile)
		h.setFile(f)
		sum += h.use()
		typedSink = h.file
	})
	t.Logf("%d local objects with a file stored later", n)
	if n > 1 {
		t.Errorf("%d local objects, want at most the holder", n)
	}
	done := make(chan bool)
	go func() { typedSink.mu.Lock(); typedSink.n++; typedSink.mu.Unlock(); done <- true }()
	<-done
	if sum == 0 {
		t.Errorf("no sum")
	}
}

// What a function returns to a caller that calls it again, or to a
// function that it is called by in turn: where the caller stores it is
// the call's to say, not the function's.

type recNode struct {
	kid, next *recNode
	v         int
}

// recBuild stores what its own call returns in the node that it was
// given, which is its caller's, and not the one that it passes down.
//
//go:noinline
func recBuild(n *recNode, d int) *recNode {
	c := &recNode{v: d}
	if d > 0 {
		n.kid = recBuild(c, d-1)
	}
	return c
}

//go:noinline
func recChain(n *recNode, d int) *recNode {
	if d == 0 {
		return &recNode{v: -1}
	}
	x := recLink(n, d-1)
	n.next = x
	return &recNode{v: d, kid: x}
}

//go:noinline
func recLink(n *recNode, d int) *recNode {
	c := &recNode{v: d}
	c.next = recChain(c, d)
	return c
}

var recSink *recNode

// As a parser does: a function of the batch stores in what another
// returns, passes what it gets back to another one that stores in it,
// and returns what it also keeps in the parser.

type recParser struct {
	note  *recNote
	items []*recItem
}

type recNote struct{ v int }

type recItem struct {
	note *recNote
	sub  *recItem
	v    int
}

//go:noinline
func recItemOf(p *recParser, d int) *recItem {
	it := &recItem{v: d}
	if d > 0 {
		sub := recItemOf(p, d-1)
		sub.note = p.note
		recFill(p, sub, d-1)
		it.sub = sub
	}
	p.items = append(p.items, it)
	return it
}

//go:noinline
func recFill(p *recParser, it *recItem, d int) {
	if d > 1 {
		it.sub = recItemOf(p, d-2)
	}
}

var recParserSink *recParser

func TestSharedRecursiveStores(t *testing.T) {
	skipIfNotSharing(t)
	if !linkLocal(func() { _ = *linkNew(1).p }) {
		t.Skip("no local objects here")
	}
	sum := 0
	n := runtime.CountLocalAllocs(func() {
		p := &recParser{note: &recNote{v: 1}}
		sum += recItemOf(p, 5).v + len(p.items)
	})
	t.Logf("%d local objects under a local parser", n)
	if n < 1 {
		t.Errorf("no local object under a local parser")
	}
	n = runtime.CountLocalAllocs(func() {
		p := &recParser{note: &recNote{v: 1}}
		recParserSink = p
		sum += recItemOf(p, 5).v
	})
	t.Logf("%d local objects under a shared parser", n)
	done := make(chan int)
	go func() {
		k := 0
		for _, it := range recParserSink.items {
			for ; it != nil; it = it.sub {
				if it.note != nil {
					k += it.note.v
				}
			}
		}
		done <- k
	}()
	if k := <-done; k < 3 {
		t.Errorf("%d notes from the shared parser, want at least 3", k)
	}
	if sum == 0 {
		t.Errorf("no sum")
	}
}

// What a bit says: that what the function stores in is local, of what
// the argument reaches. A holder of a shared file is passed with its bit
// to a function that stores only in the holder and its list.

type narrowFile struct {
	mu   sync.Mutex
	name string
}

type narrowItem struct {
	next *narrowItem
	v    int
}

type narrowHolder struct {
	file  *narrowFile
	items []*narrowItem
}

var narrowShared *narrowFile

//go:noinline
func narrowAdd(h *narrowHolder, v int) {
	h.items = append(h.items, &narrowItem{v: v})
}

func TestSharedNarrow(t *testing.T) {
	skipIfNotSharing(t)
	if !linkLocal(func() { _ = *linkNew(1).p }) {
		t.Skip("no local objects here")
	}
	narrowShared = &narrowFile{name: "x"}
	done := make(chan bool)
	go func() {
		narrowShared.mu.Lock()
		narrowShared.mu.Unlock()
		done <- true
	}()
	<-done
	sum := 0
	n := runtime.CountLocalAllocs(func() {
		h := &narrowHolder{file: narrowShared}
		for i := 0; i < 10; i++ {
			narrowAdd(h, i)
		}
		sum += len(h.items)
	})
	t.Logf("%d local objects in a holder of a shared file", n)
	if n < 1 {
		t.Errorf("no local object in a holder of a shared file")
	}
	// The holder is shared: so is what is stored in it.
	var h *narrowHolder
	n = runtime.CountLocalAllocs(func() {
		h = &narrowHolder{file: narrowShared}
		go func() { done <- h.file != nil }()
		<-done
		for i := 0; i < 10; i++ {
			narrowAdd(h, i)
		}
	})
	t.Logf("%d local objects in a shared holder", n)
	if sum != 10 {
		t.Errorf("sum %d", sum)
	}
}

func TestSharedRecursive(t *testing.T) {
	skipIfNotSharing(t)
	sum := 0
	if !linkLocal(func() { sum += *linkNew(1).p }) {
		t.Skip("no local objects here")
	}
	// The root is local: all the nodes may be.
	n := runtime.CountLocalAllocs(func() {
		root := &recNode{}
		sum += recBuild(root, 5).v + recChain(root, 4).v
	})
	// (How many depends on the pages of the nodes that the others
	// are stored in, which in a large heap do not always say that
	// they are local: before, none could be.)
	t.Logf("%d local objects under a local root", n)
	if n < 1 {
		t.Errorf("no local object under a local root")
	}
	// The root is shared: every other node of recBuild is stored in
	// one that the root leads to, and another goroutine walks them.
	n = runtime.CountLocalAllocs(func() {
		root := &recNode{}
		recSink = root
		sum += recBuild(root, 5).v + recChain(root, 4).v
	})
	t.Logf("%d local objects under a shared root", n)
	done := make(chan int)
	go func() {
		k := 0
		for x := recSink.kid; x != nil; x = x.kid {
			k++
		}
		for x := recSink.next; x != nil; x = x.next {
			k++
		}
		done <- k
	}()
	if k := <-done; k < 3 {
		t.Errorf("%d nodes from the shared root, want at least 3", k)
	}
	if sum == 0 {
		t.Errorf("no sum")
	}
}

// deepAdd adds a leaf to root, and returns it.
//
//go:noinline
func deepAdd(root *deepNode) *deepNode {
	k := deepLeaf()
	root.kids = append(root.kids, k)
	return k
}

// deepPlanted is an object that every goroutine reaches.
var deepPlanted = new(deepObj)

type deepPlants struct{}

func (deepPlants) SpoilDeep(n *deepNode) { n.name.obj = deepPlanted }

//go:noinline
func deepSpoil(w deepSpoiler, n *deepNode) { w.SpoilDeep(n) }
