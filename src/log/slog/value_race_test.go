// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"fmt"
	"internal/asan"
	"internal/msan"
	"internal/race"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A Value is one interface, which is loaded and stored whole: a racing
// reader sees the group or the time of one store, never the pointer of
// one and the length or the nanoseconds of another
// (doc/gd/publication-point.md).

var valueRaceSlot Value

//go:noinline
func valueRaceStore(v Value) { valueRaceSlot = v }

//go:noinline
func valueRaceLoad() Value { return valueRaceSlot }

func valueRaceGroup(n int) Value {
	as := make([]Attr, n)
	for i := range as {
		as[i] = Int("n", n)
	}
	return GroupValue(as...)
}

// valueRaceCheck reports what is wrong with v, one of the values stored
// by TestValueRaceWhole. It reads only the first Attr of a group, which
// every group has, so a torn length is seen and not followed.
func valueRaceCheck(v Value) string {
	switch v.Kind() {
	case KindGroup:
		g := v.Group()
		if n := g[0].Value.Int64(); int64(len(g)) != n {
			return fmt.Sprintf("group of %d attrs with length %d", n, len(g))
		}
	case KindTime:
		tm := v.Time()
		_, off := tm.Zone()
		if want := int(tm.Unix()-1) * 3600; off != want {
			return fmt.Sprintf("time %d in zone %+d, want %+d", tm.Unix(), off, want)
		}
	}
	return ""
}

func TestValueRaceWhole(t *testing.T) {
	if race.Enabled || msan.Enabled || asan.Enabled {
		t.Skip("races on purpose")
	}
	if runtime.GOARCH == "wasm" || runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two threads")
	}
	d := time.Second
	if testing.Short() {
		d = 100 * time.Millisecond
	}
	if s := os.Getenv("GD_PUB_LITMUS"); s != "" {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			t.Fatal(err)
		}
	}

	vals := []Value{
		valueRaceGroup(1),
		valueRaceGroup(1000),
		TimeValue(time.Unix(1, 0).UTC()),
		TimeValue(time.Unix(2, 0).In(time.FixedZone("B", 3600))),
	}
	valueRaceStore(vals[0])

	var stop atomic.Bool
	var bad atomic.Pointer[string]
	var torn atomic.Int64
	var wg sync.WaitGroup
	writers := max(runtime.GOMAXPROCS(0)/2, 1)
	for w := range writers {
		wg.Go(func() {
			for i := w; !stop.Load(); i++ {
				valueRaceStore(vals[i%len(vals)])
			}
		})
		wg.Go(func() {
			for !stop.Load() {
				if s := valueRaceCheck(valueRaceLoad()); s != "" {
					torn.Add(1)
					bad.CompareAndSwap(nil, &s)
				}
			}
		})
	}
	time.Sleep(d)
	stop.Store(true)
	wg.Wait()
	if s := bad.Load(); s != nil {
		t.Fatalf("%d torn values, first: %s", torn.Load(), *s)
	}
}
