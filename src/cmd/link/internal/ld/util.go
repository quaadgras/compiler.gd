// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/link/internal/loader"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
)

// currentLink is the *Link Main is currently driving. Published by
// Main itself (set after linknew, cleared on exit) so that
// package-level helpers (AtExit, Exit) can route through the active
// invocation's atExitFuncs without plumbing ctxt through every call
// site. Held under linkRunMu serialization in cmd/link/host.Run; for
// parallel link invocations these helpers would need explicit ctxt
// plumbing through every call site.
var currentLink *Link

// SetInProcess is retained for compatibility but is a no-op: the
// status pointer now lives on *Link (set by Main from its parameter).
// Exit reads the active *Link's inProcessStatus via currentLink.
func SetInProcess(status *int) {}

// ClearInProcess is retained for compatibility but is a no-op.
func ClearInProcess() {}

// AtExit registers f to run when Exit is called. The slice lives on
// the currentLink (set by Main after linknew) so concurrent in-process
// invocations don't race on append/drain. Falls back to a package-
// level slice for callers that run before Main publishes currentLink
// (early-startup paths).
func AtExit(f func()) {
	if currentLink != nil {
		currentLink.atExitFuncs = append(currentLink.atExitFuncs, f)
		return
	}
	legacyAtExitFuncs = append(legacyAtExitFuncs, f)
}

var legacyAtExitFuncs []func()

// runAtExitFuncs runs the queued set of AtExit functions: per-Link
// hooks first (LIFO), then the legacy fallback.
func runAtExitFuncs() {
	if currentLink != nil {
		for i := len(currentLink.atExitFuncs) - 1; i >= 0; i-- {
			currentLink.atExitFuncs[i]()
		}
		currentLink.atExitFuncs = nil
	}
	for i := len(legacyAtExitFuncs) - 1; i >= 0; i-- {
		legacyAtExitFuncs[i]()
	}
	legacyAtExitFuncs = nil
}

// Exit exits with code after executing all atExitFuncs. Under
// cmd/link/host.Run (currentLink.inProcessStatus non-nil) it writes
// the code to the status pointer and calls runtime.Goexit instead
// of os.Exit so the calling goroutine in cmd/go survives.
func Exit(code int) {
	runAtExitFuncs()
	if currentLink != nil && currentLink.inProcessStatus != nil {
		*currentLink.inProcessStatus = code
		runtime.Goexit()
	}
	os.Exit(code)
}

// Exitf logs an error message then calls Exit(2).
func Exitf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, os.Args[0]+": "+format+"\n", a...)
	nerrors++
	if *flagH {
		panic("error")
	}
	Exit(2)
}

// afterErrorAction updates 'nerrors' on error and invokes exit or
// panics in the proper circumstances.
func afterErrorAction() {
	nerrors++
	if *flagH {
		panic("error")
	}
	if nerrors > 20 && !*flagAllErrors {
		Exitf("too many errors")
	}
}

// Errorf logs an error message without a specific symbol for context.
// Use ctxt.Errorf when possible.
//
// If more than 20 errors have been printed, exit with an error.
//
// Logging an error means that on exit cmd/link will delete any
// output file and return a non-zero error code.
func Errorf(format string, args ...any) {
	format += "\n"
	fmt.Fprintf(os.Stderr, format, args...)
	afterErrorAction()
}

// Errorf method logs an error message.
//
// If more than 20 errors have been printed, exit with an error.
//
// Logging an error means that on exit cmd/link will delete any
// output file and return a non-zero error code.
func (ctxt *Link) Errorf(s loader.Sym, format string, args ...any) {
	if ctxt.loader != nil {
		ctxt.loader.Errorf(s, format, args...)
		return
	}
	// Note: this is not expected to happen very often.
	format = fmt.Sprintf("sym %d: %s", s, format)
	format += "\n"
	fmt.Fprintf(os.Stderr, format, args...)
	afterErrorAction()
}

func artrim(x []byte) string {
	i := 0
	j := len(x)
	for i < len(x) && x[i] == ' ' {
		i++
	}
	for j > i && x[j-1] == ' ' {
		j--
	}
	return string(x[i:j])
}

func stringtouint32(x []uint32, s string) {
	for i := 0; len(s) > 0; i++ {
		var buf [4]byte
		s = s[copy(buf[:], s):]
		x[i] = binary.LittleEndian.Uint32(buf[:])
	}
}
