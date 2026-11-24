// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	c23_string "internal/c23/string"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

func Setenv(name, value *c23.Void, overwrite int32) int32 {
	n := c23.GoString(name)
	if !envName(n) {
		return -1
	}
	if _, ok := os.LookupEnv(n); ok && overwrite == 0 {
		return 0
	}
	err := os.Setenv(n, c23.GoString(value))
	envChanged()
	return c23.ErrRet(err)
}

func Unsetenv(name *c23.Void) int32 {
	n := c23.GoString(name)
	if !envName(n) {
		return -1
	}
	err := os.Unsetenv(n)
	envChanged()
	return c23.ErrRet(err)
}

// envName reports whether n may name an environment variable: not empty
// and without '=' (errno EINVAL).
func envName(n string) bool {
	if n == "" || slices.Contains([]byte(n), '=') {
		c23.SetErrno(c23.EINVAL)
		return false
	}
	return true
}

var mkstempMu sync.Mutex

var mkstempN uint64

// Mkstemp is mkstemp: it replaces the XXXXXX that ends template with what
// makes the name of a new file, which it creates (mode 0600) and opens.
func Mkstemp(template *c23.Void) int32 {
	n := c23_string.Strlen(template)
	if n < 6 || c23.GoString(c23.Add(template, int(n-6))) != "XXXXXX" {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	suffix := c23.Add(template, int(n-6))
	const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for range 238328 { // TMP_MAX
		mkstempMu.Lock()
		mkstempN++
		x := uint64(os.Getpid())*2654435761 + mkstempN*0x9e3779b97f4a7c15 + uint64(time.Now().UnixNano())
		mkstempMu.Unlock()
		for i := range 6 {
			c23.Store8(c23.Add(suffix, i), digits[x%62])
			x /= 62
		}
		fd := c23.Open(c23.GoString(template), c23.O_RDWR|c23.O_CREAT|c23.O_EXCL, 0o600)
		if fd >= 0 || c23.Errno() != c23.EEXIST {
			return fd
		}
	}
	c23.SetErrno(c23.EEXIST)
	return -1
}

// Realpath is realpath: the absolute name of an existing file, without
// symbolic links, ., .. or extra slashes, at resolved or, if it is null,
// in an array realpath allocates.
func Realpath(path, resolved *c23.Void) *c23.Void {
	name := c23.GoString(path)
	if _, err := os.Stat(name); err != nil {
		c23.SetErrnoOf(err)
		return (*c23.Void)(nil)
	}
	abs, err := filepath.Abs(name)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		c23.SetErrnoOf(err)
		return (*c23.Void)(nil)
	}
	if len(abs) >= 4096 { // PATH_MAX
		c23.SetErrno(c23.ENAMETOOLONG)
		return (*c23.Void)(nil)
	}
	if c23.Addr(resolved) == 0 {
		return c23.CString(abs)
	}
	copy(c23.BytesAt(resolved, uintptr(len(abs)+1)), abs+"\x00")
	return resolved
}

// PosixMemalign is posix_memalign: aligned_alloc, for an alignment that
// is a power of two and a multiple of sizeof(void *), into *memptr.
func PosixMemalign(memptr *c23.Void, align, size uint64) int32 {
	if align < 8 || align&(align-1) != 0 {
		return c23.EINVAL
	}
	c23.StorePtr(memptr, c23.AlignedAlloc(align, size))
	return 0
}
