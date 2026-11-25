// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"os"
)

// Fork is fork, which a Go program cannot do: its runtime's threads, and
// so its goroutines, would not be in the child. It fails with ENOSYS;
// posix_spawn starts a process.
func Fork() int32 {
	c23.SetErrno(c23.ENOSYS)
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
func execProc(path string, search bool, args []string, envp *c23.Void) int32 {
	cmd, _, e := c23.Command(path, search, args, envp, nil)
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	if err := cmd.Start(); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	st := c23.WaitStatus(cmd, cmd.Wait())
	if sig := st & 0x7f; sig != 0 {
		os.Exit(128 + int(sig))
	}
	os.Exit(int(st >> 8 & 0xff))
	return 0
}

func Execv(path, argv *c23.Void) int32 {
	return execProc(c23.GoString(path), false, c23.GoStrings(argv), (*c23.Void)(nil))
}

func Execve(path, argv, envp *c23.Void) int32 {
	return execProc(c23.GoString(path), false, c23.GoStrings(argv), envOrEmpty(envp))
}

func Execvp(file, argv *c23.Void) int32 {
	return execProc(c23.GoString(file), true, c23.GoStrings(argv), (*c23.Void)(nil))
}

func Execvpe(file, argv, envp *c23.Void) int32 {
	return execProc(c23.GoString(file), true, c23.GoStrings(argv), envOrEmpty(envp))
}

// envOrEmpty is envp, or, if it is null, an empty environment (execve's
// null envp is not the program's environment).
func envOrEmpty(envp *c23.Void) *c23.Void {
	if c23.Addr(envp) == 0 {
		return c23.Calloc(1, uint64(c23.WordSize))
	}
	return envp
}

// execlArgs reads execl's arguments: arg, then va up to a null pointer;
// with env, execle's envp after it.
func execlArgs(arg *c23.Void, va []any, env bool) (args []string, envp *c23.Void) {
	ap := c23.VaStart(va)
	for p := arg; c23.Addr(p) != 0; p = c23.VaArg[*c23.Void](ap) {
		args = append(args, c23.GoString(p))
	}
	if env {
		envp = envOrEmpty(c23.VaArg[*c23.Void](ap))
	}
	return args, envp
}

func Execl(path, arg *c23.Void, va ...any) int32 {
	args, _ := execlArgs(arg, va, false)
	return execProc(c23.GoString(path), false, args, (*c23.Void)(nil))
}

func Execlp(file, arg *c23.Void, va ...any) int32 {
	args, _ := execlArgs(arg, va, false)
	return execProc(c23.GoString(file), true, args, (*c23.Void)(nil))
}

func Execle(path, arg *c23.Void, va ...any) int32 {
	args, envp := execlArgs(arg, va, true)
	return execProc(c23.GoString(path), false, args, envp)
}

func Getpgrp() int32 { return getpgrp() }

func Getpgid(pid int32) int32 {
	g, err := getpgid(pid)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return g
}

func Getsid(pid int32) int32 {
	s, err := getsid(pid)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return s
}

func Setpgid(pid, pgid int32) int32 { return c23.ErrRet(setpgid(pid, pgid)) }

func Setsid() int32 {
	s, err := setsid()
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return s
}
