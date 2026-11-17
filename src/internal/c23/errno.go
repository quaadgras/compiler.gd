// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"errors"
	"io/fs"
	"reflect"
	"strconv"
	"sync"
)

// errno.h's values: Linux's, which glibc and musl use.
const (
	EPERM        = 1
	ENOENT       = 2
	ESRCH        = 3
	EINTR        = 4
	EIO          = 5
	E2BIG        = 7
	EBADF        = 9
	ECHILD       = 10
	EAGAIN       = 11
	ENOMEM       = 12
	EACCES       = 13
	EBUSY        = 16
	EEXIST       = 17
	EXDEV        = 18
	ENOTDIR      = 20
	EISDIR       = 21
	EINVAL       = 22
	EMFILE       = 24
	ENOSPC       = 28
	ESPIPE       = 29
	EROFS        = 30
	EPIPE        = 32
	EDOM         = 33
	ERANGE       = 34
	ENAMETOOLONG = 36
	ENOTEMPTY    = 39
	EILSEQ       = 84
)

// errnoText are glibc's messages for the errno values.
var errnoText = map[int32]string{
	0:            "Success",
	EPERM:        "Operation not permitted",
	ENOENT:       "No such file or directory",
	ESRCH:        "No such process",
	EINTR:        "Interrupted system call",
	EIO:          "Input/output error",
	E2BIG:        "Argument list too long",
	EBADF:        "Bad file descriptor",
	ECHILD:       "No child processes",
	EAGAIN:       "Resource temporarily unavailable",
	ENOMEM:       "Cannot allocate memory",
	EACCES:       "Permission denied",
	EBUSY:        "Device or resource busy",
	EEXIST:       "File exists",
	EXDEV:        "Invalid cross-device link",
	ENOTDIR:      "Not a directory",
	EISDIR:       "Is a directory",
	EINVAL:       "Invalid argument",
	EMFILE:       "Too many open files",
	ENOSPC:       "No space left on device",
	ESPIPE:       "Illegal seek",
	EROFS:        "Read-only file system",
	EPIPE:        "Broken pipe",
	EDOM:         "Numerical argument out of domain",
	ERANGE:       "Numerical result out of range",
	ENAMETOOLONG: "File name too long",
	ENOTEMPTY:    "Directory not empty",
	EILSEQ:       "Invalid or incomplete multibyte or wide character",
}

var (
	strerrorMu  sync.Mutex
	strerrorMsg = map[int32]Ptr{}
)

func strerror(e int32) string {
	if s, ok := errnoText[e]; ok {
		return s
	}
	return "Unknown error " + strconv.Itoa(int(e))
}

// Strerror returns e's message, in an array C must not modify.
func Strerror(e int32) Ptr {
	strerrorMu.Lock()
	defer strerrorMu.Unlock()
	p, ok := strerrorMsg[e]
	if !ok {
		p = CString(strerror(e))
		strerrorMsg[e] = p
	}
	return p
}

// setErrnoOf sets errno to err's: its system error number (a
// syscall.Errno) if it has one.
func setErrnoOf(err error) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		t := reflect.TypeOf(e)
		if t.PkgPath() == "syscall" && t.Name() == "Errno" && t.Kind() == reflect.Uintptr {
			SetErrno(int32(reflect.ValueOf(e).Uint()))
			return
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		SetErrno(ENOENT)
	case errors.Is(err, fs.ErrExist):
		SetErrno(EEXIST)
	case errors.Is(err, fs.ErrPermission):
		SetErrno(EACCES)
	case errors.Is(err, fs.ErrInvalid):
		SetErrno(EINVAL)
	default:
		SetErrno(EIO)
	}
}
