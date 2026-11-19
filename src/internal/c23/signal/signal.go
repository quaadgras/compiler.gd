// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package signal

import (
	c23 "internal/c23"
	"os"
	"os/signal"
	"reflect"
)

const SIGINT = 2

const SIGTERM = 15

var sigNotify [c23.Nsig]chan os.Signal

var sigTouched [c23.Nsig]bool // the program set the signal's handler

// osSignal is the signal of the host that C's signal sig is, or nil: the
// signals that other processes send (a child's SIGCHLD and alarm's
// SIGALRM are the program's own).
func osSignal(sig int32) os.Signal {
	switch sig {
	case SIGINT:
		return os.Interrupt
	case SIGHUP, SIGQUIT, SIGUSR1, SIGUSR2, SIGPIPE, SIGTERM, c23.SIGWINCH:
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
func Signal(sig int32, handler c23.Ptr) c23.Ptr {
	if sig <= 0 || sig >= c23.Nsig || sig == c23.SIGKILL || sig == c23.SIGSTOP {
		c23.SetErrno(c23.EINVAL)
		return c23.FromInt(^uintptr(0))
	}
	if h := c23.Addr(handler); h != c23.SigDfl && h != c23.SigIgn {
		c23.FuncOf[func(int32)](handler)
	}
	return sigSet(sig, c23.SigAction{Handler: handler, Flags: SA_RESTART}, true).Handler
}

// sigWire makes os/signal deliver sig to handler, if other processes
// send it. sigMu is held.
func sigWire(sig int32, handler c23.Ptr) {
	s := osSignal(sig)
	if s == nil {
		return
	}
	switch c23.Addr(handler) {
	case c23.SigIgn:
		sigTouched[sig] = true
		signal.Ignore(s)
	case c23.SigDfl:
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
					c23.SigSendProcess(sig, c23.SigInfo{Code: SI_USER}, nil)
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
	if sig == c23.SIGKILL || sig == c23.SIGSTOP {
		return killSelf(sig)
	}
	sigSendThread(c23.SigSelf(), sig, c23.SigInfo{Code: c23.SI_TKILL, Pid: int32(os.Getpid()), Uid: int32(os.Getuid())})
	return 0
}
