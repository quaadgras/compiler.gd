// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package signal

import (
	c23 "internal/c23"
	"os"
	"sync"
	"time"
)

// sigtimedwait, sigwaitinfo, sigqueue, pthread_kill, sigaltstack and
// siginterrupt.

const SI_QUEUE = -1

// takeWait waits until a signal of w is pending for the calling thread or
// the process, or the deadline if it is not zero, and takes it: 0 if the
// deadline came first.
func takeWait(w uint64, deadline time.Time) (int32, c23.SigInfo) {
	t := c23.SigSelf()
	expired := false
	if !deadline.IsZero() {
		tm := time.AfterFunc(time.Until(deadline), func() {
			c23.SigMu.Lock()
			expired = true
			c23.SigCond.Broadcast()
			c23.SigMu.Unlock()
		})
		defer tm.Stop()
	}
	c23.TestCancel()
	c23.SigMu.Lock()
	for {
		sig, info := t.Take(w)
		canceled := c23.CancelPending()
		if sig != 0 || expired || canceled {
			t.Waiting = 0
			delete(c23.SigWaiters, t)
			c23.SigMu.Unlock()
			if sig == 0 && canceled {
				c23.TestCancel()
			}
			return sig, info
		}
		t.Waiting = w
		c23.SigWaiters[t] = true
		c23.SigCond.Wait()
	}
}

// Sigtimedwait is sigtimedwait: sigwait that stores the signal's
// siginfo_t at info (if not null) and returns its number, or -1 with
// errno EAGAIN once timeout (if not null) has passed.
func Sigtimedwait(set, info, timeout c23.Ptr) int32 {
	w := c23.LoadSigset(set) &^ c23.SigUnblockable
	var deadline time.Time
	if c23.Addr(timeout) != 0 {
		sec, nsec := int64(c23.Load64(timeout)), c23.LoadLong(c23.Add(timeout, 8))
		if sec < 0 || nsec < 0 || nsec >= 1e9 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		d := time.Duration(nsec)
		if sec >= (1<<63-1)/int64(time.Second) {
			d = 1<<63 - 1
		} else {
			d += time.Duration(sec) * time.Second
		}
		deadline = time.Now().Add(max(d, 1))
	}
	sig, si := takeWait(w, deadline)
	if sig == 0 {
		c23.SetErrno(c23.EAGAIN)
		return -1
	}
	if c23.Addr(info) != 0 {
		c23.StoreSigInfo(info, sig, si)
	}
	return sig
}

// Sigwaitinfo is sigwaitinfo: sigtimedwait without a timeout.
func Sigwaitinfo(set, info c23.Ptr) int32 { return Sigtimedwait(set, info, c23.Ptr{}) }

// Sigqueue is sigqueue: to the program's own process, the signal is sent
// with si_code SI_QUEUE and si_value value (union sigval, passed as its
// pointer member); to another, it is kill's, without the value. A signal
// already pending is not queued again, even a real-time one.
func Sigqueue(pid, sig int32, value c23.Ptr) int32 {
	if sig < 0 || sig >= c23.Nsig {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if pid != int32(os.Getpid()) || sig == 0 || sig == c23.SIGKILL || sig == c23.SIGSTOP {
		return Kill(pid, sig)
	}
	c23.SigSendProcess(sig, c23.SigInfo{Code: SI_QUEUE, Pid: pid, Uid: int32(os.Getuid()), Value: value}, c23.SigSelf())
	return 0
}

// PthreadKill is pthread_kill: sig is sent to thread, which is the
// calling thread (its handler runs before pthread_kill returns, unless
// it blocks sig), or another of the program's: if it blocks sig, sig is
// pending for it (for sigwait, a signalfd, or when it unblocks it);
// otherwise the handler runs on a goroutine of its own with the thread's
// mask, as the program's signals from elsewhere run (the thread goes on). 0 is the main thread's
// id, as pthread_self gives it. The result is an error number: ESRCH for
// a thread that ended or is not one.
func PthreadKill(thread uint64, sig int32) int32 {
	if sig < 0 || sig >= c23.Nsig {
		return c23.EINVAL
	}
	var t *c23.SigThread
	if thread == 0 {
		t = sigMainSelf
	} else {
		c23.ThreadsMu.Lock()
		if th := c23.Threads[thread]; th != nil {
			t = th.Sig
		}
		c23.ThreadsMu.Unlock()
	}
	c23.SigMu.Lock()
	live := t != nil && c23.SigLive[t]
	c23.SigMu.Unlock()
	if !live {
		return c23.ESRCH
	}
	if sig == 0 {
		return 0
	}
	if sig == c23.SIGKILL || sig == c23.SIGSTOP {
		killSelf(sig)
		return 0
	}
	info := c23.SigInfo{Code: c23.SI_TKILL, Pid: int32(os.Getpid()), Uid: int32(os.Getuid())}
	self := c23.SigSelf()
	if t == self {
		sigSendThread(t, sig, info)
		return 0
	}
	b := c23.SigBit(sig)
	c23.SigMu.Lock()
	if c23.SigIgnored(sig) {
		c23.SigMu.Unlock()
		return 0
	}
	if t.Mask&b != 0 || t.Waiting&b != 0 || c23.SigWaiters[t] && t.Waiting == 0 {
		// Pending for it, or taken by its sigwait or sigsuspend.
		t.AddPending(sig, info)
		c23.SigCond.Broadcast()
		c23.SigMu.Unlock()
		c23.PollNotify()
		return 0
	}
	h := &c23.SigThread{Mask: t.Mask}
	c23.SigMu.Unlock()
	go func() {
		c23.GSelf().Sig = h
		h.Run(sig, info)
		h.DeliverPending()
	}()
	return 0
}

// sigaltstack's flags and sizes: Linux's.
const (
	SS_ONSTACK  = 1
	SS_DISABLE  = 2
	MINSIGSTKSZ = 2048
)

type altStack struct {
	sp    c23.Ptr
	flags int32
	size  int64
}

var (
	altStacksMu sync.Mutex
	altStacks   = map[*c23.SigThread]altStack{}
)

// Sigaltstack is sigaltstack: it keeps the thread's stack_t {void *ss_sp;
// int ss_flags; size_t ss_size;} to give back, and has no other effect,
// as handlers run on goroutines.
func Sigaltstack(ss, oss c23.Ptr) int32 {
	t := c23.SigSelf()
	altStacksMu.Lock()
	defer altStacksMu.Unlock()
	old, ok := altStacks[t]
	if !ok {
		old.flags = SS_DISABLE
	}
	if c23.Addr(ss) != 0 {
		a := altStack{
			sp:    c23.LoadPtr(ss),
			flags: int32(c23.Load32(c23.Add(ss, int(c23.WordSize)))),
			size:  c23.LoadLong(c23.Add(ss, 2*int(c23.WordSize))),
		}
		switch {
		case a.flags&^SS_DISABLE != 0:
			c23.SetErrno(c23.EINVAL)
			return -1
		case a.flags == 0 && uint64(a.size) < MINSIGSTKSZ:
			c23.SetErrno(c23.ENOMEM)
			return -1
		}
		if a.flags == SS_DISABLE {
			a = altStack{flags: SS_DISABLE}
		}
		altStacks[t] = a
	}
	if c23.Addr(oss) != 0 {
		c23.StorePtr(oss, old.sp)
		c23.Store32(c23.Add(oss, int(c23.WordSize)), uint32(old.flags))
		c23.StoreLong(c23.Add(oss, 2*int(c23.WordSize)), old.size)
	}
	return 0
}

// Siginterrupt is siginterrupt: it clears sig's SA_RESTART if flag, and
// sets it if not. Nothing is interrupted either way.
func Siginterrupt(sig, flag int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	c23.SigMu.Lock()
	if flag != 0 {
		c23.SigActs[sig].Flags &^= SA_RESTART
	} else {
		c23.SigActs[sig].Flags |= SA_RESTART
	}
	c23.SigMu.Unlock()
	return 0
}
