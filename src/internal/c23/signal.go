// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"sync"
)

// signal.h, with Linux's numbers.
const (
	SIGINT  = 2
	SIGILL  = 4
	SIGABRT = 6
	SIGFPE  = 8
	SIGSEGV = 11
	SIGTERM = 15
	nsig    = 65
)

// The handlers' special values: SIG_DFL is null, SIG_IGN 1 and SIG_ERR -1.
const (
	sigDfl = 0
	sigIgn = 1
)

var (
	sigMu      sync.Mutex
	sigNotify  [nsig]chan os.Signal
	sigTouched [nsig]bool // the program set the signal's handler
)

// osSignal is the signal of the host that C's signal sig is, or nil: the
// signals that other processes send (a child's SIGCHLD and alarm's
// SIGALRM are the program's own).
func osSignal(sig int32) os.Signal {
	switch sig {
	case SIGINT:
		return os.Interrupt
	case SIGHUP, SIGQUIT, SIGUSR1, SIGUSR2, SIGPIPE, SIGTERM, SIGWINCH:
		return sysSignal(sig)
	}
	return nil
}

// sysSignalOf makes syscall.Signal(sig), where the system has the type
// (package syscall names the signals on every system os/signal
// supports; os has only Interrupt and Kill): found by reflection, so
// that a program without signals links none for it.
func sysSignalOf(sig int32) os.Signal {
	v := reflect.ValueOf(os.Interrupt)
	s := reflect.New(v.Type()).Elem()
	if !s.CanInt() {
		return nil
	}
	s.SetInt(int64(sig))
	return s.Interface().(os.Signal)
}

// Signal is signal: it sets sig's handler and returns the previous one.
// The handler stays (as glibc's BSD semantics), and it is sigaction's
// with SA_RESTART and an empty mask.
//
// The handler of a signal other processes send (osSignal) is also
// called when the program receives it, through os/signal: on a
// goroutine of its own, not on an interrupted thread, so it may do more
// than a handler may in C (it runs as another thread would). SIG_IGN
// ignores the signal and SIG_DFL restores its default action (as raise
// takes it, once the program set another handler). raise calls the
// handlers of any signal.
func Signal(sig int32, handler Ptr) Ptr {
	if sig <= 0 || sig >= nsig || sig == SIGKILL || sig == SIGSTOP {
		SetErrno(EINVAL)
		return FromInt(^uintptr(0))
	}
	if h := Addr(handler); h != sigDfl && h != sigIgn {
		FuncOf[func(int32)](handler)
	}
	return sigSet(sig, sigAction{handler: handler, flags: SA_RESTART}, true).handler
}

// sigWire makes os/signal deliver sig to handler, if other processes
// send it. sigMu is held.
func sigWire(sig int32, handler Ptr) {
	s := osSignal(sig)
	if s == nil {
		return
	}
	switch Addr(handler) {
	case sigIgn:
		sigTouched[sig] = true
		signal.Ignore(s)
	case sigDfl:
		if !sigTouched[sig] {
			break // the runtime's default action
		}
		// signal.Reset would keep an ignored signal ignored: the
		// program takes the default action instead.
		fallthrough
	default:
		sigTouched[sig] = true
		if sigNotify[sig] == nil {
			c := make(chan os.Signal, 1)
			sigNotify[sig] = c
			go func() {
				for range c {
					sigSendProcess(sig, sigInfo{code: SI_USER}, nil)
				}
			}()
		}
		signal.Notify(sigNotify[sig], s)
	}
}

// Raise is raise: it sends sig to the calling thread, which runs its
// handler at once unless it blocks sig, or takes its default action:
// abort for SIGABRT, nothing for those whose default is to ignore them
// and, for any other signal, the end of the program with the status
// 128+sig (as a shell reports a signal's), without flushing C's streams.
func Raise(sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	if sig == SIGKILL || sig == SIGSTOP {
		return killSelf(sig)
	}
	sigSendThread(sigSelf(), sig, sigInfo{code: SI_TKILL, pid: int32(os.Getpid()), uid: int32(os.Getuid())})
	return 0
}

func defaultAction(sig int32) {
	if sig == SIGABRT {
		abort()
	}
	os.Exit(128 + int(sig))
}

// System is system: it runs command with /bin/sh -c and returns its
// status as wait does (its exit status times 256, or the number of the
// signal that ended it), -1 if it cannot run, or, for a null command,
// whether there is a shell. It does not flush C's streams first.
func System(command Ptr) int32 {
	if Addr(command) == 0 {
		_, err := os.Stat("/bin/sh")
		return B2I(err == nil)
	}
	cmd := exec.Command("/bin/sh", "-c", GoString(command))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return waitStatus(cmd, cmd.Run())
}

// waitStatus is the status, as wait gives it, of cmd, which ran with err,
// or -1 (with errno) if it did not run.
func waitStatus(cmd *exec.Cmd, err error) int32 {
	if cmd.ProcessState == nil {
		setErrnoOf(err)
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
