// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/goexperiment"
	"internal/race"
	"maps"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A data race on a map is memory safe (doc/gd/maps.md): writers that
// collide panic, and readers racing with a writer, growth, a clear or an
// iteration see only what was stored. The values below say so of
// themselves: a torn string, slice or interface would have a length, an
// array or a type that does not go with the rest.

func mapTearSkip(t *testing.T) {
	t.Helper()
	if race.Enabled {
		t.Skip("the race detector reports the races this test makes")
	}
	if runtime.GOARCH == "wasm" {
		t.Skip("goroutines do not race on wasm")
	}
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs goroutines that run in parallel")
	}
}

var mapTearStrs = func() []string {
	var s []string
	for i, n := range []int{1, 7, 15, 16, 40, 300, 5000} {
		s = append(s, strings.Repeat(string(rune('a'+i)), n))
	}
	return s
}()

func mapTearCheckStr(s string) string {
	if s == "" {
		return ""
	}
	i := int(s[0]) - 'a'
	if i < 0 || i >= len(mapTearStrs) || len(s) != len(mapTearStrs[i]) {
		return "string of length " + itoa(len(s))
	}
	if s != mapTearStrs[i] {
		return "string with the bytes of another"
	}
	return ""
}

type mapTearA struct{ a, b int }
type mapTearB struct{ a, b, c, d, e, f int }

func (x *mapTearA) sum() int { return x.a + x.b }
func (x *mapTearB) sum() int { return x.a + x.b + x.c + x.d + x.e + x.f }

// mapTearIfaces holds values of types stored in an interface differently:
// a pointer, a small value in the interface, a string, a slice.
var mapTearIfaces = []any{
	&mapTearA{1, 2},
	&mapTearB{1, 1, 1, 0, 0, 0},
	7,
	[2]int{3, 4},
	mapTearStrs[1],
	mapTearStrs[6],
}

func mapTearCheckIface(v any) string {
	switch x := v.(type) {
	case nil:
	case *mapTearA:
		if x.sum() != 3 {
			return "*mapTearA with the wrong value"
		}
	case *mapTearB:
		if x.sum() != 3 {
			return "*mapTearB with the wrong value"
		}
	case int:
		if x != 7 {
			return "int with the wrong value"
		}
	case [2]int:
		if x != [2]int{3, 4} {
			return "[2]int with the wrong value"
		}
	case string:
		return mapTearCheckStr(x)
	case sliceTearVal:
		return sliceTearCheck(x)
	default:
		return "interface of an unknown type"
	}
	return ""
}

type mapTearBig [40]int // indirect in a map, as a key and as an element

// mapTearPlain is held in the group: its words are plain data, and one read
// while its slot is reused or cleared may have words of two values.
type mapTearPlain [8]int

func mapTearBigOf(i int) mapTearBig {
	var b mapTearBig
	for j := range b {
		b[j] = 1000 + i
	}
	return b
}

// mapTearRace has one goroutine write maps of keys and vals, from empty
// through growth and splits to a clear, while others read them every way
// there is. check reports what is wrong with a key or element read.
func mapTearRace[K comparable, V any](t *testing.T, keys []K, vals []V, checkK func(K) string, checkV func(V) string) {
	mapTearSkip(t)
	d := 500 * time.Millisecond
	if testing.Short() {
		d = 100 * time.Millisecond
	}
	const readers = 7

	var cur atomic.Pointer[map[K]V]
	first := make(map[K]V)
	cur.Store(&first)
	var stop atomic.Bool
	var bad atomic.Value
	report := func(msg string) { bad.CompareAndSwap(nil, msg) }

	// A write that collides with the fallback of Clone panics, which is
	// what a write that races with a clone may do. The rest of the
	// round goes on.
	write := func(f func()) {
		defer func() {
			if e := recover(); e != nil {
				if err, ok := e.(runtime.Error); !ok || err.Error() != "concurrent map writes" {
					panic(e)
				}
			}
		}()
		f()
	}

	var wg sync.WaitGroup
	for id := 0; id < readers; id++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if e := recover(); e != nil {
					if err, ok := e.(error); ok {
						report("panic: " + err.Error())
					} else {
						report("panic in a reader")
					}
				}
			}()
			checkKV := func(k K, v V) bool {
				// A key read after the iteration returned its slot
				// may be the zero a delete or a clear left there.
				var zeroK K
				msg := ""
				if k != zeroK {
					msg = checkK(k)
				}
				if msg == "" {
					msg = checkV(v)
				}
				if msg != "" {
					report(msg)
					return false
				}
				return true
			}
			for i := id; !stop.Load() && bad.Load() == nil; i++ {
				m := *cur.Load()
				k := keys[(i*31)%len(keys)]
				switch i % 16 {
				case 0, 1, 2, 3, 4, 5:
					if msg := checkV(m[k]); msg != "" {
						report(msg)
					}
				case 6, 7, 8:
					v, ok := m[k]
					if msg := checkV(v); msg != "" {
						report(msg)
					} else if !ok {
						var zero V
						if msg := checkV(zero); msg != "" {
							report(msg)
						}
					}
				case 9:
					if n := len(m); n < 0 || n > len(keys) {
						report("len " + itoa(n))
					}
				case 10, 11:
					n := 0
					for k, v := range m {
						if !checkKV(k, v) {
							break
						}
						if n++; n > 64 {
							break
						}
					}
				case 12:
					rv := reflect.ValueOf(m).MapIndex(reflect.ValueOf(&k).Elem())
					if rv.IsValid() {
						v, _ := rv.Interface().(V) // (nil if V is an interface)
						if msg := checkV(v); msg != "" {
							report(msg)
						}
					}
				case 13:
					it := reflect.ValueOf(m).MapRange()
					for n := 0; n < 16 && it.Next(); n++ {
						k, _ := it.Key().Interface().(K)
						v, _ := it.Value().Interface().(V)
						if !checkKV(k, v) {
							break
						}
					}
				case 14:
					// A clone is of one state of the map: nothing
					// races with it, and it is a map.
					c := maps.Clone(m)
					n := 0
					for k, v := range c {
						if !checkKV(k, v) {
							break
						}
						if _, ok := c[k]; !ok && k == k {
							report("a clone lacks a key it iterates over")
						}
						n++
					}
					if n != len(c) && bad.Load() == nil {
						report("a clone of " + itoa(len(c)) + " entries iterates over " + itoa(n))
					}
				case 15:
					for range m {
						break
					}
				}
			}
		}()
	}

	deadline := time.Now().Add(d)
	for r := 0; time.Now().Before(deadline) && bad.Load() == nil; r++ {
		// Small, then through the growth of a table, then splits.
		size := []int{6, 9, 100, len(keys)}[r%4]
		if size > len(keys) {
			size = len(keys)
		}
		m := make(map[K]V)
		cur.Store(&m)
		for i := 0; i < size; i++ {
			write(func() { m[keys[i]] = vals[i%len(vals)] })
			if i%5 == 4 {
				write(func() { delete(m, keys[i/2]) })
			}
			if i%3 == 0 {
				write(func() { m[keys[i/3]] = vals[(i+1)%len(vals)] })
			}
		}
		for i := 0; i < size; i += 2 {
			write(func() { delete(m, keys[i]) })
		}
		for i := 0; i < size; i += 3 {
			write(func() { m[keys[i]] = vals[i%len(vals)] })
		}
		write(func() { clear(m) })
		for i := 0; i < size/2; i++ {
			write(func() { m[keys[i]] = vals[i%len(vals)] })
		}
	}
	stop.Store(true)
	wg.Wait()
	if msg := bad.Load(); msg != nil {
		t.Fatalf("map read: %s", msg)
	}
}

func TestMapTear(t *testing.T) {
	const nkeys = 3000
	t.Run("int32", func(t *testing.T) {
		keys := make([]int32, nkeys)
		vals := make([]int32, 16)
		for i := range keys {
			keys[i] = int32(i) * 3
		}
		for i := range vals {
			vals[i] = int32(i)*2 + 1
		}
		mapTearRace(t, keys, vals, func(k int32) string {
			if k%3 != 0 || k < 0 || k >= 3*nkeys {
				return "key " + itoa(int(k))
			}
			return ""
		}, func(v int32) string {
			if v != 0 && (v%2 != 1 || v > 31) {
				return "element " + itoa(int(v))
			}
			return ""
		})
	})
	t.Run("int64", func(t *testing.T) {
		keys := make([]int64, nkeys)
		vals := make([]int64, 16)
		for i := range keys {
			keys[i] = int64(i)<<33 + 5
		}
		for i := range vals {
			vals[i] = int64(i)<<33 + 1
		}
		mapTearRace(t, keys, vals, func(k int64) string {
			if k&(1<<33-1) != 5 || k>>33 >= nkeys {
				return "key " + itoa(int(k))
			}
			return ""
		}, func(v int64) string {
			if v != 0 && (v&(1<<33-1) != 1 || v>>33 >= 16) {
				return "element " + itoa(int(v))
			}
			return ""
		})
	})
	t.Run("pointer", func(t *testing.T) {
		keys := make([]*mapTearA, nkeys)
		for i := range keys {
			keys[i] = &mapTearA{i, 3 - i}
		}
		vals := []*mapTearA{{1, 2}, {2, 1}, {0, 3}}
		check := func(p *mapTearA) string {
			if p != nil && p.sum() != 3 {
				return "pointer to the wrong value"
			}
			return ""
		}
		mapTearRace(t, keys, vals, check, check)
	})
	t.Run("string", func(t *testing.T) {
		keys := make([]string, nkeys)
		for i := range keys {
			// Keys of every length that is stored differently,
			// each made of one repeated byte and a number that
			// is checked with it.
			s := mapTearStrs[i%len(mapTearStrs)]
			if len(s) > 40 {
				s = s[:40]
			}
			keys[i] = s + "/" + itoa(i)
		}
		mapTearRace(t, keys, mapTearStrs, func(k string) string {
			a, _, ok := strings.Cut(k, "/")
			if !ok || a == "" || strings.Trim(a, a[:1]) != "" {
				return "key " + k
			}
			return ""
		}, mapTearCheckStr)
	})
	t.Run("iface", func(t *testing.T) {
		keys := make([]any, nkeys)
		for i := range keys {
			switch i % 4 {
			case 0:
				keys[i] = i
			case 1:
				keys[i] = "k" + itoa(i)
			case 2:
				keys[i] = [2]int{i, i}
			case 3:
				keys[i] = float64(i)
			}
		}
		vals := append([]any{sliceTearVals[3]}, mapTearIfaces...)
		mapTearRace(t, keys, vals, func(k any) string {
			switch x := k.(type) {
			case int, float64:
			case string:
				if x == "" || x[0] != 'k' {
					return "key " + x
				}
			case [2]int:
				if x[0] != x[1] {
					return "[2]int key with two values"
				}
			default:
				return "key of an unknown type"
			}
			return ""
		}, mapTearCheckIface)
	})
	t.Run("strings", func(t *testing.T) {
		// A key type compared by a generated function.
		keys := make([][2]string, nkeys)
		for i := range keys {
			keys[i] = [2]string{mapTearStrs[i%len(mapTearStrs)], itoa(i)}
		}
		mapTearRace(t, keys, sliceTearVals, func(k [2]string) string {
			return mapTearCheckStr(k[0])
		}, sliceTearCheck)
	})
	t.Run("struct", func(t *testing.T) {
		// A generated function again, with interfaces and strings.
		type K struct {
			i any
			s string
			n int
		}
		keys := make([]K, nkeys)
		for i := range keys {
			keys[i] = K{mapTearIfaces[i%len(mapTearIfaces)], mapTearStrs[i%len(mapTearStrs)], i}
		}
		mapTearRace(t, keys, mapTearStrs, func(k K) string {
			if msg := mapTearCheckIface(k.i); msg != "" {
				return msg
			}
			return mapTearCheckStr(k.s)
		}, mapTearCheckStr)
	})
	t.Run("plain", func(t *testing.T) {
		keys := make([]mapTearPlain, nkeys)
		for i := range keys {
			for j := range keys[i] {
				keys[i][j] = 1000 + i
			}
		}
		word := func(x int) bool { return x == 0 || x >= 1000 && x < 1000+nkeys }
		mapTearRace(t, keys, keys[:8], func(k mapTearPlain) string {
			for _, x := range k {
				if !word(x) {
					return "key word " + itoa(x)
				}
			}
			return ""
		}, func(v mapTearPlain) string {
			for _, x := range v {
				if !word(x) {
					return "element word " + itoa(x)
				}
			}
			return ""
		})
	})
	t.Run("indirect", func(t *testing.T) {
		keys := make([]mapTearBig, nkeys)
		for i := range keys {
			keys[i] = mapTearBigOf(i)
		}
		vals := make([]mapTearBig, 8)
		for i := range vals {
			vals[i] = mapTearBigOf(i)
		}
		mapTearRace(t, keys, vals, func(k mapTearBig) string {
			if k[0] < 1000 || k[0] >= 1000+nkeys {
				return "key of " + itoa(k[0])
			}
			for _, x := range k {
				if x != k[0] {
					return "key that changed: " + itoa(k[0]) + " and " + itoa(x)
				}
			}
			return ""
		}, func(v mapTearBig) string {
			// Elements are plain words: one read while it is
			// overwritten may have words of two, but each is a
			// word that was stored.
			for _, x := range v {
				if x != 0 && (x < 1000 || x >= 1008) {
					return "element word " + itoa(x)
				}
			}
			return ""
		})
	})
	t.Run("indirect-strings", func(t *testing.T) {
		// Indirect keys that hold strings: an assignment updates the
		// key's memory, which a stale table shares.
		type K struct {
			s string
			n [40]int
		}
		keys := make([]K, nkeys)
		for i := range keys {
			keys[i] = K{s: mapTearStrs[i%len(mapTearStrs)]}
			keys[i].n[0] = i
		}
		vals := []string{mapTearStrs[0], mapTearStrs[3], mapTearStrs[6]}
		mapTearRace(t, keys, vals, func(k K) string {
			return mapTearCheckStr(k.s)
		}, mapTearCheckStr)
	})
}

// TestMapConcurrentWrites checks that writers that collide panic, with an
// error that can be recovered, and leave a map that is a map and is free.
func TestMapConcurrentWrites(t *testing.T) {
	mapTearSkip(t)
	const G = 8
	n := 200000
	if testing.Short() {
		n = 50000
	}
	m := map[int]int{}
	var panics atomic.Int64
	var wrong atomic.Value
	// The writers start together, and write again if none of them met
	// another: on a machine that is slow to start a thread, one may be
	// done before the next begins.
	for round := 0; round < 50 && (round == 0 || panics.Load() == 0 && !goexperiment.Sharing); round++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for id := 0; id < G; id++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				write := func(k int) {
					defer func() {
						switch e := recover().(type) {
						case nil:
						case runtime.Error:
							if e.Error() != "concurrent map writes" {
								wrong.CompareAndSwap(nil, e.Error())
							}
							panics.Add(1)
						default:
							wrong.CompareAndSwap(nil, "a panic that is not a runtime.Error")
						}
					}()
					switch k % 16 {
					case 0:
						delete(m, k/16)
					case 1:
						if k%4096 == 1 {
							clear(m)
						}
					default:
						m[k%2048] = k%2048 + 1
					}
				}
				for i := 0; i < n; i++ {
					write(i*7 + id)
				}
			}()
		}
		close(start)
		wg.Wait()
	}
	if e := wrong.Load(); e != nil {
		t.Fatalf("wrong panic: %v", e)
	}
	if goexperiment.Sharing {
		// The writers of a shared map wait for each other.
		if panics.Load() != 0 {
			t.Errorf("%d writes to a shared map panicked", panics.Load())
		}
	} else if panics.Load() == 0 {
		t.Errorf("%d goroutines wrote one map %d times each and none panicked", G, n)
	}
	t.Logf("%d writes panicked", panics.Load())

	// The map is a map: what it iterates over it can find, and it counts
	// it. An element is what was stored, or zero: a write that lost the
	// slot it was given to a growth.
	count := 0
	for k, v := range m {
		count++
		if v != 0 && v != k+1 {
			t.Fatalf("m[%d] = %d", k, v)
		}
		if _, ok := m[k]; !ok {
			t.Fatalf("key %d is iterated over but not found", k)
		}
	}
	if count != len(m) {
		t.Fatalf("len is %d, iteration found %d", len(m), count)
	}

	// And it is free.
	for k := 0; k < 4096; k++ {
		m[k] = k + 1
	}
	for k := 0; k < 4096; k++ {
		if m[k] != k+1 {
			t.Fatalf("after the race, m[%d] = %d", k, m[k])
		}
	}
	clear(m)
	if len(m) != 0 {
		t.Fatalf("len after clear is %d", len(m))
	}
}

// TestMapHashPanicFree checks that a key whose hash panics leaves the map
// untouched and free, for each way to write a map.
func TestMapHashPanicFree(t *testing.T) {
	bad := any([]int{1}) // not hashable
	for name, write := range map[string]func(map[any]int){
		"assign": func(m map[any]int) { m[bad] = 1 },
		"delete": func(m map[any]int) { delete(m, bad) },
		"reflect": func(m map[any]int) {
			reflect.ValueOf(m).SetMapIndex(reflect.ValueOf(&bad).Elem(), reflect.ValueOf(1))
		},
	} {
		for _, size := range []int{0, 3, 100} {
			m := map[any]int{}
			for i := range size {
				m[i] = i
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Errorf("%s: no panic", name)
					}
				}()
				write(m)
			}()
			if len(m) != size {
				t.Errorf("%s: len %d after the panic, want %d", name, len(m), size)
			}
			m["after"] = 1 // panics if the map was left held
			delete(m, "after")
			clear(m)
		}
	}
}
