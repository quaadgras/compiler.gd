// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

// Slices are loaded and stored whole (doc/gd/slice16.md): a racing load
// gets one store's slice, never the pointer of one and the length or
// capacity of another. Each value of sliceTearVals has a capacity of its
// own and an array whose elements all hold the value's mark, so a torn
// slice (or one that reads beyond its array) shows.

type sliceTearVal = []int64

var (
	sliceTearVals  []sliceTearVal
	sliceTearMarks = map[int]int64{} // capacity -> mark
	sliceTearLens  = map[int]int{}   // capacity -> length
)

// sliceTearHugeArr backs the huge values: their capacity does not fit in
// 32 bits, so they are a header and a marker, and only their length is
// in memory.
var sliceTearHugeArr [64]int64

func init() {
	sliceTearVals = append(sliceTearVals, nil)
	for k := 0; k < 12; k++ {
		c := 3 + 5*k
		l := 1 + k%3 + k
		mark := int64(1000*(k+1) + c)
		a := make([]int64, c)
		for i := range a {
			a[i] = mark
		}
		sliceTearVals = append(sliceTearVals, a[:l:c])
		sliceTearMarks[c] = mark
		sliceTearLens[c] = l
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		const mark = 77777
		for i := range sliceTearHugeArr {
			sliceTearHugeArr[i] = mark
		}
		big := uint64(1) << 32 // (not a constant: int has 32 bits on 386)
		for k := 0; k < 3; k++ {
			c := int(big) + 7*k
			l := 10 + 20*k
			s := unsafe.Slice(&sliceTearHugeArr[0], c)[:l]
			sliceTearVals = append(sliceTearVals, s)
			sliceTearMarks[c] = mark
			sliceTearLens[c] = l
		}
	}
}

// sliceTearCheck reports what is wrong with s, which was loaded from where
// only the values of sliceTearVals are stored.
func sliceTearCheck(s sliceTearVal) string {
	if s == nil {
		return ""
	}
	c := cap(s)
	mark, ok := sliceTearMarks[c]
	if !ok {
		return "capacity " + itoa(c)
	}
	if len(s) != sliceTearLens[c] {
		return "length " + itoa(len(s)) + " with capacity " + itoa(c)
	}
	n := c
	if uint64(c) >= 1<<32-1 {
		n = len(s) // (a huge value's array is not as long as it says)
	}
	for _, x := range s[:n] {
		if x != mark {
			return "elements of a slice of capacity " + itoa(c)
		}
	}
	return ""
}

// sliceTearRace is strTearRace for slices.
func sliceTearRace(t *testing.T, store func(i int, v sliceTearVal), load func(i int) sliceTearVal) {
	if runtime.GOARCH == "wasm" {
		t.Skip("goroutines do not race on wasm")
	}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		t.Skip("with 4-byte words a slice is three words, not guarded (doc/gd/slice16.md)")
	}
	const G = 8
	n := 200000
	if testing.Short() {
		n = 50000
	}
	var wg sync.WaitGroup
	var bad atomic.Value
	for id := 0; id < G; id++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				switch e := recover().(type) {
				case nil:
				case string:
					bad.CompareAndSwap(nil, "panic: "+e)
				case error:
					bad.CompareAndSwap(nil, "panic: "+e.Error())
				default:
					bad.CompareAndSwap(nil, "panic")
				}
			}()
			for i := 0; i < n && bad.Load() == nil; i++ {
				if id%2 == 0 {
					store(i, sliceTearVals[(i*7+id/2)%len(sliceTearVals)])
					continue
				}
				if msg := sliceTearCheck(load(i)); msg != "" {
					bad.CompareAndSwap(nil, msg)
				}
			}
		}()
	}
	wg.Wait()
	if msg := bad.Load(); msg != nil {
		t.Fatalf("torn slice: %s", msg)
	}
}

var sliceTearGlobal sliceTearVal

type sliceTearStruct struct {
	pad [5]int64
	S   sliceTearVal
}

// sliceTearPair is loaded and stored in registers, field by field.
type sliceTearPair struct {
	n int
	s sliceTearVal
}

// sliceTearLarge is copied in memory, not in registers.
type sliceTearLarge struct {
	a [5]int
	s sliceTearVal
	b [5]int
	t sliceTearVal
}

// sliceTearMixed holds an interface, strings and slices: copied under the
// locks of the interface, each slice and string whole.
type sliceTearMixed struct {
	s sliceTearVal
	v any
	t string
	u sliceTearVal
	a [6]int
}

func TestSliceTear(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		sliceTearRace(t, func(_ int, v sliceTearVal) { sliceTearGlobal = v }, func(int) sliceTearVal { return sliceTearGlobal })
	})
	t.Run("field", func(t *testing.T) {
		p := runtime.HeapNew[sliceTearStruct]()
		sliceTearRace(t, func(_ int, v sliceTearVal) { p.S = v }, func(int) sliceTearVal { return p.S })
	})
	t.Run("element", func(t *testing.T) {
		s := make([]sliceTearVal, 3)
		other := []sliceTearVal{nil, sliceTearVals[3], sliceTearVals[len(sliceTearVals)-1]}
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 64 {
			case 0:
				clear(s)
			case 1:
				copy(s, other)
			case 2:
				// growslice reads the elements.
				if s2 := append(s, v); sliceTearCheck(s2[1]) != "" {
					panic("append")
				}
			case 3:
				copy(s[1:], s[:2]) // overlapping, backwards
			case 4:
				copy(s[:2], s[1:]) // overlapping, forwards
			default:
				s[1] = v
			}
		}, func(int) sliceTearVal { return s[1] })
	})
	t.Run("array", func(t *testing.T) {
		p := runtime.HeapNew[[4]sliceTearVal]()
		q := runtime.HeapNew[[4]sliceTearVal]()
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 3 {
			case 0:
				p[i%4] = v
			case 1:
				*q = *p
			case 2:
				*q = [4]sliceTearVal{}
			}
		}, func(i int) sliceTearVal {
			x := *q
			return x[i%4]
		})
	})
	t.Run("map", func(t *testing.T) {
		// A map is not for goroutines that race on it: they lock it.
		// What is stored in it, and what it is loaded in, race.
		var mu sync.Mutex
		m := map[int]sliceTearVal{}
		in, out := runtime.HeapNew[sliceTearStruct](), runtime.HeapNew[sliceTearStruct]()
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 3 {
			case 0:
				in.S = v
			case 1:
				mu.Lock()
				m[i%16] = in.S
				mu.Unlock()
			case 2:
				mu.Lock()
				for _, e := range m {
					out.S = e
					break
				}
				mu.Unlock()
			}
		}, func(i int) sliceTearVal {
			if i%2 == 0 {
				return out.S
			}
			mu.Lock()
			defer mu.Unlock()
			return m[i%16]
		})
	})
	t.Run("reflect", func(t *testing.T) {
		p := runtime.HeapNew[sliceTearStruct]()
		src := runtime.HeapNew[sliceTearStruct]()
		rv := reflect.ValueOf(p).Elem().Field(1)
		rsrc := reflect.ValueOf(src).Elem().Field(1)
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 3 {
			case 0:
				rv.Set(reflect.ValueOf(v))
			case 1:
				src.S = v
			case 2:
				rv.Set(rsrc)
			}
		}, func(i int) sliceTearVal {
			if i%2 == 0 {
				return rv.Interface().(sliceTearVal)
			}
			var x sliceTearVal
			reflect.ValueOf(&x).Elem().Set(rv)
			return x
		})
	})
	t.Run("chan", func(t *testing.T) {
		p := runtime.HeapNew[sliceTearStruct]()
		c := make(chan sliceTearVal, 1)
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 3 {
			case 0:
				p.S = v
			case 1:
				select {
				case c <- p.S: // sent from where it is stored
				default:
				}
			case 2:
				select {
				case p.S = <-c: // received in where it is loaded
				default:
				}
			}
		}, func(i int) sliceTearVal {
			if i%2 == 0 {
				return p.S
			}
			select {
			case v := <-c:
				return v
			default:
				return nil
			}
		})
	})
	t.Run("struct", func(t *testing.T) {
		p, q := runtime.HeapNew[sliceTearPair](), runtime.HeapNew[sliceTearPair]()
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 3 {
			case 0:
				*p = sliceTearPair{i, v}
			case 1:
				*q = *p
			case 2:
				*q = sliceTearPair{}
			}
		}, func(i int) sliceTearVal {
			x := *q
			return x.s
		})
	})
	t.Run("copy", func(t *testing.T) {
		p, q := runtime.HeapNew[sliceTearLarge](), runtime.HeapNew[sliceTearLarge]()
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 4 {
			case 0:
				*p = sliceTearLarge{s: v, t: v}
			case 1:
				*q = *p
			case 2:
				q.t = v
			case 3:
				*q = sliceTearLarge{}
			}
		}, func(i int) sliceTearVal {
			x := *q
			if i%2 == 0 {
				return x.s
			}
			return x.t
		})
	})
	t.Run("mixed", func(t *testing.T) {
		p, q := runtime.HeapNew[sliceTearMixed](), runtime.HeapNew[sliceTearMixed]()
		sliceTearRace(t, func(i int, v sliceTearVal) {
			switch i % 4 {
			case 0:
				*p = sliceTearMixed{s: v, v: v, t: "abc", u: v}
			case 1:
				*q = *p
			case 2:
				q.u = v
			case 3:
				*q = sliceTearMixed{}
			}
		}, func(i int) sliceTearVal {
			x := *q
			switch i % 3 {
			case 0:
				return x.s
			case 1:
				if x.v == nil {
					return nil
				}
				return x.v.(sliceTearVal)
			}
			return x.u
		})
	})
	t.Run("interface", func(t *testing.T) {
		p := runtime.HeapNew[struct{ V any }]()
		sliceTearRace(t, func(_ int, v sliceTearVal) { p.V = v }, func(int) sliceTearVal {
			x, _ := p.V.(sliceTearVal)
			return x
		})
	})
}
