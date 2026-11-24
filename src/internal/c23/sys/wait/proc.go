// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wait

import (
	c23 "internal/c23"
	"os"
	"time"
)

const WNOHANG = 1

const WUNTRACED = 2

const WSTOPPED = 2

const WEXITED = 4

const WCONTINUED = 8

const WNOWAIT = 0x1000000

// waitid's idtype_t.
const (
	P_ALL   = 0
	P_PID   = 1
	P_PGID  = 2
	P_PIDFD = 3
)

// siginfo_t's codes of a child's end.
const (
	CLD_EXITED = 1
	CLD_KILLED = 2
	CLD_DUMPED = 3
)

// reap waits for a child that ended: pid's, or any child's if pid is -1
// (0 or a process group: any child posix_spawn started), the one that
// ended first. It returns nil and 0 if WNOHANG and none ended yet, nil
// and errno ECHILD if there is no such child. The child is forgotten
// unless keep.
func reap(pid int32, nohang, keep bool) (*c23.Child, int32) {
	c23.ChildMu.Lock()
	defer c23.ChildMu.Unlock()
	for {
		var found *c23.Child
		any := false
		for _, c := range c23.Children {
			if pid > 0 && c.Pid != pid {
				continue
			}
			any = true
			if c.Done && (found == nil || c.Seq < found.Seq) {
				found = c
			}
		}
		if found != nil {
			if !keep {
				delete(c23.Children, found.Pid)
			}
			return found, 0
		}
		if !any {
			return nil, c23.ECHILD
		}
		if nohang {
			return nil, 0
		}
		c23.ChildCond.Wait()
	}
}

// Waitpid is waitpid: for pid -1 (or 0, or a process group: any child
// posix_spawn started) or one child. It reports only children that
// ended, with Linux's status: the exit status times 256, or the signal
// that ended it. WUNTRACED and WCONTINUED are accepted, and report
// nothing. It is not interrupted by signals.
func Waitpid(pid int32, status c23.Ptr, options int32) int32 {
	return Wait4(pid, status, options, c23.Ptr(nil))
}

// Wait is wait.
func Wait(status c23.Ptr) int32 { return Waitpid(-1, status, 0) }

// Wait3 is wait3: wait4 of any child.
func Wait3(status c23.Ptr, options int32, usage c23.Ptr) int32 {
	return Wait4(-1, status, options, usage)
}

// Wait4 is wait4: waitpid, and the child's struct rusage at usage if
// that is not null: its user and system time, as os.ProcessState gives
// them, and where the system tells it (syscall.Rusage), the rest.
func Wait4(pid int32, status c23.Ptr, options int32, usage c23.Ptr) int32 {
	if options&^(WNOHANG|WUNTRACED|WCONTINUED) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	c, e := reap(pid, options&WNOHANG != 0, false)
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	if c == nil {
		return 0
	}
	if c23.Addr(status) != 0 {
		c23.Store32(status, uint32(c.Status))
	}
	if c23.Addr(usage) != 0 {
		storeRusage(usage, c.Cmd.ProcessState)
	}
	return c.Pid
}

// storeRusage stores ps's usage as struct rusage: two struct timevals
// and 14 longs.
func storeRusage(usage c23.Ptr, ps *os.ProcessState) {
	var ru [18]int64
	if ps != nil {
		ru = sysRusage(ps)
		for i, d := range [2]time.Duration{ps.UserTime(), ps.SystemTime()} {
			ru[2*i], ru[2*i+1] = int64(d/time.Second), int64(d%time.Second/time.Microsecond)
		}
	}
	for i, v := range ru {
		if i < 4 {
			c23.Store64(c23.Add(usage, 8*i), uint64(v)) // time_t and suseconds_t
		} else {
			c23.StoreLong(c23.Add(usage, 32+int(c23.LongSize)*(i-4)), v)
		}
	}
}

// Waitid is waitid. idtype P_PID waits for child id, P_ALL for any, and
// P_PGID for any as well (the children are in the program's group
// unless posix_spawn put them in another); P_PIDFD is not supported
// (EINVAL). Only ends are reported: WSTOPPED and WCONTINUED are accepted
// and report nothing. infop's siginfo_t is filled as Linux does: SIGCHLD,
// CLD_EXITED or CLD_KILLED, the pid, the uid and the status; with
// WNOHANG and no child that ended, it is zeroed and the result is 0.
func Waitid(idtype uint32, id uint32, infop c23.Ptr, options int32) int32 {
	if options&^(WNOHANG|WSTOPPED|WEXITED|WCONTINUED|WNOWAIT) != 0 || options&(WSTOPPED|WEXITED|WCONTINUED) == 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	pid := int32(-1)
	switch idtype {
	case P_ALL, P_PGID:
	case P_PID:
		pid = int32(id)
		if pid <= 0 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
	default:
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	nohang := options&WNOHANG != 0
	var c *c23.Child
	if options&WEXITED != 0 {
		var e int32
		c, e = reap(pid, nohang, options&WNOWAIT != 0)
		if e != 0 {
			c23.SetErrno(e)
			return -1
		}
	} else if !nohang {
		// Waiting only for stops and continues, which never come.
		if _, e := reap(pid, true, true); e != 0 {
			c23.SetErrno(e)
			return -1
		}
		select {}
	}
	if c23.Addr(infop) != 0 {
		var info c23.SigInfo
		sig := int32(0)
		if c != nil {
			sig = c23.SIGCHLD
			info = c23.SigInfo{Code: CLD_EXITED, Pid: c.Pid, Uid: int32(os.Getuid()), Status: c.Status >> 8 & 0xff}
			if s := c.Status & 0x7f; s != 0 {
				info.Code, info.Status = CLD_KILLED, s
				if c.Status&0x80 != 0 {
					info.Code = CLD_DUMPED
				}
			}
		}
		c23.StoreSigInfo(infop, sig, info)
	}
	return 0
}
