// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect_test

import (
	"internal/asan"
	"internal/msan"
	"internal/race"
	"os"
	. "reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A Value is a type, a pointer, an inline payload and flags: one made of
// the words of two stores could say it is a *[1000]int and point to a
// [1]int. So it is loaded and stored whole wherever goroutines may race on
// it, as an interface is (doc/gd/publication-point.md).

func valueRaceBudget(t *testing.T) time.Duration {
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

// valueRaceVals are what the writers store: the type of each says how far
// its pointer may be followed.
var valueRaceBig, valueRaceSmall = new([1000]int), new([1]int)

func valueRaceVals() [3]Value {
	return [3]Value{ValueOf(valueRaceBig), ValueOf(valueRaceSmall), ValueOf(42)}
}

// valueCheck reports what is wrong with v, which must be one of the
// values of valueRaceVals, or zero.
func valueCheck(v Value, bigT Type, bigP, smallP uintptr) string {
	if !v.IsValid() {
		return ""
	}
	typ := v.Type()
	if v.Kind() != typ.Kind() {
		return "kind flag of one value, type of another"
	}
	if typ == bigT && v.Pointer() == smallP {
		return "type of one value, pointer of another"
	}
	if typ != bigT && v.Kind() == Pointer && v.Pointer() == bigP {
		return "type of one value, pointer of another"
	}
	return ""
}

// valueRace has a goroutine store the values through store while others
// check what load returns.
func valueRace(t *testing.T, store func(v Value), load func() Value) {
	d := valueRaceBudget(t)
	vals := valueRaceVals()
	bigT := vals[0].Type()
	bigP, smallP := vals[0].Pointer(), vals[1].Pointer()
	var stop atomic.Bool
	var reads, bad atomic.Int64
	var first atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			for j := range 1024 {
				store(vals[(i+j)%3])
			}
		}
	}()
	for range max(1, min(runtime.GOMAXPROCS(0)-1, 3)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				func() {
					// A torn Value may fault when it is used.
					defer func() {
						if e := recover(); e != nil {
							bad.Add(1)
							first.CompareAndSwap(nil, "panic using a loaded Value")
						}
					}()
					for !stop.Load() {
						for range 1024 {
							if e := valueCheck(load(), bigT, bigP, smallP); e != "" {
								bad.Add(1)
								first.CompareAndSwap(nil, e)
							}
						}
						reads.Add(1024)
					}
				}()
			}
		}()
	}
	time.Sleep(d)
	stop.Store(true)
	wg.Wait()
	t.Logf("%d reads in %v", reads.Load(), d)
	if n := bad.Load(); n != 0 {
		t.Errorf("%d of %d reads were torn: %v", n, reads.Load(), first.Load())
	}
}

// A Value at each offset in a cache line, so that some straddle two.
var valueGlobal struct {
	_ [3]int64
	V Value
}

type valueHolder struct {
	N int
	V Value
	S string
}

func TestValueRaceWhole(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		valueRace(t, func(v Value) { valueGlobal.V = v }, func() Value { return valueGlobal.V })
	})
	t.Run("field", func(t *testing.T) {
		h := new(valueHolder)
		valueRace(t, func(v Value) { h.V = v }, func() Value { return h.V })
	})
	t.Run("struct", func(t *testing.T) {
		h := new(valueHolder)
		valueRace(t, func(v Value) { *h = valueHolder{N: 1, V: v, S: "s"} }, func() Value {
			x := *h
			return x.V
		})
	})
	t.Run("array", func(t *testing.T) {
		a := new([7]Value)
		i := 3
		valueRace(t, func(v Value) { a[i] = v }, func() Value { return a[i] })
	})
	t.Run("slice", func(t *testing.T) {
		s := make([]Value, 9)
		i := 5
		valueRace(t, func(v Value) { s[i] = v }, func() Value { return s[i] })
	})
	t.Run("copy", func(t *testing.T) {
		// typedslicecopy.
		s := make([]Value, 4)
		var src [4]Value
		valueRace(t, func(v Value) {
			for i := range src {
				src[i] = v
			}
			copy(s, src[:])
		}, func() Value {
			var dst [4]Value
			copy(dst[:], s)
			return dst[2]
		})
	})
	t.Run("large struct", func(t *testing.T) {
		// Too large for the compiler to hold in registers.
		type large struct{ A, B, C Value }
		h := new(large)
		valueRace(t, func(v Value) { *h = large{A: v, B: v, C: v} }, func() Value {
			x := *h
			return x.B
		})
	})
	t.Run("clear", func(t *testing.T) {
		s := make([]Value, 4)
		valueRace(t, func(v Value) {
			s[2] = v
			clear(s)
		}, func() Value { return s[2] })
	})
	t.Run("map element", func(t *testing.T) {
		m := map[int]Value{1: {}}
		valueRace(t, func(v Value) { m[1] = v }, func() Value { return m[1] })
	})
	t.Run("map key", func(t *testing.T) {
		m := map[Value]int{}
		var last Value
		valueRace(t, func(v Value) {
			delete(m, last)
			m[v] = 1
			last = v
		}, func() Value {
			for k := range m {
				return k
			}
			return Value{}
		})
	})
	t.Run("chan", func(t *testing.T) {
		c := make(chan Value, 4)
		valueRace(t, func(v Value) {
			select {
			case c <- v:
			default:
			}
		}, func() Value {
			select {
			case v := <-c:
				return v
			default:
				return Value{}
			}
		})
	})
	t.Run("reflect", func(t *testing.T) {
		// A Value held in a variable that reflect sets and reads.
		h := new(Value)
		e := ValueOf(h).Elem()
		valueRace(t, func(v Value) { e.Set(ValueOf(v)) }, func() Value {
			return e.Interface().(Value)
		})
	})
}
