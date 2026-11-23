// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"fmt"
	"internal/asan"
	"internal/msan"
	"internal/race"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// Litmus tests of the publication point (doc/gd/publication-point.md): a
// new object that a goroutine fills with plain stores and then publishes
// with a racy store must be seen filled. They race on purpose.
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
