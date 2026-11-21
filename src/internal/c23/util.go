// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"path/filepath"
	_ "unsafe" // for go:linkname
)

// The common extensions of glibc and musl: getline, asprintf, err.h,
// libgen.h, malloc.h, sys/random.h, sys/uio.h, sys/utsname.h, grp.h,
// syslog.h, langinfo.h and others.

// progname is glibc's program_invocation_short_name (musl's __progname):
// the last element of argv[0].
func Progname() string {
	if len(os.Args) == 0 {
		return ""
	}
	return filepath.Base(os.Args[0])
}

// stdio.h

// string.h

// stdlib.h

// err.h

// libgen.h

var ()

// malloc.h

// sys/random.h

// RuntimeRand is the runtime's random number generator: ChaCha8, seeded
// from the system's entropy, which math/rand/v2's top-level functions use.
//
//go:linkname RuntimeRand runtime.rand
func RuntimeRand() uint64

// sys/uio.h

// sys/utsname.h

// grp.h

var ()

// syslog.h: there is no system logger to send to. Messages go to stderr
// with openlog's LOG_PERROR, as "ident[pid]: message", as glibc and musl
// write them there, and are otherwise dropped.

const ()

// langinfo.h

var ()

// sys/file.h

const ()
