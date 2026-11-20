// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"runtime"
	"syscall"
)

// The file of a GOOS=portable program is a Windows executable that is a shell
// script for the other hosts (see cmd/internal/portpack), which their
// kernels do not run: a shell has to. So that a GOOS=portable program can
// start GOOS=portable programs (itself, for one) as it starts others,
// [StartProcess] has the shell run a file the system says is no
// executable, if it is such a file, and [Executable] is the file the
// script says it is, not the loader or the copy the script ran.

// portableFile returns the name of the program's file, as the script at its
// start said it, or "".
func portableFile() string // in package runtime

// portableShell returns the shell and its arguments to start name with argv,
// which failed with err, if name is the file of a GOOS=portable program.
func portableShell(name string, argv []string, attr *ProcAttr, err error) (string, []string) {
	if runtime.GOOS == "windows" || !errors.Is(err, syscall.ENOEXEC) {
		return "", nil
	}
	file := name
	if attr != nil && attr.Dir != "" && len(name) > 0 && name[0] != '/' {
		file = attr.Dir + "/" + name
	}
	f, err := Open(file)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	const magic = "MZportable='\n"
	var buf [len(magic)]byte
	if n, _ := f.ReadAt(buf[:], 0); n != len(magic) || string(buf[:]) != magic {
		return "", nil
	}
	sh := []string{"sh", name}
	if len(name) > 0 && name[0] == '-' {
		sh[1] = "./" + name
	}
	if len(argv) > 1 {
		sh = append(sh, argv[1:]...)
	}
	return "/bin/sh", sh
}
