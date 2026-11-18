// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"reflect"
	"runtime"
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
	if runtime.GOARCH == "wasm" {
		t.Skip("goroutines do not race on wasm")
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
