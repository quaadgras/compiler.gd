// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package spawn

import (
	c23 "internal/c23"
	"os/exec"
	"reflect"
	"sync"
)

var childSeq int

// startChild starts cmd as a child of the program. When it ends, the
// program receives SIGCHLD; if SIGCHLD is ignored (SIG_IGN or
// SA_NOCLDWAIT), wait does not report it, as Linux.
func startChild(cmd *exec.Cmd) (int32, error) {
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	c := &c23.Child{Cmd: cmd, Pid: int32(cmd.Process.Pid)}
	c23.ChildMu.Lock()
	c23.Children[c.Pid] = c
	c23.ChildMu.Unlock()
	go func() {
		st := c23.WaitStatus(cmd, cmd.Wait())
		c23.SigMu.Lock()
		reap := c23.Addr(c23.SigActs[c23.SIGCHLD].Handler) == c23.SigIgn || c23.SigActs[c23.SIGCHLD].Flags&SA_NOCLDWAIT != 0
		c23.SigMu.Unlock()
		info := c23.SigInfo{Code: CLD_EXITED, Pid: c.Pid, Status: st >> 8 & 0xff}
		if st&0x7f != 0 {
			info.Code, info.Status = CLD_KILLED, st&0x7f
		}
		c23.ChildMu.Lock()
		childSeq++
		c.Done, c.Status, c.Seq = true, st, childSeq
		if reap {
			delete(c23.Children, c.Pid)
		}
		// SIGCHLD is sent before waitpid returns the child, as Linux
		// does. sigSendProcess without a sending thread runs no handler
		// itself.
		c23.SigSendProcess(c23.SIGCHLD, info, nil)
		c23.ChildCond.Broadcast()
		c23.ChildMu.Unlock()
	}()
	return c.Pid, nil
}

type spawnActions struct {
	mu   sync.Mutex
	list []c23.SpawnAction
}

// A spawnAttr is a posix_spawnattr_t.
type spawnAttr struct {
	mu                 sync.Mutex
	flags              int16
	pgroup             int32
	sigmask, sigdef    uint64
	policy, schedPrior int32
}

var spawnActs sync.Map // uintptr -> *spawnActions

var spawnAttrs sync.Map // uintptr -> *spawnAttr

func spawnActsOf(p *c23.Void) *spawnActions {
	c23.Check(p, 4)
	a, _ := spawnActs.LoadOrStore(c23.Addr(p), new(spawnActions))
	return a.(*spawnActions)
}

func spawnAttrOf(p *c23.Void) *spawnAttr {
	c23.Check(p, 4)
	a, _ := spawnAttrs.LoadOrStore(c23.Addr(p), new(spawnAttr))
	return a.(*spawnAttr)
}

func PosixSpawnFileActionsInit(fa *c23.Void) int32 {
	c23.Check(fa, 4)
	spawnActs.Store(c23.Addr(fa), new(spawnActions))
	return 0
}

func PosixSpawnFileActionsDestroy(fa *c23.Void) int32 {
	spawnActs.Delete(c23.Addr(fa))
	return 0
}

func (a *spawnActions) add(act c23.SpawnAction) int32 {
	if act.Fd < 0 || act.Op == 'd' && act.Newfd < 0 {
		return c23.EBADF
	}
	a.mu.Lock()
	a.list = append(a.list, act)
	a.mu.Unlock()
	return 0
}

func PosixSpawnFileActionsAddopen(fa *c23.Void, fd int32, path *c23.Void, oflag int32, mode uint32) int32 {
	return spawnActsOf(fa).add(c23.SpawnAction{Op: 'o', Fd: fd, Path: c23.GoString(path), Oflag: oflag, Mode: mode})
}

func PosixSpawnFileActionsAddclose(fa *c23.Void, fd int32) int32 {
	return spawnActsOf(fa).add(c23.SpawnAction{Op: 'c', Fd: fd})
}

func PosixSpawnFileActionsAdddup2(fa *c23.Void, fd, newfd int32) int32 {
	return spawnActsOf(fa).add(c23.SpawnAction{Op: 'd', Fd: fd, Newfd: newfd})
}

const POSIX_SPAWN_SETPGROUP = 0x02

const POSIX_SPAWN_SETSID = 0x80

func PosixSpawnattrInit(attr *c23.Void) int32 {
	c23.Check(attr, 4)
	spawnAttrs.Store(c23.Addr(attr), new(spawnAttr))
	return 0
}

func PosixSpawnattrDestroy(attr *c23.Void) int32 {
	spawnAttrs.Delete(c23.Addr(attr))
	return 0
}

// PosixSpawnattrSetflags is posix_spawnattr_setflags. Of the flags,
// POSIX_SPAWN_SETPGROUP and POSIX_SPAWN_SETSID are honored where the
// system has them (syscall.SysProcAttr's Setpgid and Setsid); the others
// are accepted and ignored: the child starts with the default actions
// and an empty mask, and the program's ids and scheduling.
func PosixSpawnattrSetflags(attr *c23.Void, flags int16) int32 {
	if flags&^0xff != 0 {
		return c23.EINVAL
	}
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.flags = flags
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetflags(attr, flags *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	c23.Store16(flags, uint16(a.flags))
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetpgroup(attr *c23.Void, pgroup int32) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.pgroup = pgroup
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetpgroup(attr, pgroup *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	c23.Store32(pgroup, uint32(a.pgroup))
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetsigmask(attr, set *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.sigmask = c23.LoadSigset(set)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetsigmask(attr, set *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	c23.StoreSigset(set, a.sigmask)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetsigdefault(attr, set *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.sigdef = c23.LoadSigset(set)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetsigdefault(attr, set *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	c23.StoreSigset(set, a.sigdef)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetschedpolicy(attr *c23.Void, policy int32) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.policy = policy
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetschedpolicy(attr, policy *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	c23.Store32(policy, uint32(a.policy))
	a.mu.Unlock()
	return 0
}

// The struct sched_param of the attribute: its int sched_priority.
func PosixSpawnattrSetschedparam(attr, param *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.schedPrior = int32(c23.Load32(param))
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetschedparam(attr, param *c23.Void) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	c23.Store32(param, uint32(a.schedPrior))
	a.mu.Unlock()
	return 0
}

// PosixSpawn is posix_spawn: path runs with the arguments argv and the
// environment envp (the program's, if null). The child's descriptors are
// the program's own (its table, not the system's: the numbers C knows),
// those not close-on-exec, after file_actions: 0, 1 and 2 are its
// standard files and the others its os/exec ExtraFiles. The result is 0,
// or an errno value (ENOENT if path does not exist, as glibc).
func PosixSpawn(pid, path, fa, attr, argv, envp *c23.Void) int32 {
	return spawnProc(pid, c23.GoString(path), false, fa, attr, argv, envp)
}

// PosixSpawnp is posix_spawnp: a file without a slash is found in PATH.
func PosixSpawnp(pid, file, fa, attr, argv, envp *c23.Void) int32 {
	return spawnProc(pid, c23.GoString(file), true, fa, attr, argv, envp)
}

func spawnProc(pidp *c23.Void, path string, search bool, fa, attr, argv, envp *c23.Void) int32 {
	var acts []c23.SpawnAction
	if c23.Addr(fa) != 0 {
		a := spawnActsOf(fa)
		a.mu.Lock()
		acts = append(acts, a.list...)
		a.mu.Unlock()
	}
	cmd, owned, e := c23.Command(path, search, c23.GoStrings(argv), envp, acts)
	defer func() {
		for _, f := range owned {
			f.Close()
		}
	}()
	if e != 0 {
		return e
	}
	if c23.Addr(attr) != 0 {
		a := spawnAttrOf(attr)
		a.mu.Lock()
		flags, pgroup := a.flags, a.pgroup
		a.mu.Unlock()
		if flags&POSIX_SPAWN_SETPGROUP != 0 {
			setSysProcAttr(cmd, "Setpgid", true)
			setSysProcAttr(cmd, "Pgid", int64(pgroup))
		}
		if flags&POSIX_SPAWN_SETSID != 0 {
			setSysProcAttr(cmd, "Setsid", true)
		}
	}
	pid, err := startChild(cmd)
	if err != nil {
		return c23.ErrnoOf(err)
	}
	if c23.Addr(pidp) != 0 {
		c23.Store32(pidp, uint32(pid))
	}
	return 0
}

// setSysProcAttr sets cmd.SysProcAttr's field name, where the system's
// syscall.SysProcAttr has it: by reflection, so that the package does
// not import syscall.
func setSysProcAttr(cmd *exec.Cmd, name string, v any) {
	p := reflect.ValueOf(&cmd.SysProcAttr).Elem()
	if p.IsNil() {
		p.Set(reflect.New(p.Type().Elem()))
	}
	f := p.Elem().FieldByName(name)
	if !f.IsValid() || !f.CanSet() {
		return
	}
	switch x := v.(type) {
	case bool:
		if f.Kind() == reflect.Bool {
			f.SetBool(x)
		}
	case int64:
		if f.CanInt() {
			f.SetInt(x)
		}
	}
}
