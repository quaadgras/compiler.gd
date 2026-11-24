// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package signal

import (
	c23 "internal/c23"
	"math/bits"
	"os"
	"sync"
)

const SIGHUP = 1

const SIGQUIT = 3

const SIGUSR1 = 10

const SIGUSR2 = 12

const SIGPIPE = 13

const SA_RESTART = 0x10000000

const SIG_BLOCK = 0

const SIG_UNBLOCK = 1

const SIG_SETMASK = 2

const SI_USER = 0

var sigMainSelf = new(c23.SigThread)

// The program's first goroutine, which runs package initialization and
// main, is the main thread.
func init() {
	c23.GSelf().Sig = sigMainSelf
	c23.SigLive[sigMainSelf] = true
}

// sigSendThread sends sig to t, the calling thread.
func sigSendThread(t *c23.SigThread, sig int32, info c23.SigInfo) {
	c23.SigMu.Lock()
	if t.Mask&c23.SigBit(sig) != 0 {
		t.AddPending(sig, info)
		c23.SigCond.Broadcast()
		c23.SigMu.Unlock()
		return
	}
	c23.SigMu.Unlock()
	t.Run(sig, info)
}

// sigValid reports whether sig is a signal's number (errno EINVAL if
// not).
func sigValid(sig int32) bool {
	if sig <= 0 || sig >= c23.Nsig {
		c23.SetErrno(c23.EINVAL)
		return false
	}
	return true
}

func Sigemptyset(set *c23.Void) int32 { c23.StoreSigset(set, 0); return 0 }

func Sigfillset(set *c23.Void) int32 { c23.StoreSigset(set, 1<<(c23.Nsig-1)-1); return 0 }

func Sigaddset(set *c23.Void, sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	storeSigset32(set, sig, true)
	return 0
}

func Sigdelset(set *c23.Void, sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	storeSigset32(set, sig, false)
	return 0
}

// storeSigset32 sets or clears sig in the set at p, in the int of p that
// holds it.
func storeSigset32(p *c23.Void, sig int32, on bool) {
	w := c23.Add(p, int(sig-1)/32*4)
	b := uint32(1) << ((sig - 1) % 32)
	if on {
		c23.Store32(w, c23.Load32(w)|b)
	} else {
		c23.Store32(w, c23.Load32(w)&^b)
	}
}

func Sigismember(set *c23.Void, sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	return c23.B2I(c23.LoadSigset(set)&c23.SigBit(sig) != 0)
}

const saMaskOff = int(c23.WordSize)

const saFlagsOff = saMaskOff + c23.SigsetLen

// Sigaction is sigaction. An action for SIGINT, SIGTERM, SIGHUP, SIGQUIT,
// SIGUSR1, SIGUSR2, SIGPIPE or SIGWINCH also takes those the program
// receives from other processes (os/signal).
func Sigaction(sig int32, act, oact *c23.Void) int32 {
	if !sigValid(sig) {
		return -1
	}
	var a c23.SigAction
	if c23.Addr(act) != 0 {
		if sig == c23.SIGKILL || sig == c23.SIGSTOP {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		a = c23.SigAction{
			Handler: c23.LoadPtr(act),
			Mask:    c23.LoadSigset(c23.Add(act, saMaskOff)) &^ c23.SigUnblockable,
			Flags:   c23.Load32(c23.Add(act, saFlagsOff)),
		}
		if h := c23.Addr(a.Handler); h != c23.SigDfl && h != c23.SigIgn {
			if a.Flags&c23.SA_SIGINFO != 0 {
				c23.FuncOf[func(int32, *c23.Void, *c23.Void)](a.Handler)
			} else {
				c23.FuncOf[func(int32)](a.Handler)
			}
		}
	}
	old := sigSet(sig, a, c23.Addr(act) != 0)
	if c23.Addr(oact) != 0 {
		c23.StorePtr(oact, old.Handler)
		c23.StoreSigset(c23.Add(oact, saMaskOff), old.Mask)
		c23.Store32(c23.Add(oact, saFlagsOff), old.Flags)
	}
	return 0
}

// sigSet sets sig's action to a, if set, and returns the previous one.
func sigSet(sig int32, a c23.SigAction, set bool) c23.SigAction {
	c23.SigMu.Lock()
	defer c23.SigMu.Unlock()
	old := c23.SigActs[sig]
	if !set {
		return old
	}
	c23.SigActs[sig] = a
	if c23.SigIgnored(sig) {
		// Pending, the signal is discarded.
		b := c23.SigBit(sig)
		c23.SigPending &^= b
		for t := range c23.SigLive {
			t.Pending &^= b
		}
		sigMainSelf.Pending &^= b
	}
	sigWire(sig, a.Handler)
	return old
}

// PthreadSigmask is pthread_sigmask: it changes the calling thread's
// mask, and runs the actions of the pending signals it unblocks before
// it returns.
func PthreadSigmask(how int32, set, oset *c23.Void) int32 {
	t := c23.SigSelf()
	c23.SigMu.Lock()
	old := t.Mask
	if c23.Addr(set) != 0 {
		m := c23.LoadSigset(set)
		switch how {
		case SIG_BLOCK:
			t.Mask |= m
		case SIG_UNBLOCK:
			t.Mask &^= m
		case SIG_SETMASK:
			t.Mask = m
		default:
			c23.SigMu.Unlock()
			return c23.EINVAL
		}
		t.Mask &^= c23.SigUnblockable
	}
	c23.SigMu.Unlock()
	if c23.Addr(oset) != 0 {
		c23.StoreSigset(oset, old)
	}
	t.DeliverPending()
	return 0
}

// Sigprocmask is sigprocmask, which is pthread_sigmask.
func Sigprocmask(how int32, set, oset *c23.Void) int32 {
	if e := PthreadSigmask(how, set, oset); e != 0 {
		c23.SetErrno(e)
		return -1
	}
	return 0
}

// Sigpending is sigpending: the signals pending for the calling thread
// or the process that it blocks.
func Sigpending(set *c23.Void) int32 {
	t := c23.SigSelf()
	c23.SigMu.Lock()
	m := (t.Pending | c23.SigPending) & t.Mask
	c23.SigMu.Unlock()
	c23.StoreSigset(set, m)
	return 0
}

// Sigsuspend is sigsuspend: it waits with the mask at mask until a
// signal's action runs on the calling thread, then restores its mask.
// It returns -1 with errno EINTR.
func Sigsuspend(mask *c23.Void) int32 {
	c23.Suspend(c23.LoadSigset(mask) &^ c23.SigUnblockable)
	c23.SetErrno(c23.EINTR)
	return -1
}

// Sigwait is sigwait, a cancellation point: it waits for a signal of set
// to be pending (the caller blocks them), takes it and stores its number
// at sig.
func Sigwait(set, sig *c23.Void) int32 {
	w := c23.LoadSigset(set) &^ c23.SigUnblockable
	if w == 0 {
		return c23.EINVAL
	}
	c23.TestCancel()
	t := c23.SigSelf()
	c23.SigMu.Lock()
	for {
		if c23.CancelPending() {
			t.Waiting = 0
			delete(c23.SigWaiters, t)
			c23.SigMu.Unlock()
			c23.TestCancel()
		}
		if r := t.Pending & w; r != 0 {
			n := int32(bits.TrailingZeros64(r)) + 1
			t.Pending &^= c23.SigBit(n)
			c23.Store32(sig, uint32(n))
			break
		}
		if r := c23.SigPending & w; r != 0 {
			n := int32(bits.TrailingZeros64(r)) + 1
			c23.SigPending &^= c23.SigBit(n)
			c23.Store32(sig, uint32(n))
			break
		}
		t.Waiting = w
		c23.SigWaiters[t] = true
		c23.SigCond.Wait()
	}
	t.Waiting = 0
	delete(c23.SigWaiters, t)
	c23.SigMu.Unlock()
	return 0
}

// Kill is kill. A signal to the program's own process is delivered to
// its handlers (pid 0, the process group, is the program and the
// children it started); another process's is os.Process.Signal's, which
// SIGKILL is everywhere and the other signals are where the system has
// them. A negative pid (a process group, or every process) is not
// supported: errno ENOSYS.
func Kill(pid, sig int32) int32 {
	if sig < 0 || sig >= c23.Nsig {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	self := int32(os.Getpid())
	switch {
	case pid == self:
		return killSelf(sig)
	case pid == 0:
		c23.ChildMu.Lock()
		var cs []*c23.Child
		for _, c := range c23.Children {
			if !c.Done {
				cs = append(cs, c)
			}
		}
		c23.ChildMu.Unlock()
		for _, c := range cs {
			c.Cmd.Process.Signal(sysSignal(sig))
		}
		return killSelf(sig)
	case pid < 0:
		c23.SetErrno(c23.ENOSYS)
		return -1
	}
	c23.ChildMu.Lock()
	c := c23.Children[pid]
	c23.ChildMu.Unlock()
	var p *os.Process
	if c != nil {
		if c.Done {
			return 0 // not yet waited for: a zombie
		}
		p = c.Cmd.Process
	} else {
		var err error
		if p, err = os.FindProcess(int(pid)); err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
	}
	var err error
	if sig == c23.SIGKILL {
		err = p.Kill()
	} else if s := sysSignal(sig); s != nil {
		err = p.Signal(s)
	} else {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if err == os.ErrProcessDone {
		c23.SetErrno(c23.ESRCH)
		return -1
	}
	return c23.ErrRet(err)
}

// killSelf sends sig to the program's process.
func killSelf(sig int32) int32 {
	switch sig {
	case 0:
		return 0
	case c23.SIGKILL, c23.SIGSTOP:
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			if sig == c23.SIGKILL {
				p.Kill()
				select {}
			}
			if s := sysSignal(sig); s != nil {
				p.Signal(s)
			}
		}
		return 0
	}
	c23.SigSendProcess(sig, c23.SigInfo{Code: SI_USER, Pid: int32(os.Getpid()), Uid: int32(os.Getuid())}, c23.SigSelf())
	return 0
}

// sysSignal is the host's signal sig, or nil: syscall.Signal(sig), found
// by reflection on os.Interrupt's type so that the package does not
// import syscall.
func sysSignal(sig int32) os.Signal {
	if sig == c23.SIGKILL {
		return os.Kill
	}
	sysSignalsMu.Lock()
	defer sysSignalsMu.Unlock()
	s, ok := sysSignals[sig]
	if !ok {
		s = sysSignalOf(sig)
		sysSignals[sig] = s
	}
	return s
}

var sysSignalsMu sync.Mutex

var sysSignals = map[int32]os.Signal{}

// Psignal is psignal: "s: description" on standard error (fd 2).
func Psignal(sig int32, s *c23.Void) {
	msg := c23.Strsignal(sig) + "\n"
	if c23.Addr(s) != 0 && c23.Load8(s) != 0 {
		msg = c23.GoString(s) + ": " + msg
	}
	if f := c23.FdOS(2); f != nil {
		f.Write([]byte(msg))
	}
}
