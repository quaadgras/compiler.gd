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
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
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
// captured are written to files in -objdir). env is the KEY=VALUE
// list that the subprocess cgo would have received via Cmd.Env;
// host.Run applies it to os.Environ for the duration of the call
// and restores prior values on return. status is non-zero when
// cgo failed.
//
// cgomain.Run can fatal via cgomain.ExitFunc → os.Exit. We
// override ExitFunc with a runtime.Goexit-on-worker variant so a
// fatal cgo error doesn't kill cmd/go; the calling goroutine sees
// status set and Run returning normally.
//
// env handling matters because cmd/go clears CGO_LDFLAGS from the
// subprocess env before invoking cgo (otherwise cgomain reads it
// AND the -ldflags= cmd-line arg, double-recording //go:cgo_ldflag
// entries). For in-process callers the env clear is mandatory; we
// run under runMu so the os.Setenv churn is serialised against
// other in-process cgo calls. cgomain itself only reads env at
// init time (GOARCH, CGO_LDFLAGS), so concurrent compile/link
// goroutines are unaffected.
func Run(args []string, env []string, stdout, stderr io.Writer) (status int, err error) {
	_ = stdout
	_ = stderr

	runMu.Lock()
	defer runMu.Unlock()

	if restore := applyEnv(env); restore != nil {
		defer restore()
	}

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
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "cgo: panic: %v\n", r)
				os.Stderr.Write(debug.Stack())
				st = 2
			}
		}()
		cgomain.Run(args)
	}()
	<-done

	return st, nil
}

// applyEnv sets each KEY=VALUE pair from env on the current
// process and returns a func that restores prior values. KEYs
// already present in os.Environ are remembered; KEYs we add are
// unset on restore. Empty VALUE clears the variable (mirrors how
// cmd/go uses cgoenv = append(cgoenv, "CGO_LDFLAGS=") to wipe).
func applyEnv(env []string) func() {
	if len(env) == 0 {
		return nil
	}
	type prev struct {
		key string
		val string
		had bool
	}
	saved := make([]prev, 0, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		key, val := kv[:i], kv[i+1:]
		old, had := os.LookupEnv(key)
		saved = append(saved, prev{key, old, had})
		if val == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, val)
		}
	}
	return func() {
		for _, p := range saved {
			if p.had {
				os.Setenv(p.key, p.val)
			} else {
				os.Unsetenv(p.key)
			}
		}
	}
}
