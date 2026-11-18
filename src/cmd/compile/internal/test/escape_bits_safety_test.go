// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Tests of what doc/gd/escape-bits-safety.md's properties are checked
// with, and of obligation B5 where the runtime is the caller.

type ebsU struct{ a, b, c, d int }

// A finalizer may have results, which are discarded: the parameter
// that the compiler adds for a pointer result is not one of the
// finalizer's, and the finalizer goroutine passes nil for it.
func TestEscapeBitsOutBufFinalizer(t *testing.T) {
	done := make(chan int, 1)
	func() {
		p := &ebmT{a: 42}
		runtime.SetFinalizer(p, func(p *ebmT) *ebsU {
			u := &ebsU{a: p.a}
			done <- u.a
			return u
		})
	}()
	for i := 0; i < 50; i++ {
		runtime.GC()
		select {
		case got := <-done:
			if got != 42 {
				t.Errorf("finalizer saw %d, want 42", got)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Error("finalizer did not run")
}

const ebsStackPtrProg = `package main

import (
	"os"
	"runtime"
	"unsafe"
)

type T struct{ v, w int }

var g *T

//go:nosplit
func hide(p unsafe.Pointer) unsafe.Pointer { x := uintptr(p); return unsafe.Pointer(x ^ 0) }

//go:noinline
func leakGlobal() {
	var x T
	x.v = 1
	g = (*T)(hide(unsafe.Pointer(&x)))
}

//go:noinline
func leakRet() *T {
	var x T
	x.v = 1
	return (*T)(hide(unsafe.Pointer(&x)))
}

//go:noinline
func deep(n int, f func()) {
	var pad [128]int
	if n > 0 {
		deep(n-1, f)
	} else {
		f()
	}
	pad[n%128]++
}

//go:noinline
func deepRet(n int) *T {
	var pad [128]int
	var p *T
	if n > 0 {
		p = deepRet(n - 1)
	} else {
		p = leakRet()
	}
	pad[n%128]++
	return p
}

// gcParked collects while the calling goroutine is parked.
func gcParked() {
	done := make(chan struct{})
	go func() { runtime.GC(); close(done) }()
	<-done
}

func main() {
	switch os.Args[1] {
	case "global":
		deep(20, leakGlobal)
		gcParked()
	case "frame":
		p := deepRet(20)
		gcParked()
		println(p != nil)
	case "ok":
		// What the runtime itself points into stacks with, and
		// pointers up the stack.
		ch := make(chan *T)
		var x T
		go func() { var y T; ch <- &y }()
		p := <-ch
		q := &x
		deep(20, func() { q.v++; gcParked() })
		gcParked()
		println(p != nil, q.v)
	}
	println("done")
}
`

// GODEBUG=stackptrcheck=1 reports a pointer into a returned frame
// that the collector finds in a global or in an older frame, and
// nothing in a program without one.
func TestStackPtrCheck(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("builds a program")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(ebsStackPtrProg), 0o666); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "prog.exe")
	if out, err := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", exe, src).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, c := range []struct{ arg, want string }{
		{"global", "found pointer into a returned frame"},
		{"frame", "found pointer from a frame into a returned frame"},
		{"ok", ""},
	} {
		cmd := testenv.Command(t, exe, c.arg)
		cmd.Env = append(os.Environ(), "GODEBUG=stackptrcheck=1")
		out, err := cmd.CombinedOutput()
		switch {
		case c.want == "" && (err != nil || !strings.Contains(string(out), "done")):
			t.Errorf("%s: %v\n%s", c.arg, err, out)
		case c.want != "" && !strings.Contains(string(out), c.want):
			if len(out) > 2000 {
				out = out[:2000]
			}
			t.Errorf("%s: want a throw with %q, got (%v):\n%s", c.arg, c.want, err, out)
		}
		// Without the setting the program runs.
		if out, err := testenv.Command(t, exe, c.arg).CombinedOutput(); err != nil {
			t.Errorf("%s without stackptrcheck: %v\n%s", c.arg, err, out)
		}
	}
}
