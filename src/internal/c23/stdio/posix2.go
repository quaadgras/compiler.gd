// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	c23 "internal/c23"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var popenMu sync.Mutex

var popens = map[*c23.File]*exec.Cmd{}

// Popen is popen: a stream that reads the standard output of the shell's
// command (mode "r") or writes its standard input ("w"). The command's
// other standard files are the program's.
func Popen(command, mode c23.Ptr) c23.Ptr {
	m := c23.GoString(mode)
	rd := strings.HasPrefix(m, "r")
	if !rd && !strings.HasPrefix(m, "w") || strings.Trim(m[1:], "e") != "" {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr{}
	}
	r, w, err := os.Pipe()
	if err != nil {
		c23.SetErrnoOf(err)
		return c23.Ptr{}
	}
	cmd := exec.Command("/bin/sh", "-c", c23.GoString(command))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	mine, theirs := r, w
	if rd {
		cmd.Stdout = w
	} else {
		cmd.Stdin = r
		mine, theirs = w, r
	}
	err = cmd.Start()
	theirs.Close()
	if err != nil {
		mine.Close()
		c23.SetErrnoOf(err)
		return c23.Ptr{}
	}
	p := c23.NewFile(c23.FdNew(mine, c23.If[int32](rd, c23.O_RDONLY, c23.O_WRONLY)), rd, !rd)
	popenMu.Lock()
	popens[c23.FileOf(p)] = cmd
	popenMu.Unlock()
	return p
}

// Pclose is pclose: it closes the stream and waits for its command, whose
// status it returns (as wait does), or -1 (errno ECHILD) for a stream
// popen did not open.
func Pclose(p c23.Ptr) int32 {
	f := c23.FileOf(p)
	popenMu.Lock()
	cmd := popens[f]
	delete(popens, f)
	popenMu.Unlock()
	if cmd == nil {
		c23.SetErrno(c23.ECHILD)
		return -1
	}
	Fclose(p)
	return c23.WaitStatus(cmd, cmd.Wait())
}
