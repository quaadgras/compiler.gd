// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math/bits"
	"os"
	"strconv"
	"sync"
	"time"
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
	SIGHUP    = 1
	SIGQUIT   = 3
	SIGTRAP   = 5
	SIGBUS    = 7
	SIGKILL   = 9
	SIGUSR1   = 10
	SIGUSR2   = 12
	SIGPIPE   = 13
	SIGALRM   = 14
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
	sigsetLen = 128 // sizeof(sigset_t), as glibc's and musl's
)

// sigaction's flags, sigprocmask's hows and siginfo_t's codes: Linux's.
const (
	SA_NOCLDSTOP = 1
	SA_NOCLDWAIT = 2
	SA_SIGINFO   = 4
	SA_RESTART   = 0x10000000
	SA_NODEFER   = 0x40000000
	SA_RESETHAND = 0x80000000

	SIG_BLOCK   = 0
	SIG_UNBLOCK = 1
	SIG_SETMASK = 2

	SI_USER    = 0
	SI_KERNEL  = 0x80
	SI_TKILL   = -6
	CLD_EXITED = 1
	CLD_KILLED = 2
)

// A sigAction is a struct sigaction: the handler (SIG_DFL, SIG_IGN, a
// void (*)(int) or, with SA_SIGINFO, a void (*)(int, siginfo_t *, void *)),
// the signals blocked while it runs, and the flags.
type sigAction struct {
	handler Ptr
	mask    uint64
	flags   uint32
}

// A sigInfo is what siginfo_t tells of a signal.
type sigInfo struct {
	code, pid, uid, status int32
}

// A sigThread is a thread's signal state.
type sigThread struct {
	mask      uint64 // the blocked signals (bit sig-1)
	pending   uint64 // the signals sent to the thread, or given it to run
	info      map[int32]sigInfo
	waiting   uint64 // the signals sigwait waits for
	suspended bool   // in sigsuspend or pause
}

var (
	sigActs     [nsig]sigAction
	sigCond     = sync.NewCond(&sigMu)
	sigPending  uint64 // the process's pending signals, which every thread blocks
	sigInfos    = map[int32]sigInfo{}
	sigLive     = map[*sigThread]bool{} // the threads that may take a process's signal
	sigWaiters  = map[*sigThread]bool{} // the threads in sigsuspend, pause or sigwait
	sigMainSelf = new(sigThread)
)

// The program's first goroutine, which runs package initialization and
// main, is the main thread.
func init() {
	gSelf().sig = sigMainSelf
	sigLive[sigMainSelf] = true
}

// sigSelf returns the calling thread's signal state.
func sigSelf() *sigThread {
	tb := gSelf()
	if tb.sig == nil {
		tb.sig = new(sigThread)
	}
	return tb.sig
}

// sigInherit is the signal state of a new thread: its creator's mask,
// and nothing pending. The thread ends with sigExit.
func sigInherit() *sigThread {
	t := &sigThread{mask: sigSelf().mask}
	sigMu.Lock()
	sigLive[t] = true
	sigMu.Unlock()
	return t
}

func sigExit(t *sigThread) {
	sigMu.Lock()
	delete(sigLive, t)
	sigMu.Unlock()
}

func sigBit(sig int32) uint64 { return 1 << (sig - 1) }

// sigUnblockable are the signals no mask blocks.
const sigUnblockable = 1<<(SIGKILL-1) | 1<<(SIGSTOP-1)

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
func sigIgnored(sig int32) bool {
	switch Addr(sigActs[sig].handler) {
	case sigIgn:
		return true
	case sigDfl:
		return sigDefaultIgnores(sig)
	}
	return false
}

func (t *sigThread) addPending(sig int32, info sigInfo) {
	t.pending |= sigBit(sig)
	if t.info == nil {
		t.info = map[int32]sigInfo{}
	}
	t.info[sig] = info
}

// sigSendProcess sends sig to the process. self is the sending thread,
// if one of the program's sends it.
func sigSendProcess(sig int32, info sigInfo, self *sigThread) {
	b := sigBit(sig)
	sigMu.Lock()
	// A thread waiting for the signal takes it.
	for t := range sigWaiters {
		if t.waiting&b != 0 || t.suspended && t.mask&b == 0 && !sigIgnored(sig) {
			t.addPending(sig, info)
			sigCond.Broadcast()
			sigMu.Unlock()
			return
		}
	}
	if self != nil && self.mask&b == 0 {
		ign := sigIgnored(sig)
		sigMu.Unlock()
		if !ign {
			self.run(sig, info)
		}
		return
	}
	for t := range sigLive {
		if t.mask&b == 0 {
			ign := sigIgnored(sig)
			sigMu.Unlock()
			if !ign {
				h := &sigThread{mask: t.mask}
				go func() {
					gSelf().sig = h
					h.run(sig, info)
					h.deliverPending()
				}()
			}
			return
		}
	}
	sigPending |= b
	sigInfos[sig] = info
	sigCond.Broadcast()
	sigMu.Unlock()
}

// sigSendThread sends sig to t, the calling thread.
func sigSendThread(t *sigThread, sig int32, info sigInfo) {
	sigMu.Lock()
	if t.mask&sigBit(sig) != 0 {
		t.addPending(sig, info)
		sigCond.Broadcast()
		sigMu.Unlock()
		return
	}
	sigMu.Unlock()
	t.run(sig, info)
}

// run takes sig's action on t, the calling thread: its handler runs with
// the action's mask added to t's.
func (t *sigThread) run(sig int32, info sigInfo) {
	sigMu.Lock()
	act := sigActs[sig]
	switch Addr(act.handler) {
	case sigIgn:
		sigMu.Unlock()
		return
	case sigDfl:
		sigMu.Unlock()
		if !sigDefaultIgnores(sig) {
			defaultAction(sig)
		}
		return
	}
	if act.flags&SA_RESETHAND != 0 {
		sigActs[sig] = sigAction{}
	}
	old := t.mask
	t.mask |= act.mask
	if act.flags&(SA_NODEFER|SA_RESETHAND) == 0 {
		t.mask |= sigBit(sig)
	}
	t.mask &^= sigUnblockable
	sigMu.Unlock()
	if act.flags&SA_SIGINFO != 0 {
		si := Calloc(1, sigInfoSize)
		Store32(si, uint32(sig))
		Store32(Add(si, 8), uint32(info.code))
		Store32(Add(si, 12), uint32(info.pid))
		Store32(Add(si, 16), uint32(info.uid))
		Store32(Add(si, 20), uint32(info.status))
		FuncOf[func(int32, Ptr, Ptr)](act.handler)(sig, si, Ptr{})
		Free(si)
	} else {
		FuncOf[func(int32)](act.handler)(sig)
	}
	sigMu.Lock()
	t.mask = old
	sigMu.Unlock()
}

// sizeof(siginfo_t): {int si_signo, si_errno, si_code; pid_t si_pid;
// uid_t si_uid; int si_status; ...}.
const sigInfoSize = 128

// deliverPending runs the actions of the signals pending for t, the
// calling thread, or the process that t's mask does not block, the
// lowest first; it reports whether there were any.
func (t *sigThread) deliverPending() bool {
	ran := false
	for {
		sigMu.Lock()
		ready := (t.pending | sigPending) &^ t.mask
		if ready == 0 {
			sigMu.Unlock()
			return ran
		}
		sig := int32(bits.TrailingZeros64(ready)) + 1
		b := sigBit(sig)
		var info sigInfo
		if t.pending&b != 0 {
			t.pending &^= b
			info = t.info[sig]
		} else {
			sigPending &^= b
			info = sigInfos[sig]
		}
		sigMu.Unlock()
		t.run(sig, info)
		ran = true
	}
}

// Signal sets.

func loadSigset(p Ptr) uint64 { return uint64(Load32(p)) | uint64(Load32(Add(p, 4)))<<32 }

func storeSigset(p Ptr, m uint64) {
	Store32(p, uint32(m))
	Store32(Add(p, 4), uint32(m>>32))
	for i := 8; i < sigsetLen; i += 4 {
		Store32(Add(p, i), 0)
	}
}

// sigValid reports whether sig is a signal's number (errno EINVAL if
// not).
func sigValid(sig int32) bool {
	if sig <= 0 || sig >= nsig {
		SetErrno(EINVAL)
		return false
	}
	return true
}

func Sigemptyset(set Ptr) int32 { storeSigset(set, 0); return 0 }
func Sigfillset(set Ptr) int32  { storeSigset(set, 1<<(nsig-1)-1); return 0 }

func Sigaddset(set Ptr, sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	storeSigset32(set, sig, true)
	return 0
}

func Sigdelset(set Ptr, sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	storeSigset32(set, sig, false)
	return 0
}

// storeSigset32 sets or clears sig in the set at p, in the int of p that
// holds it.
func storeSigset32(p Ptr, sig int32, on bool) {
	w := Add(p, int(sig-1)/32*4)
	b := uint32(1) << ((sig - 1) % 32)
	if on {
		Store32(w, Load32(w)|b)
	} else {
		Store32(w, Load32(w)&^b)
	}
}

func Sigismember(set Ptr, sig int32) int32 {
	if !sigValid(sig) {
		return -1
	}
	return B2I(loadSigset(set)&sigBit(sig) != 0)
}

// sigaction

// The offsets in struct sigaction {void (*sa_handler)(int) (or
// sa_sigaction); sigset_t sa_mask; int sa_flags; void (*sa_restorer)(void);}.
const (
	saMaskOff  = int(wordSize)
	saFlagsOff = saMaskOff + sigsetLen
)

// Sigaction is sigaction. An action for SIGINT, SIGTERM, SIGHUP, SIGQUIT,
// SIGUSR1, SIGUSR2, SIGPIPE or SIGWINCH also takes those the program
// receives from other processes (os/signal).
func Sigaction(sig int32, act, oact Ptr) int32 {
	if !sigValid(sig) {
		return -1
	}
	var a sigAction
	if Addr(act) != 0 {
		if sig == SIGKILL || sig == SIGSTOP {
			SetErrno(EINVAL)
			return -1
		}
		a = sigAction{
			handler: LoadPtr(act),
			mask:    loadSigset(Add(act, saMaskOff)) &^ sigUnblockable,
			flags:   Load32(Add(act, saFlagsOff)),
		}
		if h := Addr(a.handler); h != sigDfl && h != sigIgn {
			if a.flags&SA_SIGINFO != 0 {
				FuncOf[func(int32, Ptr, Ptr)](a.handler)
			} else {
				FuncOf[func(int32)](a.handler)
			}
		}
	}
	old := sigSet(sig, a, Addr(act) != 0)
	if Addr(oact) != 0 {
		StorePtr(oact, old.handler)
		storeSigset(Add(oact, saMaskOff), old.mask)
		Store32(Add(oact, saFlagsOff), old.flags)
	}
	return 0
}

// sigSet sets sig's action to a, if set, and returns the previous one.
func sigSet(sig int32, a sigAction, set bool) sigAction {
	sigMu.Lock()
	defer sigMu.Unlock()
	old := sigActs[sig]
	if !set {
		return old
	}
	sigActs[sig] = a
	if sigIgnored(sig) {
		// Pending, the signal is discarded.
		b := sigBit(sig)
		sigPending &^= b
		for t := range sigLive {
			t.pending &^= b
		}
		sigMainSelf.pending &^= b
	}
	sigWire(sig, a.handler)
	return old
}

// PthreadSigmask is pthread_sigmask: it changes the calling thread's
// mask, and runs the actions of the pending signals it unblocks before
// it returns.
func PthreadSigmask(how int32, set, oset Ptr) int32 {
	t := sigSelf()
	sigMu.Lock()
	old := t.mask
	if Addr(set) != 0 {
		m := loadSigset(set)
		switch how {
		case SIG_BLOCK:
			t.mask |= m
		case SIG_UNBLOCK:
			t.mask &^= m
		case SIG_SETMASK:
			t.mask = m
		default:
			sigMu.Unlock()
			return EINVAL
		}
		t.mask &^= sigUnblockable
	}
	sigMu.Unlock()
	if Addr(oset) != 0 {
		storeSigset(oset, old)
	}
	t.deliverPending()
	return 0
}

// Sigprocmask is sigprocmask, which is pthread_sigmask.
func Sigprocmask(how int32, set, oset Ptr) int32 {
	if e := PthreadSigmask(how, set, oset); e != 0 {
		SetErrno(e)
		return -1
	}
	return 0
}

// Sigpending is sigpending: the signals pending for the calling thread
// or the process that it blocks.
func Sigpending(set Ptr) int32 {
	t := sigSelf()
	sigMu.Lock()
	m := (t.pending | sigPending) & t.mask
	sigMu.Unlock()
	storeSigset(set, m)
	return 0
}

// Sigsuspend is sigsuspend: it waits with the mask at mask until a
// signal's action runs on the calling thread, then restores its mask.
// It returns -1 with errno EINTR.
func Sigsuspend(mask Ptr) int32 {
	suspend(loadSigset(mask) &^ sigUnblockable)
	SetErrno(EINTR)
	return -1
}

// Pause is pause.
func Pause() int32 {
	suspend(sigSelf().mask)
	SetErrno(EINTR)
	return -1
}

func suspend(mask uint64) {
	t := sigSelf()
	sigMu.Lock()
	old := t.mask
	t.mask = mask
	t.suspended = true
	sigWaiters[t] = true
	for (t.pending|sigPending)&^t.mask == 0 {
		sigCond.Wait()
	}
	t.suspended = false
	delete(sigWaiters, t)
	sigMu.Unlock()
	t.deliverPending()
	sigMu.Lock()
	t.mask = old
	sigMu.Unlock()
	t.deliverPending()
}

// Sigwait is sigwait: it waits for a signal of set to be pending (the
// caller blocks them), takes it and stores its number at sig.
func Sigwait(set, sig Ptr) int32 {
	w := loadSigset(set) &^ sigUnblockable
	if w == 0 {
		return EINVAL
	}
	t := sigSelf()
	sigMu.Lock()
	for {
		if r := t.pending & w; r != 0 {
			n := int32(bits.TrailingZeros64(r)) + 1
			t.pending &^= sigBit(n)
			Store32(sig, uint32(n))
			break
		}
		if r := sigPending & w; r != 0 {
			n := int32(bits.TrailingZeros64(r)) + 1
			sigPending &^= sigBit(n)
			Store32(sig, uint32(n))
			break
		}
		t.waiting = w
		sigWaiters[t] = true
		sigCond.Wait()
	}
	t.waiting = 0
	delete(sigWaiters, t)
	sigMu.Unlock()
	return 0
}

// Kill is kill. A signal to the program's own process is delivered to
// its handlers (pid 0, the process group, is the program and the
// children it started); another process's is os.Process.Signal's, which
// SIGKILL is everywhere and the other signals are where the system has
// them. A negative pid (a process group, or every process) is not
// supported: errno ENOSYS.
func Kill(pid, sig int32) int32 {
	if sig < 0 || sig >= nsig {
		SetErrno(EINVAL)
		return -1
	}
	self := int32(os.Getpid())
	switch {
	case pid == self:
		return killSelf(sig)
	case pid == 0:
		childMu.Lock()
		var cs []*child
		for _, c := range children {
			if !c.done {
				cs = append(cs, c)
			}
		}
		childMu.Unlock()
		for _, c := range cs {
			c.cmd.Process.Signal(sysSignal(sig))
		}
		return killSelf(sig)
	case pid < 0:
		SetErrno(ENOSYS)
		return -1
	}
	childMu.Lock()
	c := children[pid]
	childMu.Unlock()
	var p *os.Process
	if c != nil {
		if c.done {
			return 0 // not yet waited for: a zombie
		}
		p = c.cmd.Process
	} else {
		var err error
		if p, err = os.FindProcess(int(pid)); err != nil {
			setErrnoOf(err)
			return -1
		}
	}
	var err error
	if sig == SIGKILL {
		err = p.Kill()
	} else if s := sysSignal(sig); s != nil {
		err = p.Signal(s)
	} else {
		SetErrno(EINVAL)
		return -1
	}
	if err == os.ErrProcessDone {
		SetErrno(ESRCH)
		return -1
	}
	return errRet(err)
}

// killSelf sends sig to the program's process.
func killSelf(sig int32) int32 {
	switch sig {
	case 0:
		return 0
	case SIGKILL, SIGSTOP:
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			if sig == SIGKILL {
				p.Kill()
				select {}
			}
			if s := sysSignal(sig); s != nil {
				p.Signal(s)
			}
		}
		return 0
	}
	sigSendProcess(sig, sigInfo{code: SI_USER, pid: int32(os.Getpid()), uid: int32(os.Getuid())}, sigSelf())
	return 0
}

// sysSignal is the host's signal sig, or nil: syscall.Signal(sig), found
// by reflection on os.Interrupt's type so that the package does not
// import syscall.
func sysSignal(sig int32) os.Signal {
	if sig == SIGKILL {
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

var (
	sysSignalsMu sync.Mutex
	sysSignals   = map[int32]os.Signal{}
)

var (
	alarmMu    sync.Mutex
	alarmTimer *time.Timer
	alarmAt    time.Time
)

// Alarm is alarm: SIGALRM is sent to the process seconds from now
// (time.AfterFunc), and the result is the seconds left until the
// previous alarm, rounded as Linux does.
func Alarm(seconds uint32) uint32 {
	alarmMu.Lock()
	defer alarmMu.Unlock()
	var left uint32
	if alarmTimer != nil && alarmTimer.Stop() {
		d := alarmAt.Sub(time.Now())
		if d > 0 {
			left = uint32(d / 1e9)
			if d%1e9 >= 5e8 || left == 0 {
				left++
			}
		}
	}
	alarmTimer = nil
	if seconds > 0 {
		alarmAt = time.Now().Add(time.Duration(seconds) * time.Second)
		alarmTimer = time.AfterFunc(time.Duration(seconds)*time.Second, func() {
			sigSendProcess(SIGALRM, sigInfo{code: SI_KERNEL}, nil)
		})
	}
	return left
}

// Strsignal is strsignal: glibc's descriptions.
func Strsignal(sig int32) Ptr {
	strsignalMu.Lock()
	defer strsignalMu.Unlock()
	p, ok := strsignalMsg[sig]
	if !ok {
		p = CString(strsignal(sig))
		strsignalMsg[sig] = p
	}
	return p
}

var (
	strsignalMu  sync.Mutex
	strsignalMsg = map[int32]Ptr{}
)

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

func strsignal(sig int32) string {
	switch {
	case sig > 0 && int(sig) < len(sigText):
		return sigText[sig]
	case sig >= sigRTMin && sig <= sigRTMax:
		return "Real-time signal " + strconv.Itoa(int(sig-sigRTMin))
	}
	return "Unknown signal " + strconv.Itoa(int(sig))
}

// Psignal is psignal: "s: description" on standard error (fd 2).
func Psignal(sig int32, s Ptr) {
	msg := strsignal(sig) + "\n"
	if Addr(s) != 0 && Load8(s) != 0 {
		msg = GoString(s) + ": " + msg
	}
	if f := fdOS(2); f != nil {
		f.Write([]byte(msg))
	}
}
