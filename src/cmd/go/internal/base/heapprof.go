// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package base

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
)

// init registers, with GOGD_HEAPPROF=1 in the environment, a SIGUSR1
// handler that writes a heap profile to /tmp/cmdgo-heap-<pid>.pprof.
// Useful for diagnosing memory growth in long-running cmd/go processes
// (in-process compile, large builds).
//
// gd fork only — stock cmd/go has no need.
func init() {
	if os.Getenv("GOGD_HEAPPROF") != "1" {
		return
	}
	// Sample every allocation. Default 512 KB rate gave a profile that
	// summed to a few hundred kB even when live heap was multi-GB,
	// because cmd/go's allocations are mostly small objects that miss
	// the sample net. That records a stack for every allocation of
	// everything cmd/go runs in-process (the compiler, cmd/cc), which
	// made them several times slower (cmd/cc 15x), so it is opt-in.
	runtime.MemProfileRate = 1

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	go func() {
		for range c {
			runtime.GC()
			pid := os.Getpid()
			path := fmt.Sprintf("/tmp/cmdgo-heap-%d.pprof", pid)
			f, err := os.Create(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cmdgo heap profile create: %v\n", err)
				continue
			}
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintf(os.Stderr, "cmdgo heap profile write: %v\n", err)
				f.Close()
				continue
			}
			f.Close()

			// Goroutine dump alongside — long-running goroutines that
			// pin allocations show up here even when the heap profile
			// looks clean.
			gpath := fmt.Sprintf("/tmp/cmdgo-goroutine-%d.txt", pid)
			if g, err := os.Create(gpath); err == nil {
				pprof.Lookup("goroutine").WriteTo(g, 1)
				g.Close()
			}

			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Fprintf(os.Stderr,
				"cmdgo heap profile pid=%d alloc=%dMB sys=%dMB heap_objects=%d goroutines=%d → %s, %s\n",
				pid, m.Alloc/1024/1024, m.Sys/1024/1024, m.HeapObjects, runtime.NumGoroutine(), path, gpath)
		}
	}()
}
