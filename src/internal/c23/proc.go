// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
)

// Processes: sys/wait.h, spawn.h and unistd.h's fork and exec family.
//
// A Go program cannot fork (its runtime's threads would not be copied),
// so fork fails with ENOSYS and processes start with posix_spawn, as
// os/exec starts them. The children are the program's own: wait and
// waitpid know those posix_spawn started (not popen's or system's).

// waitpid's options: Linux's.
const (
	WNOHANG    = 1
	WUNTRACED  = 2
	WCONTINUED = 8
)

// A child is a process posix_spawn started.
type child struct {
	cmd    *exec.Cmd
	pid    int32
	done   bool  // it ended, and waitpid may report it
	seq    int   // the order in which children ended
	status int32 // as wait reports it
}

var (
	childMu   sync.Mutex
	childCond = sync.NewCond(&childMu)
	children  = map[int32]*child{}
	childSeq  int
)

// startChild starts cmd as a child of the program. When it ends, the
// program receives SIGCHLD; if SIGCHLD is ignored (SIG_IGN or
// SA_NOCLDWAIT), wait does not report it, as Linux.
func startChild(cmd *exec.Cmd) (int32, error) {
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	c := &child{cmd: cmd, pid: int32(cmd.Process.Pid)}
	childMu.Lock()
	children[c.pid] = c
	childMu.Unlock()
	go func() {
		st := waitStatus(cmd, cmd.Wait())
		sigMu.Lock()
		reap := Addr(sigActs[SIGCHLD].handler) == sigIgn || sigActs[SIGCHLD].flags&SA_NOCLDWAIT != 0
		sigMu.Unlock()
		info := sigInfo{code: CLD_EXITED, pid: c.pid, status: st >> 8 & 0xff}
		if st&0x7f != 0 {
			info.code, info.status = CLD_KILLED, st&0x7f
		}
		childMu.Lock()
		childSeq++
		c.done, c.status, c.seq = true, st, childSeq
		if reap {
			delete(children, c.pid)
		}
		// SIGCHLD is sent before waitpid returns the child, as Linux
		// does. sigSendProcess without a sending thread runs no handler
		// itself.
		sigSendProcess(SIGCHLD, info, nil)
		childCond.Broadcast()
		childMu.Unlock()
	}()
	return c.pid, nil
}

// Waitpid is waitpid: for pid -1 (or 0, or a process group: any child
// posix_spawn started) or one child. It reports only children that
// ended, with Linux's status: the exit status times 256, or the signal
// that ended it. WUNTRACED and WCONTINUED are accepted, and report
// nothing. It is not interrupted by signals.
func Waitpid(pid int32, status Ptr, options int32) int32 {
	if options&^(WNOHANG|WUNTRACED|WCONTINUED) != 0 {
		SetErrno(EINVAL)
		return -1
	}
	childMu.Lock()
	defer childMu.Unlock()
	for {
		var found *child
		any := false
		for _, c := range children {
			if pid > 0 && c.pid != pid {
				continue
			}
			any = true
			if c.done && (found == nil || c.seq < found.seq) {
				found = c
			}
		}
		if found != nil {
			delete(children, found.pid)
			if Addr(status) != 0 {
				Store32(status, uint32(found.status))
			}
			return found.pid
		}
		if !any {
			SetErrno(ECHILD)
			return -1
		}
		if options&WNOHANG != 0 {
			return 0
		}
		childCond.Wait()
	}
}

// Wait is wait.
func Wait(status Ptr) int32 { return Waitpid(-1, status, 0) }

// goStrings reads a null-terminated array of strings, such as argv.
func goStrings(p Ptr) []string {
	var s []string
	if Addr(p) == 0 {
		return nil
	}
	for i := 0; ; i++ {
		q := LoadPtr(Add(p, i*int(wordSize)))
		if Addr(q) == 0 {
			return s
		}
		s = append(s, GoString(q))
	}
}

// File actions and attributes are kept by the address of the C object.

type spawnAction struct {
	op        byte // 'o' open, 'c' close, 'd' dup2
	fd, newfd int32
	path      string
	oflag     int32
	mode      uint32
}

type spawnActions struct {
	mu   sync.Mutex
	list []spawnAction
}

// A spawnAttr is a posix_spawnattr_t.
type spawnAttr struct {
	mu                 sync.Mutex
	flags              int16
	pgroup             int32
	sigmask, sigdef    uint64
	policy, schedPrior int32
}

var (
	spawnActs  sync.Map // uintptr -> *spawnActions
	spawnAttrs sync.Map // uintptr -> *spawnAttr
)

func spawnActsOf(p Ptr) *spawnActions {
	check(p, 4)
	a, _ := spawnActs.LoadOrStore(Addr(p), new(spawnActions))
	return a.(*spawnActions)
}

func spawnAttrOf(p Ptr) *spawnAttr {
	check(p, 4)
	a, _ := spawnAttrs.LoadOrStore(Addr(p), new(spawnAttr))
	return a.(*spawnAttr)
}

func PosixSpawnFileActionsInit(fa Ptr) int32 {
	check(fa, 4)
	spawnActs.Store(Addr(fa), new(spawnActions))
	return 0
}

func PosixSpawnFileActionsDestroy(fa Ptr) int32 {
	spawnActs.Delete(Addr(fa))
	return 0
}

func (a *spawnActions) add(act spawnAction) int32 {
	if act.fd < 0 || act.op == 'd' && act.newfd < 0 {
		return EBADF
	}
	a.mu.Lock()
	a.list = append(a.list, act)
	a.mu.Unlock()
	return 0
}

func PosixSpawnFileActionsAddopen(fa Ptr, fd int32, path Ptr, oflag int32, mode uint32) int32 {
	return spawnActsOf(fa).add(spawnAction{op: 'o', fd: fd, path: GoString(path), oflag: oflag, mode: mode})
}

func PosixSpawnFileActionsAddclose(fa Ptr, fd int32) int32 {
	return spawnActsOf(fa).add(spawnAction{op: 'c', fd: fd})
}

func PosixSpawnFileActionsAdddup2(fa Ptr, fd, newfd int32) int32 {
	return spawnActsOf(fa).add(spawnAction{op: 'd', fd: fd, newfd: newfd})
}

// posix_spawnattr_setflags's flags: glibc's.
const (
	POSIX_SPAWN_RESETIDS      = 0x01
	POSIX_SPAWN_SETPGROUP     = 0x02
	POSIX_SPAWN_SETSIGDEF     = 0x04
	POSIX_SPAWN_SETSIGMASK    = 0x08
	POSIX_SPAWN_SETSCHEDPARAM = 0x10
	POSIX_SPAWN_SETSCHEDULER  = 0x20
	POSIX_SPAWN_USEVFORK      = 0x40
	POSIX_SPAWN_SETSID        = 0x80
)

func PosixSpawnattrInit(attr Ptr) int32 {
	check(attr, 4)
	spawnAttrs.Store(Addr(attr), new(spawnAttr))
	return 0
}

func PosixSpawnattrDestroy(attr Ptr) int32 {
	spawnAttrs.Delete(Addr(attr))
	return 0
}

// PosixSpawnattrSetflags is posix_spawnattr_setflags. Of the flags,
// POSIX_SPAWN_SETPGROUP and POSIX_SPAWN_SETSID are honored where the
// system has them (syscall.SysProcAttr's Setpgid and Setsid); the others
// are accepted and ignored: the child starts with the default actions
// and an empty mask, and the program's ids and scheduling.
func PosixSpawnattrSetflags(attr Ptr, flags int16) int32 {
	if flags&^0xff != 0 {
		return EINVAL
	}
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.flags = flags
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetflags(attr, flags Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	Store16(flags, uint16(a.flags))
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetpgroup(attr Ptr, pgroup int32) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.pgroup = pgroup
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetpgroup(attr, pgroup Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	Store32(pgroup, uint32(a.pgroup))
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetsigmask(attr, set Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.sigmask = loadSigset(set)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetsigmask(attr, set Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	storeSigset(set, a.sigmask)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetsigdefault(attr, set Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.sigdef = loadSigset(set)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetsigdefault(attr, set Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	storeSigset(set, a.sigdef)
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrSetschedpolicy(attr Ptr, policy int32) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.policy = policy
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetschedpolicy(attr, policy Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	Store32(policy, uint32(a.policy))
	a.mu.Unlock()
	return 0
}

// The struct sched_param of the attribute: its int sched_priority.
func PosixSpawnattrSetschedparam(attr, param Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	a.schedPrior = int32(Load32(param))
	a.mu.Unlock()
	return 0
}

func PosixSpawnattrGetschedparam(attr, param Ptr) int32 {
	a := spawnAttrOf(attr)
	a.mu.Lock()
	Store32(param, uint32(a.schedPrior))
	a.mu.Unlock()
	return 0
}

// PosixSpawn is posix_spawn: path runs with the arguments argv and the
// environment envp (the program's, if null). The child's descriptors are
// the program's own (its table, not the system's: the numbers C knows),
// those not close-on-exec, after file_actions: 0, 1 and 2 are its
// standard files and the others its os/exec ExtraFiles. The result is 0,
// or an errno value (ENOENT if path does not exist, as glibc).
func PosixSpawn(pid, path, fa, attr, argv, envp Ptr) int32 {
	return spawnProc(pid, GoString(path), false, fa, attr, argv, envp)
}

// PosixSpawnp is posix_spawnp: a file without a slash is found in PATH.
func PosixSpawnp(pid, file, fa, attr, argv, envp Ptr) int32 {
	return spawnProc(pid, GoString(file), true, fa, attr, argv, envp)
}

func spawnProc(pidp Ptr, path string, search bool, fa, attr, argv, envp Ptr) int32 {
	var acts []spawnAction
	if Addr(fa) != 0 {
		a := spawnActsOf(fa)
		a.mu.Lock()
		acts = append(acts, a.list...)
		a.mu.Unlock()
	}
	cmd, owned, e := command(path, search, goStrings(argv), envp, acts)
	defer func() {
		for _, f := range owned {
			f.Close()
		}
	}()
	if e != 0 {
		return e
	}
	if Addr(attr) != 0 {
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
		return errnoOf(err)
	}
	if Addr(pidp) != 0 {
		Store32(pidp, uint32(pid))
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

// errnoOf is the errno value of err.
func errnoOf(err error) int32 {
	old := Errno()
	setErrnoOf(err)
	e := Errno()
	SetErrno(old)
	return e
}

// command makes the exec.Cmd of a child that runs path (found in PATH if
// search) with args and the environment envp, whose descriptors are the
// program's after acts. owned are the files the actions opened, which
// the caller closes once the child started; e is an errno value.
func command(path string, search bool, args []string, envp Ptr, acts []spawnAction) (cmd *exec.Cmd, owned []*os.File, e int32) {
	if path == "" {
		return nil, nil, ENOENT
	}
	prog := path
	if search && !strings.Contains(path, "/") {
		lp, err := exec.LookPath(path)
		if err != nil && !errors.Is(err, exec.ErrDot) {
			return nil, nil, ENOENT
		}
		prog = lp
	}
	cmd = &exec.Cmd{Path: prog, Args: args}
	if len(args) == 0 {
		cmd.Args = []string{""}
	}
	if Addr(envp) != 0 {
		cmd.Env = goStrings(envp)
		if cmd.Env == nil {
			cmd.Env = []string{}
		}
	}
	type cfd struct {
		f       *os.File
		cloexec bool
	}
	fds := map[int32]cfd{}
	maxfd := int32(-1)
	fdMu.Lock()
	for i, e := range fdTab {
		if e != nil {
			fds[int32(i)] = cfd{e.of.f, e.cloexec}
		}
	}
	fdMu.Unlock()
	for _, a := range acts {
		switch a.op {
		case 'o':
			f, err := os.OpenFile(a.path, goOpenFlags(a.oflag), goMode(a.mode))
			if err != nil {
				return nil, owned, errnoOf(err)
			}
			owned = append(owned, f)
			fds[a.fd] = cfd{f, a.oflag&O_CLOEXEC != 0}
		case 'c':
			delete(fds, a.fd)
		case 'd':
			f, ok := fds[a.fd]
			if !ok {
				return nil, owned, EBADF
			}
			fds[a.newfd] = cfd{f.f, false}
		}
	}
	for fd, f := range fds {
		if f.cloexec {
			delete(fds, fd)
		} else {
			maxfd = max(maxfd, fd)
		}
	}
	if f, ok := fds[0]; ok {
		cmd.Stdin = f.f
	}
	if f, ok := fds[1]; ok {
		cmd.Stdout = f.f
	}
	if f, ok := fds[2]; ok {
		cmd.Stderr = f.f
	}
	for fd := int32(3); fd <= maxfd; fd++ {
		cmd.ExtraFiles = append(cmd.ExtraFiles, fds[fd].f)
	}
	return cmd, owned, 0
}

// goOpenFlags are os.OpenFile's flags of open's.
func goOpenFlags(flags int32) int {
	flag := os.O_RDONLY
	switch flags & O_ACCMODE {
	case O_WRONLY:
		flag = os.O_WRONLY
	case O_RDWR:
		flag = os.O_RDWR
	}
	for _, m := range [...]struct {
		c int32
		g int
	}{{O_CREAT, os.O_CREATE}, {O_EXCL, os.O_EXCL}, {O_TRUNC, os.O_TRUNC}, {O_APPEND, os.O_APPEND}, {O_SYNC, os.O_SYNC}} {
		if flags&m.c == m.c {
			flag |= m.g
		}
	}
	return flag
}

// Fork is fork, which a Go program cannot do: its runtime's threads, and
// so its goroutines, would not be in the child. It fails with ENOSYS;
// posix_spawn starts a process.
func Fork() int32 {
	SetErrno(ENOSYS)
	return -1
}

// The exec family cannot replace the program, which Go's runtime does
// not allow: it is emulated. The program runs as a child, with the
// program's descriptors (those not close-on-exec) and the environment
// envp, and the program waits for it and ends with its status (128+sig
// if a signal ended it), as _exit does: the functions atexit registered
// do not run, and C's streams are not flushed. What is not as POSIX says:
// the program has a new pid, its other threads run until the child ends,
// and the signals the process receives are its handlers' (its
// handlers do not become the defaults). If the program cannot run, the
// result is -1 with errno set, as exec's.
func execProc(path string, search bool, args []string, envp Ptr) int32 {
	cmd, _, e := command(path, search, args, envp, nil)
	if e != 0 {
		SetErrno(e)
		return -1
	}
	if err := cmd.Start(); err != nil {
		setErrnoOf(err)
		return -1
	}
	st := waitStatus(cmd, cmd.Wait())
	if sig := st & 0x7f; sig != 0 {
		os.Exit(128 + int(sig))
	}
	os.Exit(int(st >> 8 & 0xff))
	return 0
}

func Execv(path, argv Ptr) int32 { return execProc(GoString(path), false, goStrings(argv), Ptr{}) }

func Execve(path, argv, envp Ptr) int32 {
	return execProc(GoString(path), false, goStrings(argv), envOrEmpty(envp))
}

func Execvp(file, argv Ptr) int32 { return execProc(GoString(file), true, goStrings(argv), Ptr{}) }

func Execvpe(file, argv, envp Ptr) int32 {
	return execProc(GoString(file), true, goStrings(argv), envOrEmpty(envp))
}

// envOrEmpty is envp, or, if it is null, an empty environment (execve's
// null envp is not the program's environment).
func envOrEmpty(envp Ptr) Ptr {
	if Addr(envp) == 0 {
		return Calloc(1, uint64(wordSize))
	}
	return envp
}

// execlArgs reads execl's arguments: arg, then va up to a null pointer;
// with env, execle's envp after it.
func execlArgs(arg Ptr, va []any, env bool) (args []string, envp Ptr) {
	ap := VaStart(va)
	for p := arg; Addr(p) != 0; p = VaArg[Ptr](ap) {
		args = append(args, GoString(p))
	}
	if env {
		envp = envOrEmpty(VaArg[Ptr](ap))
	}
	return args, envp
}

func Execl(path, arg Ptr, va ...any) int32 {
	args, _ := execlArgs(arg, va, false)
	return execProc(GoString(path), false, args, Ptr{})
}

func Execlp(file, arg Ptr, va ...any) int32 {
	args, _ := execlArgs(arg, va, false)
	return execProc(GoString(file), true, args, Ptr{})
}

func Execle(path, arg Ptr, va ...any) int32 {
	args, envp := execlArgs(arg, va, true)
	return execProc(GoString(path), false, args, envp)
}

// Process groups and sessions, where the system has them (proc_unix.go).

func Getpgrp() int32 { return getpgrp() }

func Getpgid(pid int32) int32 {
	g, err := getpgid(pid)
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	return g
}

func Getsid(pid int32) int32 {
	s, err := getsid(pid)
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	return s
}

func Setpgid(pid, pgid int32) int32 { return errRet(setpgid(pid, pgid)) }

func Setsid() int32 {
	s, err := setsid()
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	return s
}
