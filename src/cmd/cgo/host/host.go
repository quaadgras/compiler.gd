// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package host exposes a public entry point for invoking cmd/cgo
// in-process. cmd/go imports this package to skip the per-cgo
// fork/exec overhead when building cgo packages.
//
// gd fork specific: cgo carries a pile of package-level state
// (flag.String pointers, fset, the typedef/goIdent maps in gcc.go,
// nerrors, etc.). We serialise calls under runMu and reset that
// state at entry; cgo's logic itself is unmodified.
package host

import (
	"io"
	"runtime"
	"sync"

	"cmd/cgo/internal/cgomain"
)

// runMu serialises in-process cgo invocations. cgomain has dozens
// of package-level globals (notably the flag.* pointers and the
// typedef/goIdent maps). resetState clears them at the start of
// each Run, but two concurrent Run calls would still race on the
// in-flight state.
var runMu sync.Mutex

// Run drives one cmd/cgo invocation in the calling process. args
// is the argv that the standalone cgo binary would have received
// as os.Args[1:]. stdout/stderr are reserved (cgomain writes
// directly to os.Stdout/os.Stderr today; outputs that need to be
// captured are written to files in -objdir). status is non-zero
// when cgo failed.
//
// cgomain.Run can fatal via cgomain.ExitFunc → os.Exit. We
// override ExitFunc with a runtime.Goexit-on-worker variant so a
// fatal cgo error doesn't kill cmd/go; the calling goroutine sees
// status set and Run returning normally.
func Run(args []string, stdout, stderr io.Writer) (status int, err error) {
	_ = stdout
	_ = stderr

	runMu.Lock()
	defer runMu.Unlock()

	var st int
	prevExit := cgomain.ExitFunc
	cgomain.ExitFunc = func(code int) {
		st = code
		runtime.Goexit()
	}
	defer func() { cgomain.ExitFunc = prevExit }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cgomain.Run(args)
	}()
	<-done

	return st, nil
}
