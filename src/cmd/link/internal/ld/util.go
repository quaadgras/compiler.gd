// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/link/internal/loader"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
)

// goroutineID returns the current goroutine's id by parsing the first
// line of runtime.Stack ("goroutine N [..."). Slow (~µs) but called
// only from the AtExit/Exit/Errorf rare paths, so the overhead is
// negligible compared to a stale-routing bug.
func goroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine N [...]" — extract N.
	b := buf[:n]
	const prefix = "goroutine "
	if !bytes.HasPrefix(b, []byte(prefix)) {
		return 0
	}
	b = b[len(prefix):]
	end := bytes.IndexByte(b, ' ')
	if end < 0 {
		return 0
	}
	id, err := strconv.ParseInt(string(b[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// goroutineLink maps goroutine id to the *Link driving that
// goroutine's Main. Used to route AtExit / Exit / Errorf to the right
// invocation under concurrent in-process host.Run calls — the
// previous package-level currentLink raced (one goroutine's Main
// would overwrite it while another was mid-Errorf or mid-AtExit, so
// hooks fired against the wrong invocation and could remove an
// in-flight invocation's tmpdir).
var goroutineLink sync.Map // int64 -> *Link

// currentLink retains its name for code that runs outside the host.Run
// worker goroutine (early-startup paths, tests). Per-invocation ctxt
// lookup goes through goroutineLink.
var currentLink *Link

// linkForGoroutine returns the *Link the calling goroutine is
// currently driving (set by host.Run before invoking Main), falling
// back to currentLink for code that runs outside a worker goroutine.
func linkForGoroutine() *Link {
	if v, ok := goroutineLink.Load(goroutineID()); ok {
		return v.(*Link)
	}
	return currentLink
}

// SetCurrentLink registers ctxt as the *Link driving this goroutine's
// Main. Call from host.Run (or directly from Main for the standalone
// link binary) before any AtExit/Exit/Errorf path runs. Pass nil to
// clear when the invocation finishes.
func SetCurrentLink(ctxt *Link) {
	gid := goroutineID()
	if ctxt == nil {
		goroutineLink.Delete(gid)
		return
	}
	goroutineLink.Store(gid, ctxt)
}

// SetInProcess is retained for compatibility but is a no-op: the
// status pointer now lives on *Link (set by Main from its parameter).
// Exit reads the active *Link's inProcessStatus via currentLink.
func SetInProcess(status *int) {}

// ClearInProcess is retained for compatibility but is a no-op.
func ClearInProcess() {}

// AtExit on *Link registers f to run when this invocation exits.
// Always prefer this over the package-level AtExit: with concurrent
// in-process Main invocations, currentLink races (one goroutine's
// Main may overwrite currentLink while another goroutine is in the
// middle of registering an AtExit hook), and an AtExit registered
// against the wrong invocation can fire when the wrong invocation
// exits — e.g. removing another invocation's tmpdir while it is
// still linking.
func (ctxt *Link) AtExit(f func()) {
	ctxt.atExitFuncs = append(ctxt.atExitFuncs, f)
}

// AtExit (package-level) registers f via linkForGoroutine for legacy
// callers that don't have a *Link in scope. Pre-Main early-startup
// paths fall back to legacyAtExitFuncs. Prefer (*Link).AtExit when
// possible.
func AtExit(f func()) {
	if l := linkForGoroutine(); l != nil {
		l.AtExit(f)
		return
	}
	legacyAtExitFuncs = append(legacyAtExitFuncs, f)
}

var legacyAtExitFuncs []func()

// runAtExitFuncs runs the queued set of AtExit functions for ctxt:
// per-Link hooks first (LIFO), then the legacy fallback.
func (ctxt *Link) runAtExitFuncs() {
	for i := len(ctxt.atExitFuncs) - 1; i >= 0; i-- {
		ctxt.atExitFuncs[i]()
	}
	ctxt.atExitFuncs = nil
	for i := len(legacyAtExitFuncs) - 1; i >= 0; i-- {
		legacyAtExitFuncs[i]()
	}
	legacyAtExitFuncs = nil
}

// Exit exits with code after executing this goroutine's *Link's
// atExitFuncs. Under cmd/link/host.Run (inProcessStatus non-nil) it
// writes the code to the status pointer and calls runtime.Goexit
// instead of os.Exit so the calling goroutine in cmd/go survives.
func Exit(code int) {
	if l := linkForGoroutine(); l != nil {
		l.runAtExitFuncs()
		if l.inProcessStatus != nil {
			*l.inProcessStatus = code
			runtime.Goexit()
		}
	} else {
		// Pre-Main: only legacy hooks to run.
		for i := len(legacyAtExitFuncs) - 1; i >= 0; i-- {
			legacyAtExitFuncs[i]()
		}
		legacyAtExitFuncs = nil
	}
	os.Exit(code)
}

// Exitf logs an error message then calls Exit(2). Reads flagH and
// nerrors from this goroutine's *Link.
func Exitf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, os.Args[0]+": "+format+"\n", a...)
	if l := linkForGoroutine(); l != nil {
		l.nerrors++
		if l.flagH {
			panic("error")
		}
	}
	Exit(2)
}

// afterErrorAction updates 'nerrors' on error and invokes exit or
// panics in the proper circumstances. Routes through this goroutine's
// *Link (set by Main via SetCurrentLink).
func afterErrorAction() {
	l := linkForGoroutine()
	if l == nil {
		return // pre-Main; nothing to update
	}
	l.nerrors++
	if l.flagH {
		panic("error")
	}
	if l.nerrors > 20 && !l.flagAllErrors {
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
