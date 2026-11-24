// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"path/filepath"
	"strings"
)

// The *at functions (openat, fstatat, unlinkat and the others) name files
// relative to a directory descriptor. Go's os.Root resolves names in a
// directory too, but it keeps them in it (no "..", no symbolic link out
// of it), which POSIX does not; so a name relative to a descriptor is
// resolved against the name of the descriptor's directory, as it was
// opened (made absolute then: Ofile's Dir). What differs from POSIX: a
// descriptor of a directory that is renamed or removed after it is
// opened still names the directory of that name.

// The *at functions' constants (fcntl.h, Linux's).
const (
	AT_FDCWD            = -100
	AT_SYMLINK_NOFOLLOW = 0x100
	AT_REMOVEDIR        = 0x200
	AT_EACCESS          = 0x200
	AT_SYMLINK_FOLLOW   = 0x400
	AT_EMPTY_PATH       = 0x1000
)

// AtPath is the name of path relative to the directory of the descriptor
// dirfd (the working directory if dirfd is AT_FDCWD), or "" and errno's
// value: EBADF if dirfd is not a descriptor, ENOTDIR if it is not of a
// directory, ENOENT if path is empty. An absolute path is itself.
func AtPath(dirfd int32, path string) (string, int32) {
	if path == "" {
		return "", ENOENT
	}
	if dirfd == AT_FDCWD || filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return path, 0
	}
	dir, e := FdDirName(dirfd)
	if e != 0 {
		return "", e
	}
	return strings.TrimSuffix(dir, string(os.PathSeparator)) + string(os.PathSeparator) + path, 0
}

// FdDirName is the name of the directory fd is a descriptor of, or ""
// and errno's value (EBADF, ENOTDIR).
func FdDirName(fd int32) (string, int32) {
	of := FdFile(fd)
	if of == nil {
		return "", EBADF
	}
	if of.F == nil {
		return "", ENOTDIR
	}
	if fi, err := of.F.Stat(); err != nil || !fi.IsDir() {
		return "", ENOTDIR
	}
	if of.Dir != "" {
		return of.Dir, 0
	}
	return of.F.Name(), 0
}

// AtName is AtPath of the C string path, setting errno if it fails.
func AtName(dirfd int32, path *Void) (string, bool) {
	name, e := AtPath(dirfd, GoString(path))
	if e != 0 {
		SetErrno(e)
		return "", false
	}
	return name, true
}
