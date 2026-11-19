// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math/bits"
	"strconv"
	"sync"
)

// POSIX's signals: sigaction, signal sets and masks, kill, sigsuspend,
// sigwait, alarm.
//
// A handler runs on a goroutine, not on an interrupted thread: a signal
// a thread sends itself (raise, or kill of its own process while it does
// not block the signal) runs the handler at once on the sending thread,
// as C's would before raise or kill returns; one from elsewhere (another
// process through os/signal, a child's end, alarm's timer, a thread's
// kill of its process that it blocks) runs it on a thread waiting in
// sigsuspend, pause or sigwait, or else on a new goroutine, while any
// thread does not block it. Nothing is interrupted: a blocking call does
// not end with EINTR because a handler ran (SA_RESTART is how everything
// behaves), and handlers may do what a thread may.
//
// Each thread has its own mask, which a new thread inherits from its
// creator; sigprocmask is pthread_sigmask. A signal blocked where it is
// sent stays pending until it is unblocked (then its handler runs before
// sigprocmask returns) or taken by sigwait.

// Signal numbers, Linux's.
const (
	SIGTRAP = 5
	SIGBUS  = 7
	SIGKILL = 9

	SIGCHLD   = 17
	SIGCONT   = 18
	SIGSTOP   = 19
	SIGTSTP   = 20
	SIGTTIN   = 21
	SIGTTOU   = 22
	SIGURG    = 23
	SIGWINCH  = 28
	sigRTMin  = 34
	sigRTMax  = 64
	SigsetLen = 128 // sizeof(sigset_t), as glibc's and musl's
)

// sigaction's flags, sigprocmask's hows and siginfo_t's codes: Linux's.
const (
	SA_NOCLDSTOP = 1

	SA_SIGINFO = 4

	SA_NODEFER   = 0x40000000
	SA_RESETHAND = 0x80000000

	SI_TKILL = -6
)

// A sigAction is a struct sigaction: the handler (SIG_DFL, SIG_IGN, a
// void (*)(int) or, with SA_SIGINFO, a void (*)(int, siginfo_t *, void *)),
// the signals blocked while it runs, and the flags.
type SigAction struct {
	Handler Ptr
	Mask    uint64
	Flags   uint32
}

// A sigInfo is what siginfo_t tells of a signal.
type SigInfo struct {
	Code, Pid, Uid, Status int32
	Value                  Ptr // si_value, of sigqueue
}

// A sigThread is a thread's signal state.
type SigThread struct {
	Mask      uint64 // the blocked signals (bit sig-1)
	Pending   uint64 // the signals sent to the thread, or given it to run
	info      map[int32]SigInfo
	Waiting   uint64 // the signals sigwait waits for
	suspended bool   // in sigsuspend or pause
}

var (
	SigActs    [Nsig]SigAction
	SigCond    = sync.NewCond(&SigMu)
	SigPending uint64 // the process's pending signals, which every thread blocks
	sigInfos   = map[int32]SigInfo{}
	SigLive    = map[*SigThread]bool{} // the threads that may take a process's signal
	SigWaiters = map[*SigThread]bool{} // the threads in sigsuspend, pause or sigwait

)

// sigSelf returns the calling thread's signal state.
func SigSelf() *SigThread {
	tb := GSelf()
	if tb.Sig == nil {
		tb.Sig = new(SigThread)
	}
	return tb.Sig
}

// sigInherit is the signal state of a new thread: its creator's mask
// (or every signal, if blocked), and nothing pending. The thread ends
// with sigExit.
func sigInherit(blocked bool) *SigThread {
	t := &SigThread{Mask: SigSelf().Mask}
	if blocked {
		t.Mask = (1<<(Nsig-1) - 1) &^ SigUnblockable
	}
	SigMu.Lock()
	SigLive[t] = true
	SigMu.Unlock()
	return t
}

func sigExit(t *SigThread) {
	SigMu.Lock()
	delete(SigLive, t)
	SigMu.Unlock()
}

func SigBit(sig int32) uint64 { return 1 << (sig - 1) }

// sigUnblockable are the signals no mask blocks.
const SigUnblockable = 1<<(SIGKILL-1) | 1<<(SIGSTOP-1)

// sigDefaultIgnores reports whether sig's default action is to ignore it.
// The stop signals are ignored too: a Go program cannot stop itself.
func sigDefaultIgnores(sig int32) bool {
	switch sig {
	case SIGCHLD, SIGCONT, SIGURG, SIGWINCH, SIGSTOP, SIGTSTP, SIGTTIN, SIGTTOU:
		return true
	}
	return false
}

// sigIgnored reports whether sig, sent now, is discarded. sigMu is held.
func SigIgnored(sig int32) bool {
	switch Addr(SigActs[sig].Handler) {
	case SigIgn:
		return true
	case SigDfl:
		return sigDefaultIgnores(sig)
	}
	return false
}

func (t *SigThread) AddPending(sig int32, info SigInfo) {
	t.Pending |= SigBit(sig)
	if t.info == nil {
		t.info = map[int32]SigInfo{}
	}
	t.info[sig] = info
}

// sigSendProcess sends sig to the process. self is the sending thread,
// if one of the program's sends it.
func SigSendProcess(sig int32, info SigInfo, self *SigThread) {
	b := SigBit(sig)
	SigMu.Lock()
	// A thread waiting for the signal takes it.
	for t := range SigWaiters {
		if t.Waiting&b != 0 || t.suspended && t.Mask&b == 0 && !SigIgnored(sig) {
			t.AddPending(sig, info)
			SigCond.Broadcast()
			SigMu.Unlock()
			return
		}
	}
	if self != nil && self.Mask&b == 0 {
		ign := SigIgnored(sig)
		SigMu.Unlock()
		if !ign {
			self.Run(sig, info)
		}
		return
	}
	for t := range SigLive {
		if t.Mask&b == 0 {
			ign := SigIgnored(sig)
			SigMu.Unlock()
			if !ign {
				h := &SigThread{Mask: t.Mask}
				go func() {
					GSelf().Sig = h
					h.Run(sig, info)
					h.DeliverPending()
				}()
			}
			return
		}
	}
	SigPending |= b
	sigInfos[sig] = info
	SigCond.Broadcast()
	SigMu.Unlock()
	PollNotify() // a signalfd may be readable
}

// run takes sig's action on t, the calling thread: its handler runs with
// the action's mask added to t's.
func (t *SigThread) Run(sig int32, info SigInfo) {
	SigMu.Lock()
	act := SigActs[sig]
	switch Addr(act.Handler) {
	case SigIgn:
		SigMu.Unlock()
		return
	case SigDfl:
		SigMu.Unlock()
		if !sigDefaultIgnores(sig) {
			defaultAction(sig)
		}
		return
	}
	if act.Flags&SA_RESETHAND != 0 {
		SigActs[sig] = SigAction{}
	}
	old := t.Mask
	t.Mask |= act.Mask
	if act.Flags&(SA_NODEFER|SA_RESETHAND) == 0 {
		t.Mask |= SigBit(sig)
	}
	t.Mask &^= SigUnblockable
	SigMu.Unlock()
	if act.Flags&SA_SIGINFO != 0 {
		si := Calloc(1, sigInfoSize)
		Store32(si, uint32(sig))
		Store32(Add(si, 8), uint32(info.Code))
		Store32(Add(si, 12), uint32(info.Pid))
		Store32(Add(si, 16), uint32(info.Uid))
		Store32(Add(si, 20), uint32(info.Status))
		StorePtr(Add(si, SigInfoValueOff), info.Value)
		FuncOf[func(int32, Ptr, Ptr)](act.Handler)(sig, si, Ptr{})
		Free(si)
	} else {
		FuncOf[func(int32)](act.Handler)(sig)
	}
	SigMu.Lock()
	t.Mask = old
	SigMu.Unlock()
}

// sizeof(siginfo_t): {int si_signo, si_errno, si_code; pid_t si_pid;
// uid_t si_uid; int si_status; ...}.
const sigInfoSize = 128

// SigInfoSize is sizeof(siginfo_t) on the target: 116 bytes on wasm32.
const SigInfoSize = 24 + 3*WordSize + 80

// SigInfoValueOff is the offset of si_value in siginfo_t, after
// void *si_addr at 24.
const SigInfoValueOff = 24 + int(WordSize)

// StoreSigInfo stores sig and info as siginfo_t at si, all of it.
func StoreSigInfo(si Ptr, sig int32, info SigInfo) {
	Memset(si, 0, uint64(SigInfoSize))
	Store32(si, uint32(sig))
	Store32(Add(si, 8), uint32(info.Code))
	Store32(Add(si, 12), uint32(info.Pid))
	Store32(Add(si, 16), uint32(info.Uid))
	Store32(Add(si, 20), uint32(info.Status))
	StorePtr(Add(si, SigInfoValueOff), info.Value)
}

// Take takes the lowest signal of w pending for t or for the process,
// t's first, and returns it and what siginfo_t tells of it; 0 if none
// is. SigMu is held.
func (t *SigThread) Take(w uint64) (int32, SigInfo) {
	if r := t.Pending & w; r != 0 {
		sig := int32(bits.TrailingZeros64(r)) + 1
		t.Pending &^= SigBit(sig)
		return sig, t.info[sig]
	}
	if r := SigPending & w; r != 0 {
		sig := int32(bits.TrailingZeros64(r)) + 1
		SigPending &^= SigBit(sig)
		return sig, sigInfos[sig]
	}
	return 0, SigInfo{}
}

// deliverPending runs the actions of the signals pending for t, the
// calling thread, or the process that t's mask does not block, the
// lowest first; it reports whether there were any.
func (t *SigThread) DeliverPending() bool {
	ran := false
	for {
		SigMu.Lock()
		ready := (t.Pending | SigPending) &^ t.Mask
		if ready == 0 {
			SigMu.Unlock()
			return ran
		}
		sig := int32(bits.TrailingZeros64(ready)) + 1
		b := SigBit(sig)
		var info SigInfo
		if t.Pending&b != 0 {
			t.Pending &^= b
			info = t.info[sig]
		} else {
			SigPending &^= b
			info = sigInfos[sig]
		}
		SigMu.Unlock()
		t.Run(sig, info)
		ran = true
	}
}

// Signal sets.

func LoadSigset(p Ptr) uint64 { return uint64(Load32(p)) | uint64(Load32(Add(p, 4)))<<32 }

func StoreSigset(p Ptr, m uint64) {
	Store32(p, uint32(m))
	Store32(Add(p, 4), uint32(m>>32))
	for i := 8; i < SigsetLen; i += 4 {
		Store32(Add(p, i), 0)
	}
}

// sigaction

// The offsets in struct sigaction {void (*sa_handler)(int) (or
// sa_sigaction); sigset_t sa_mask; int sa_flags; void (*sa_restorer)(void);}.
const ()

func Suspend(mask uint64) {
	t := SigSelf()
	SigMu.Lock()
	old := t.Mask
	t.Mask = mask
	t.suspended = true
	SigWaiters[t] = true
	for (t.Pending|SigPending)&^t.Mask == 0 {
		SigCond.Wait()
	}
	t.suspended = false
	delete(SigWaiters, t)
	SigMu.Unlock()
	t.DeliverPending()
	SigMu.Lock()
	t.Mask = old
	SigMu.Unlock()
	t.DeliverPending()
}

var ()

var ()

var ()

var sigText = [...]string{
	1: "Hangup", "Interrupt", "Quit", "Illegal instruction", "Trace/breakpoint trap",
	"Aborted", "Bus error", "Floating point exception", "Killed", "User defined signal 1",
	"Segmentation fault", "User defined signal 2", "Broken pipe", "Alarm clock",
	"Terminated", "Stack fault", "Child exited", "Continued", "Stopped (signal)", "Stopped",
	"Stopped (tty input)", "Stopped (tty output)", "Urgent I/O condition",
	"CPU time limit exceeded", "File size limit exceeded", "Virtual timer expired",
	"Profiling timer expired", "Window changed", "I/O possible", "Power failure",
	"Bad system call",
}

func Strsignal(sig int32) string {
	switch {
	case sig > 0 && int(sig) < len(sigText):
		return sigText[sig]
	case sig >= sigRTMin && sig <= sigRTMax:
		return "Real-time signal " + strconv.Itoa(int(sig-sigRTMin))
	}
	return "Unknown signal " + strconv.Itoa(int(sig))
}
