// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gd

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	tracepkg "runtime/trace"
	"strings"
	"sync"

	"cmd/compile/internal/base"
)

func profileName(gd *base.Invocation, fn, suffix string) string {
	if strings.HasSuffix(fn, string(os.PathSeparator)) {
		err := os.MkdirAll(fn, 0755)
		if err != nil {
			gd.Fatalf("%v", err)
		}
	}
	if fi, statErr := os.Stat(fn); statErr == nil && fi.IsDir() {
		fn = filepath.Join(fn, url.PathEscape(gd.Ctxt.Pkgpath)+suffix)
	}
	return fn
}

var startProfileOnce sync.Once

func startProfile(gd *base.Invocation) {
	first := false
	startProfileOnce.Do(func() { first = true })
	if !first {
		// Process-global profile flags + runtime MemProfileRate /
		// SetBlockProfileRate / SetMutexProfileFraction; once is
		// sufficient. Concurrent in-process invocations all use
		// the same flag values from cmd/go.
		return
	}
	if gd.Flag.CPUProfile != "" {
		fn := profileName(gd, gd.Flag.CPUProfile, ".cpuprof")
		f, err := os.Create(fn)
		if err != nil {
			gd.Fatalf("%v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			gd.Fatalf("%v", err)
		}
		gd.AtExit(func() {
			pprof.StopCPUProfile()
			if err = f.Close(); err != nil {
				gd.Fatalf("error closing cpu profile: %v", err)
			}
		})
	}
	if gd.Flag.MemProfile != "" {
		if gd.Flag.MemProfileRate != 0 {
			runtime.MemProfileRate = gd.Flag.MemProfileRate
		}
		const (
			gzipFormat = 0
			textFormat = 1
		)
		// compilebench parses the memory profile to extract memstats,
		// which are only written in the legacy (text) pprof format.
		// See golang.org/issue/18641 and runtime/pprof/pprof.go:writeHeap.
		// gzipFormat is what most people want, otherwise
		var format = textFormat
		fn := gd.Flag.MemProfile
		if strings.HasSuffix(fn, string(os.PathSeparator)) {
			err := os.MkdirAll(fn, 0755)
			if err != nil {
				gd.Fatalf("%v", err)
			}
		}
		if fi, statErr := os.Stat(fn); statErr == nil && fi.IsDir() {
			fn = filepath.Join(fn, url.PathEscape(gd.Ctxt.Pkgpath)+".memprof")
			format = gzipFormat
		}

		f, err := os.Create(fn)

		if err != nil {
			gd.Fatalf("%v", err)
		}
		gd.AtExit(func() {
			// Profile all outstanding allocations.
			runtime.GC()
			if err := pprof.Lookup("heap").WriteTo(f, format); err != nil {
				gd.Fatalf("%v", err)
			}
			if err = f.Close(); err != nil {
				gd.Fatalf("error closing memory profile: %v", err)
			}
		})
	} else {
		// Not doing memory profiling; disable it entirely.
		runtime.MemProfileRate = 0
	}
	if gd.Flag.BlockProfile != "" {
		f, err := os.Create(profileName(gd, gd.Flag.BlockProfile, ".blockprof"))
		if err != nil {
			gd.Fatalf("%v", err)
		}
		runtime.SetBlockProfileRate(1)
		gd.AtExit(func() {
			pprof.Lookup("block").WriteTo(f, 0)
			f.Close()
		})
	}
	if gd.Flag.MutexProfile != "" {
		f, err := os.Create(profileName(gd, gd.Flag.MutexProfile, ".mutexprof"))
		if err != nil {
			gd.Fatalf("%v", err)
		}
		runtime.SetMutexProfileFraction(1)
		gd.AtExit(func() {
			pprof.Lookup("mutex").WriteTo(f, 0)
			f.Close()
		})
	}
	if gd.Flag.TraceProfile != "" {
		f, err := os.Create(profileName(gd, gd.Flag.TraceProfile, ".trace"))
		if err != nil {
			gd.Fatalf("%v", err)
		}
		if err := tracepkg.Start(f); err != nil {
			gd.Fatalf("%v", err)
		}
		gd.AtExit(func() {
			tracepkg.Stop()
			if err = f.Close(); err != nil {
				gd.Fatalf("error closing trace profile: %v", err)
			}
		})
	}
}
