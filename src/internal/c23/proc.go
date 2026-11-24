// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"errors"
	"os"
	"os/exec"
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
const ()

// A child is a process posix_spawn started.
type Child struct {
	Cmd    *exec.Cmd
	Pid    int32
	Done   bool  // it ended, and waitpid may report it
	Seq    int   // the order in which children ended
	Status int32 // as wait reports it
}

var (
	ChildMu   sync.Mutex
	ChildCond = sync.NewCond(&ChildMu)
	Children  = map[int32]*Child{}
)

// goStrings reads a null-terminated array of strings, such as argv.
func GoStrings(p *Void) []string {
	var s []string
	if Addr(p) == 0 {
		return nil
	}
	for i := 0; ; i++ {
		q := LoadPtr(Add(p, i*int(WordSize)))
		if Addr(q) == 0 {
			return s
		}
		s = append(s, GoString(q))
	}
}

// File actions and attributes are kept by the address of the C object.

type SpawnAction struct {
	Op        byte // 'o' open, 'c' close, 'd' dup2
	Fd, Newfd int32
	Path      string
	Oflag     int32
	Mode      uint32
}

var ()

// posix_spawnattr_setflags's flags: glibc's.
const (
	POSIX_SPAWN_RESETIDS = 0x01

	POSIX_SPAWN_SETSIGDEF     = 0x04
	POSIX_SPAWN_SETSIGMASK    = 0x08
	POSIX_SPAWN_SETSCHEDPARAM = 0x10
	POSIX_SPAWN_SETSCHEDULER  = 0x20
	POSIX_SPAWN_USEVFORK      = 0x40
)

// command makes the exec.Cmd of a child that runs path (found in PATH if
// search) with args and the environment envp, whose descriptors are the
// program's after acts. owned are the files the actions opened, which
// the caller closes once the child started; e is an errno value.
func Command(path string, search bool, args []string, envp *Void, acts []SpawnAction) (cmd *exec.Cmd, owned []*os.File, e int32) {
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
		cmd.Env = GoStrings(envp)
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
	FdMu.Lock()
	for i, e := range FdTab {
		if e != nil {
			fds[int32(i)] = cfd{e.Of.F, e.Cloexec}
		}
	}
	FdMu.Unlock()
	for _, a := range acts {
		switch a.Op {
		case 'o':
			f, err := os.OpenFile(a.Path, goOpenFlags(a.Oflag), GoMode(a.Mode))
			if err != nil {
				return nil, owned, ErrnoOf(err)
			}
			owned = append(owned, f)
			fds[a.Fd] = cfd{f, a.Oflag&O_CLOEXEC != 0}
		case 'c':
			delete(fds, a.Fd)
		case 'd':
			f, ok := fds[a.Fd]
			if !ok {
				return nil, owned, EBADF
			}
			fds[a.Newfd] = cfd{f.f, false}
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

// Process groups and sessions, where the system has them (proc_unix.go).
