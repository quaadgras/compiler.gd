// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"bytes"
	"fmt"
	"internal/asan"
	"internal/msan"
	"internal/race"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// Litmus tests of the publication point (doc/gd/publication-point.md): a
// new object that a goroutine fills with plain stores and then publishes
// with a racy store must be seen filled, and the bytes of a new string or
// slice must never be those of a freed object. They race on purpose.
//
// What they look for depends on the processor's memory order, so a pass
// proves little and a failure proves a lot. GD_PUB_LITMUS=30s runs each
// for that long.

func pubBudget(t *testing.T) time.Duration {
	t.Helper()
	if race.Enabled || msan.Enabled || asan.Enabled {
		t.Skip("races on purpose")
	}
	if runtime.GOARCH == "wasm" || runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two threads")
	}
	if s := os.Getenv("GD_PUB_LITMUS"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if testing.Short() {
		return 100 * time.Millisecond
	}
	return time.Second
}

// pubCounts counts what readers saw, by kind of failure.
type pubCounts struct {
	mu    sync.Mutex
	reads atomic.Int64
	kinds map[string]int64
}

func (c *pubCounts) fail(kind string) {
	c.mu.Lock()
	if c.kinds == nil {
		c.kinds = map[string]int64{}
	}
	c.kinds[kind]++
	c.mu.Unlock()
}

func (c *pubCounts) report(t *testing.T, d time.Duration) {
	t.Helper()
	t.Logf("%d reads in %v", c.reads.Load(), d)
	for k, n := range c.kinds {
		t.Errorf("%s: %d of %d reads", k, n, c.reads.Load())
	}
}

// pubRace runs a writer and a reader goroutine for each of a few pairs
// until the budget is spent. A panic in a reader (a nil function called,
// a nil pointer followed) counts as a failure of that kind.
func pubRace(t *testing.T, write func(pair int, stop *atomic.Bool), read func(pair int, c *pubCounts)) {
	d := pubBudget(t)
	pairs := max(1, min(runtime.GOMAXPROCS(0)/2, 5))
	var stop atomic.Bool
	var c pubCounts
	var wg sync.WaitGroup
	for p := range pairs {
		wg.Add(2)
		go func() {
			defer wg.Done()
			write(p, &stop)
		}()
		go func() {
			defer wg.Done()
			for !stop.Load() {
				func() {
					defer func() {
						if e := recover(); e != nil {
							c.fail(fmt.Sprintf("reader panicked: %v", e))
						}
					}()
					for !stop.Load() {
						for range 1024 {
							read(p, &c)
						}
						c.reads.Add(1024)
					}
				}()
			}
		}()
	}
	time.Sleep(d)
	stop.Store(true)
	wg.Wait()
	c.report(t, d)
}

// The values of the fills. Their strings are longer than an inline
// string, so that a string is an address and a length.
var (
	pubStrs   = [2]string{strings.Repeat("a", 40), strings.Repeat("b", 23)}
	pubBytes  = [2][]byte{make([]byte, 24, 56), make([]byte, 17, 33)}
	pubIfaces = [2]any{new([3]int64), new([5]int64)}
)

// pubString says what is wrong with s, which must be one of pubStrs.
func pubString(s string) string {
	for i := range pubStrs {
		if unsafe.StringData(s) == unsafe.StringData(pubStrs[i]) {
			if len(s) != len(pubStrs[i]) {
				return "string torn (address of one, length of another)"
			}
			return ""
		}
	}
	if len(s) == 0 {
		return "string not filled"
	}
	return "string torn (length with no address)"
}

func pubSlice(b []byte) string {
	for i := range pubBytes {
		if unsafe.SliceData(b) == unsafe.SliceData(pubBytes[i]) {
			if len(b) != len(pubBytes[i]) || cap(b) != cap(pubBytes[i]) {
				return "slice torn"
			}
			return ""
		}
	}
	if cap(b) == 0 {
		return "slice not filled"
	}
	return "slice torn (capacity with no array)"
}

func pubIface(x any) string {
	if x == nil {
		return "interface not filled"
	}
	if x != pubIfaces[0] && x != pubIfaces[1] {
		return "interface torn"
	}
	return ""
}

// Fills: the guarded fields of an object of one or two cache lines, and
// of one with a plain word between them, spread over more than two.
type pubFillSmall struct {
	S string
	B []byte
	I any
}

type pubFillLarge struct {
	S string
	N int
	B []byte
	_ [64]byte
	I any
}

// (Stored here and not returned: a function that returns a new object
// may be given the memory for it by its caller, and then guards its
// stores. So does one that loads a field's value from memory after the
// allocation, which is why the values are arguments.)
//
//go:noinline
func pubStoreSmall(c *pubFillCell, s string, b []byte, x any) {
	c.small = &pubFillSmall{S: s, B: b, I: x}
}

//go:noinline
func pubStoreLarge(c *pubFillCell, s string, b []byte, x any) {
	c.large = &pubFillLarge{S: s, N: len(s), B: b, I: x}
}

type pubFillCell struct {
	small *pubFillSmall
	large *pubFillLarge
	_     [112]byte
}

var pubFills [8]pubFillCell

func pubCheckFill(c *pubCounts, s string, b []byte, x any) {
	for _, e := range [...]string{pubString(s), pubSlice(b), pubIface(x)} {
		if e != "" {
			c.fail(e)
		}
	}
}

func TestPublicationFill(t *testing.T) {
	t.Run("small", func(t *testing.T) {
		pubRace(t, func(p int, stop *atomic.Bool) {
			c := &pubFills[p]
			for i := 0; !stop.Load(); i++ {
				for j := range 256 {
					k := (i + j) & 1
					pubStoreSmall(c, pubStrs[k], pubBytes[k], pubIfaces[k])
				}
			}
		}, func(p int, c *pubCounts) {
			if q := pubFills[p].small; q != nil {
				pubCheckFill(c, q.S, q.B, q.I)
			}
		})
	})
	t.Run("large", func(t *testing.T) {
		pubRace(t, func(p int, stop *atomic.Bool) {
			c := &pubFills[p]
			for i := 0; !stop.Load(); i++ {
				for j := range 256 {
					k := (i + j) & 1
					pubStoreLarge(c, pubStrs[k], pubBytes[k], pubIfaces[k])
				}
			}
		}, func(p int, c *pubCounts) {
			if q := pubFills[p].large; q != nil {
				pubCheckFill(c, q.S, q.B, q.I)
			}
		})
	})
}

type pubRecv struct {
	S string
	B []byte
}

func (r pubRecv) get() (string, []byte) { return r.S, r.B }

//go:noinline
func pubNewClosure(s string, b []byte) func() (string, []byte) {
	return func() (string, []byte) { return s, b }
}

//go:noinline
func pubNewMethodValue(s string, b []byte) func() (string, []byte) {
	return pubRecv{s, b}.get
}

type pubFuncCell struct {
	f func() (string, []byte)
	_ [120]byte
}

var pubFuncs [8]pubFuncCell

func TestPublicationClosure(t *testing.T) {
	pubRace(t, func(p int, stop *atomic.Bool) {
		c := &pubFuncs[p]
		for i := 0; !stop.Load(); i++ {
			for j := range 256 {
				k := (i + j) & 1
				if j&1 == 0 {
					c.f = pubNewClosure(pubStrs[k], pubBytes[k])
				} else {
					c.f = pubNewMethodValue(pubStrs[k], pubBytes[k])
				}
			}
		}
	}, func(p int, c *pubCounts) {
		f := pubFuncs[p].f
		if f == nil {
			return
		}
		s, b := f()
		for _, e := range [...]string{pubString(s), pubSlice(b)} {
			if e != "" {
				c.fail(e)
			}
		}
	})
}

type pubRefCell struct {
	f func(int) byte
	_ [120]byte
}

var (
	pubRefFuncs [8]pubRefCell
	pubRefFault [8]bool
)

// A function value with no string, slice or interface in it must be seen
// whole too. Its code runs on the words after it without checking them: a
// closure indexes what it captured by reference through a pointer it does
// not test for nil, here at an address past the zero page, and a caller
// takes a mask of zero to say that the callee keeps none of its arguments.
func TestPublicationClosureContext(t *testing.T) {
	pubRace(t, func(p int, stop *atomic.Bool) {
		var buf [8192]byte
		buf[5000] = 7
		c := &pubRefFuncs[p]
		for !stop.Load() {
			for range 256 {
				c.f = func(i int) byte { return buf[i&8191] }
			}
		}
		runtime.KeepAlive(&buf)
	}, func(p int, c *pubCounts) {
		if !pubRefFault[p] {
			pubRefFault[p] = true
			debug.SetPanicOnFault(true)
		}
		f := pubRefFuncs[p].f
		if f != nil && f(5000) != 7 {
			c.fail("closure read the wrong byte")
		}
	})
}

type pubMakeFuncCell struct {
	f func(int) int
	_ [120]byte
}

var pubMakeFuncs [8]pubMakeFuncCell

func TestPublicationMakeFunc(t *testing.T) {
	typ := reflect.TypeFor[func(int) int]()
	pubRace(t, func(p int, stop *atomic.Bool) {
		c := &pubMakeFuncs[p]
		for !stop.Load() {
			v := reflect.MakeFunc(typ, func(a []reflect.Value) []reflect.Value {
				return []reflect.Value{reflect.ValueOf(int(a[0].Int()) + 1)}
			})
			c.f = v.Interface().(func(int) int)
		}
	}, func(p int, c *pubCounts) {
		if f := pubMakeFuncs[p].f; f != nil && f(1) != 2 {
			c.fail("MakeFunc returned the wrong value")
		}
	})
}

type pubChanCell struct {
	c chan [512]byte
	_ [120]byte
}

var pubChans [8]pubChanCell

// A new channel's buffer, element type and size are seen before it is.
func TestPublicationChan(t *testing.T) {
	pubRace(t, func(p int, stop *atomic.Bool) {
		c := &pubChans[p]
		for !stop.Load() {
			for range 64 {
				c.c = make(chan [512]byte, 2)
			}
		}
	}, func(p int, c *pubCounts) {
		ch := pubChans[p].c
		if ch == nil {
			return
		}
		var v [512]byte
		select {
		case ch <- v:
		default:
		}
		if cap(ch) != 2 {
			c.fail("channel with the wrong capacity")
		}
	})
}

// pubGrown appends to a slice that starts in the frame (it is moved to
// the heap when it is returned, or by growslice when it outgrows it): the
// elements are copied from the stack to the new array with a plain copy.
//
//go:noinline
func pubGrown(n, k int) []string {
	var out []string
	for range n {
		out = append(out, pubStrs[k])
	}
	return out
}

type pubSliceCell struct {
	s []string
	_ [120]byte
}

var pubSlices [8]pubSliceCell

func TestPublicationGrowFromStack(t *testing.T) {
	pubRace(t, func(p int, stop *atomic.Bool) {
		c := &pubSlices[p]
		for i := 0; !stop.Load(); i++ {
			for j := range 256 {
				// One or two elements are moved from the frame when
				// the slice is returned. A third makes growslice copy
				// the first two.
				c.s = pubGrown(1+j%3, (i+j)&1)
			}
		}
	}, func(p int, c *pubCounts) {
		a := pubSlices[p].s
		for i, s := range a {
			if s == "" && i == 2 {
				// The element append stores after growslice returns,
				// with a guarded store: it may be seen late, whole.
				continue
			}
			if e := pubString(s); e != "" {
				c.fail("element of a grown slice: " + e)
			}
		}
	})
}

// Stale bytes. pubDirty leaves garbage full of a marker in the size
// classes of the values below, which the next allocations of those sizes
// reuse without clearing it. No value contains the marker.

const pubMarker = 0xEE

var pubDirtySink atomic.Pointer[[]byte]

func pubDirty(sizes []int) {
	for _, n := range sizes {
		for range 32 {
			b := make([]byte, n)
			for i := range b {
				b[i] = pubMarker
			}
			pubDirtySink.Store(&b)
		}
	}
}

// pubGC keeps the collector running, so that the garbage is swept and
// reused, until stop is set.
func pubGC(stop *atomic.Bool) {
	for !stop.Load() {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
}

// A cell of the stale-bytes tests. Each form stores in a field of its own.
type pubBytesCell struct {
	str   string // string(b), concatenation, Repeat, Builder
	bs    []byte // []byte(s), bytes.Repeat, append, append in place
	runes []rune // []rune(s)
	_     [64]byte
}

var pubStale [8]pubBytesCell

// pubScan reports whether b holds the marker.
func pubScan(b []byte) bool {
	for _, x := range b {
		if x == pubMarker {
			return true
		}
	}
	return false
}

func pubStaleRead(p int, c *pubCounts) {
	cell := &pubStale[p]
	if s := cell.str; len(s) > 0 && pubScan(unsafe.Slice(unsafe.StringData(s), len(s))) {
		c.fail("stale byte in a new string")
	}
	if b := cell.bs; cap(b) > 0 {
		if pubScan(b) {
			c.fail("stale byte in a new slice")
		} else if pubScan(b[:cap(b)]) {
			c.fail("stale byte in the capacity of a new slice")
		}
	}
	if r := cell.runes; cap(r) > 0 {
		for _, x := range r[:cap(r)] {
			if x&0xff == pubMarker {
				c.fail("stale bytes in a new []rune")
				break
			}
		}
	}
}

func pubStaleTest(t *testing.T, sizes []int, write func(cell *pubBytesCell, n, i int)) {
	var gcStop atomic.Bool
	var gcDone sync.WaitGroup
	gcDone.Add(1)
	go func() {
		defer gcDone.Done()
		pubGC(&gcStop)
	}()
	defer func() {
		gcStop.Store(true)
		gcDone.Wait()
	}()
	pubRace(t, func(p int, stop *atomic.Bool) {
		cell := &pubStale[p]
		for i := 0; !stop.Load(); i++ {
			pubDirty(sizes)
			for j := range 64 {
				write(cell, sizes[j%len(sizes)], i+j)
			}
		}
	}, pubStaleRead)
}

var (
	pubSrcBytes  = bytes.Repeat([]byte{'A'}, 2048)
	pubSrcString = strings.Repeat("A", 2048)
	pubSrcRunes  = []rune(pubSrcString)
	pubSizes     = []int{24, 32, 48, 64, 96, 128, 256, 512, 1024}
)

func TestPublicationStaleBytes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		write func(cell *pubBytesCell, n, i int)
	}{
		{"slicebytetostring", func(cell *pubBytesCell, n, i int) {
			cell.str = string(pubSrcBytes[:n])
		}},
		{"concatstrings", func(cell *pubBytesCell, n, i int) {
			cell.str = pubSrcString[:n/2] + pubSrcString[:n-n/2]
		}},
		{"stringtoslicebyte", func(cell *pubBytesCell, n, i int) {
			cell.bs = []byte(pubSrcString[:n])
		}},
		{"stringtoslicerune", func(cell *pubBytesCell, n, i int) {
			cell.runes = []rune(pubSrcString[:n])
		}},
		{"slicerunetostring", func(cell *pubBytesCell, n, i int) {
			cell.str = string(pubSrcRunes[:n])
		}},
		{"strings.Repeat", func(cell *pubBytesCell, n, i int) {
			cell.str = strings.Repeat("A", n)
		}},
		{"strings.Builder", func(cell *pubBytesCell, n, i int) {
			var b strings.Builder
			b.Grow(n)
			b.WriteString(pubSrcString[:n])
			cell.str = b.String()
		}},
		{"bytes.Repeat", func(cell *pubBytesCell, n, i int) {
			cell.bs = bytes.Repeat([]byte{'A'}, n)
		}},
		{"append", func(cell *pubBytesCell, n, i int) {
			cell.bs = append([]byte(nil), pubSrcBytes[:n]...)
		}},
		{"append in place", func(cell *pubBytesCell, n, i int) {
			// The growth of a slice in memory: its header is what the
			// reader loads.
			cell.bs = nil
			for range n {
				cell.bs = append(cell.bs, 'A')
			}
		}},
	} {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			pubStaleTest(t, pubSizes, tt.write)
		})
	}
}
