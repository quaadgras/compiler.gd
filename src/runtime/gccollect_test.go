// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// collectAll runs collections until n more goroutines were collected than
// before, or it gives up.
func collectAll(t *testing.T, before uint64, n int) {
	t.Helper()
	for i := 0; i < 20; i++ {
		runtime.GC()
		if runtime.GoroutinesCollected()-before >= uint64(n) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("collected %d goroutines, want %d", runtime.GoroutinesCollected()-before, n)
}

func skipIfNotCollecting(t *testing.T) {
	if runtime.GOARCH == "wasm" || runtime.Raceenabled {
		t.Skip("goroutines are not collected here")
	}
}

// leakGoroutines starts n goroutines that block forever, each holding an
// object with a finalizer that counts in finalized. It is a function of
// its own so that nothing of it is left in the caller's frame.
//
//go:noinline
func leakGoroutines(n int, finalized *atomic.Int32) {
	started := make(chan bool)
	leak := func(block func()) {
		go func() {
			held := new([64]int)
			runtime.SetFinalizer(held, func(*[64]int) { finalized.Add(1) })
			started <- true
			block()
			runtime.KeepAlive(held)
		}()
		<-started
	}
	for i := 0; i < n; i++ {
		switch i % 6 {
		case 0: // a receive on a channel nobody has
			c := make(chan int)
			leak(func() { <-c })
		case 1: // a send
			c := make(chan int)
			leak(func() { c <- 1 })
		case 2: // a select
			c, d := make(chan int), make(chan string)
			leak(func() {
				select {
				case <-c:
				case d <- "x":
				}
			})
		case 3: // a mutex nobody will unlock
			// (In an object with a pointer: a mutex alone shares
			// its block of memory with other small objects, and
			// is reachable while any of them is.)
			mu := &new(struct {
				sync.Mutex
				_ *int
			}).Mutex
			mu.Lock()
			leak(func() { mu.Lock() })
		case 4: // a wait group nobody will finish
			wg := new(sync.WaitGroup)
			wg.Add(1)
			leak(func() { wg.Wait() })
		case 5: // nothing at all
			leak(func() { select {} })
		}
	}
}

// TestCollectGoroutines checks that goroutines blocked on what nothing
// else reaches are collected, with what they held.
func TestCollectGoroutines(t *testing.T) {
	skipIfNotCollecting(t)
	const N = 50
	before := runtime.GoroutinesCollected()
	var finalized atomic.Int32
	leakGoroutines(N, &finalized)
	// Give them the time to block.
	time.Sleep(20 * time.Millisecond)
	n := runtime.NumGoroutine()
	collectAll(t, before, N)
	if got := runtime.NumGoroutine(); got > n-N {
		t.Errorf("%d goroutines after collecting %d of %d", got, N, n)
	}
	// What only they held is freed.
	for i := 0; i < 50 && finalized.Load() < N; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	if got := finalized.Load(); got != N {
		t.Errorf("%d of %d objects held by collected goroutines were finalized", got, N)
	}
}

// TestCollectGoroutinesKept checks that goroutines that something can
// still wake are not collected, and run when it does.
func TestCollectGoroutinesKept(t *testing.T) {
	skipIfNotCollecting(t)
	const N = 20
	var woken atomic.Int32
	var wg sync.WaitGroup
	chans := make([]chan int, N)
	mus := make([]*sync.Mutex, N)
	started := make(chan bool)
	// A goroutine blocked on a channel that only another blocked
	// goroutine has, which waits on one this test has.
	inner := make(chan int)
	outer := make(chan int)
	wg.Add(2)
	go func() {
		defer wg.Done()
		started <- true
		<-inner
		woken.Add(1)
	}()
	<-started
	go func(inner chan int) {
		defer wg.Done()
		started <- true
		<-outer
		inner <- 1
		woken.Add(1)
	}(inner)
	<-started
	inner = nil // only the second goroutine has it now

	for i := range chans {
		chans[i] = make(chan int)
		mus[i] = new(sync.Mutex)
		mus[i].Lock()
		wg.Add(2)
		go func() {
			defer wg.Done()
			started <- true
			<-chans[i]
			woken.Add(1)
		}()
		<-started
		go func() {
			defer wg.Done()
			started <- true
			mus[i].Lock()
			woken.Add(1)
		}()
		<-started
	}
	time.Sleep(20 * time.Millisecond)
	before := runtime.GoroutinesCollected()
	for i := 0; i < 8; i++ {
		runtime.GC()
	}
	if got := runtime.GoroutinesCollected() - before; got != 0 {
		t.Errorf("%d goroutines that can be woken were collected", got)
	}
	for i := range chans {
		chans[i] <- 1
		mus[i].Unlock()
	}
	outer <- 1
	wg.Wait()
	if got := woken.Load(); got != 2*N+2 {
		t.Errorf("%d goroutines woke, want %d", got, 2*N+2)
	}
}

// TestCollectGoroutinesChurn runs collections while goroutines are
// started, blocked forever and woken.
func TestCollectGoroutinesChurn(t *testing.T) {
	skipIfNotCollecting(t)
	stop := make(chan bool)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				// One that leaks.
				c := make(chan []byte)
				go func() { c <- make([]byte, 1024) }()
				// One that does not.
				d := make(chan []byte)
				go func() { d <- make([]byte, 1024) }()
				if len(<-d) != 1024 {
					panic("bad value")
				}
				if i%16 == 0 {
					runtime.Gosched()
				}
			}
		}()
	}
	for i := 0; i < 30; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	close(stop)
	wg.Wait()
}
