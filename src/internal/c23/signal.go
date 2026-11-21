// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"os/exec"
	"reflect"
	"sync"
)

// signal.h, with Linux's numbers.
const (
	SIGILL  = 4
	SIGABRT = 6
	SIGFPE  = 8
	SIGSEGV = 11

	Nsig = 65
)

// The handlers' special values: SIG_DFL is null, SIG_IGN 1 and SIG_ERR -1.
const (
	SigDfl = 0
	SigIgn = 1
)

var (
	SigMu sync.Mutex
)

func defaultAction(sig int32) {
	if sig == SIGABRT {
		abort()
	}
	os.Exit(128 + int(sig))
}

// waitStatus is the status, as wait gives it, of cmd, which ran with err,
// or -1 (with errno) if it did not run.
func WaitStatus(cmd *exec.Cmd, err error) int32 {
	if cmd.ProcessState == nil {
		SetErrnoOf(err)
		return -1
	}
	if c := cmd.ProcessState.ExitCode(); c >= 0 {
		return int32(c&0xff) << 8
	}
	// Ended by a signal: syscall.WaitStatus's Signal.
	if m := reflect.ValueOf(cmd.ProcessState.Sys()).MethodByName("Signal"); m.IsValid() {
		if r := m.Call(nil); len(r) == 1 && r[0].CanInt() {
			return int32(r[0].Int()) & 0x7f
		}
	}
	return -1
}
