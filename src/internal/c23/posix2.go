// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import ()

// More of POSIX: what SQLite's Unix VFS and shell use.

// unistd.h

// fcntl.h's advisory record locks.

// fcntl's lock commands and struct flock's types: Linux's.
const ()

// sys/ioctl.h

// ioctl's requests: Linux's.
const ()

// stdio.h's popen and pclose

var ()

// time.h's POSIX functions

// clock_gettime's clocks.
const ()

// utime.h, sys/time.h's utimes

// pwd.h

var ()

// sys/resource.h

const ()

// sys/mman.h

const (
	PROT_NONE  = 0
	PROT_READ  = 1
	PROT_WRITE = 2
	PROT_EXEC  = 4
)

// Shared file mappings: within the process, every mapping of the same
// region of the same file is the same memory, read from the file when
// first mapped. What is written to it does not reach the file, and other
// processes do not share it: enough for a file used only to share memory
// in a process, as SQLite's WAL index is when one process uses it.
var ()
