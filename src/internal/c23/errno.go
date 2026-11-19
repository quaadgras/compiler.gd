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
	EPERM           = 1
	ENOENT          = 2
	ESRCH           = 3
	EINTR           = 4
	EIO             = 5
	ENXIO           = 6
	E2BIG           = 7
	ENOEXEC         = 8
	EBADF           = 9
	ECHILD          = 10
	EAGAIN          = 11
	ENOMEM          = 12
	EACCES          = 13
	EFAULT          = 14
	ENOTBLK         = 15
	EBUSY           = 16
	EEXIST          = 17
	EXDEV           = 18
	ENODEV          = 19
	ENOTDIR         = 20
	EISDIR          = 21
	EINVAL          = 22
	ENFILE          = 23
	EMFILE          = 24
	ENOTTY          = 25
	ETXTBSY         = 26
	EFBIG           = 27
	ENOSPC          = 28
	ESPIPE          = 29
	EROFS           = 30
	EMLINK          = 31
	EPIPE           = 32
	EDOM            = 33
	ERANGE          = 34
	EDEADLK         = 35
	ENAMETOOLONG    = 36
	ENOLCK          = 37
	ENOSYS          = 38
	ENOTEMPTY       = 39
	ELOOP           = 40
	EOVERFLOW       = 75
	EILSEQ          = 84
	ENOTSOCK        = 88
	EOPNOTSUPP      = 95
	EADDRINUSE      = 98
	ECONNRESET      = 104
	ETIMEDOUT       = 110
	ECONNREFUSED    = 111
	ENOMSG          = 42
	EIDRM           = 43
	ENOSTR          = 60
	ENODATA         = 61
	ETIME           = 62
	ENOSR           = 63
	ENOLINK         = 67
	EPROTO          = 71
	EMULTIHOP       = 72
	EBADMSG         = 74
	EUSERS          = 87
	EDESTADDRREQ    = 89
	EMSGSIZE        = 90
	EPROTOTYPE      = 91
	ENOPROTOOPT     = 92
	EPROTONOSUPPORT = 93
	ESOCKTNOSUPPORT = 94
	EPFNOSUPPORT    = 96
	EAFNOSUPPORT    = 97
	EADDRNOTAVAIL   = 99
	ENETDOWN        = 100
	ENETUNREACH     = 101
	ENETRESET       = 102
	ECONNABORTED    = 103
	ENOBUFS         = 105
	EISCONN         = 106
	ENOTCONN        = 107
	ESHUTDOWN       = 108
	ETOOMANYREFS    = 109
	EHOSTDOWN       = 112
	EHOSTUNREACH    = 113
	EALREADY        = 114
	EINPROGRESS     = 115
	ESTALE          = 116
	EDQUOT          = 122
	ECANCELED       = 125
	EOWNERDEAD      = 130
	ENOTRECOVERABLE = 131
)

// errnoText are glibc's messages for the errno values.
var errnoText = map[int32]string{
	0:               "Success",
	EPERM:           "Operation not permitted",
	ENOENT:          "No such file or directory",
	ESRCH:           "No such process",
	EINTR:           "Interrupted system call",
	EIO:             "Input/output error",
	ENXIO:           "No such device or address",
	E2BIG:           "Argument list too long",
	ENOEXEC:         "Exec format error",
	EBADF:           "Bad file descriptor",
	ECHILD:          "No child processes",
	EAGAIN:          "Resource temporarily unavailable",
	ENOMEM:          "Cannot allocate memory",
	EACCES:          "Permission denied",
	EFAULT:          "Bad address",
	ENOTBLK:         "Block device required",
	EBUSY:           "Device or resource busy",
	EEXIST:          "File exists",
	EXDEV:           "Invalid cross-device link",
	ENODEV:          "No such device",
	ENOTDIR:         "Not a directory",
	EISDIR:          "Is a directory",
	EINVAL:          "Invalid argument",
	ENFILE:          "Too many open files in system",
	EMFILE:          "Too many open files",
	ENOTTY:          "Inappropriate ioctl for device",
	ETXTBSY:         "Text file busy",
	EFBIG:           "File too large",
	ENOSPC:          "No space left on device",
	ESPIPE:          "Illegal seek",
	EROFS:           "Read-only file system",
	EMLINK:          "Too many links",
	EPIPE:           "Broken pipe",
	EDOM:            "Numerical argument out of domain",
	ERANGE:          "Numerical result out of range",
	EDEADLK:         "Resource deadlock avoided",
	ENAMETOOLONG:    "File name too long",
	ENOLCK:          "No locks available",
	ENOSYS:          "Function not implemented",
	ENOTEMPTY:       "Directory not empty",
	ELOOP:           "Too many levels of symbolic links",
	EOVERFLOW:       "Value too large for defined data type",
	EILSEQ:          "Invalid or incomplete multibyte or wide character",
	ENOTSOCK:        "Socket operation on non-socket",
	EOPNOTSUPP:      "Operation not supported",
	EADDRINUSE:      "Address already in use",
	ECONNRESET:      "Connection reset by peer",
	ETIMEDOUT:       "Connection timed out",
	ECONNREFUSED:    "Connection refused",
	ENOMSG:          "No message of desired type",
	EIDRM:           "Identifier removed",
	ENOSTR:          "Device not a stream",
	ENODATA:         "No data available",
	ETIME:           "Timer expired",
	ENOSR:           "Out of streams resources",
	ENOLINK:         "Link has been severed",
	EPROTO:          "Protocol error",
	EMULTIHOP:       "Multihop attempted",
	EBADMSG:         "Bad message",
	EUSERS:          "Too many users",
	EDESTADDRREQ:    "Destination address required",
	EMSGSIZE:        "Message too long",
	EPROTOTYPE:      "Protocol wrong type for socket",
	ENOPROTOOPT:     "Protocol not available",
	EPROTONOSUPPORT: "Protocol not supported",
	ESOCKTNOSUPPORT: "Socket type not supported",
	EPFNOSUPPORT:    "Protocol family not supported",
	EAFNOSUPPORT:    "Address family not supported by protocol",
	EADDRNOTAVAIL:   "Cannot assign requested address",
	ENETDOWN:        "Network is down",
	ENETUNREACH:     "Network is unreachable",
	ENETRESET:       "Network dropped connection on reset",
	ECONNABORTED:    "Software caused connection abort",
	ENOBUFS:         "No buffer space available",
	EISCONN:         "Transport endpoint is already connected",
	ENOTCONN:        "Transport endpoint is not connected",
	ESHUTDOWN:       "Cannot send after transport endpoint shutdown",
	ETOOMANYREFS:    "Too many references: cannot splice",
	EHOSTDOWN:       "Host is down",
	EHOSTUNREACH:    "No route to host",
	EALREADY:        "Operation already in progress",
	EINPROGRESS:     "Operation now in progress",
	ESTALE:          "Stale file handle",
	EDQUOT:          "Disk quota exceeded",
	ECANCELED:       "Operation canceled",
	EOWNERDEAD:      "Owner died",
	ENOTRECOVERABLE: "State not recoverable",
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

// errnoErr is an error of errno's value e, which setErrnoOf sets.
type errnoErr int32

func (e errnoErr) Error() string { return strerror(int32(e)) }

// setErrnoOf sets errno to err's: its system error number (a
// syscall.Errno) if it has one.
func setErrnoOf(err error) {
	// One walk of the chain, without errors.As and errors.Is, which
	// would link internal/reflectlite into every program that writes.
	for e := err; e != nil; e = errors.Unwrap(e) {
		if en, ok := e.(errnoErr); ok {
			SetErrno(int32(en))
			return
		}
		t := reflect.TypeOf(e)
		if t.PkgPath() == "syscall" && t.Name() == "Errno" && t.Kind() == reflect.Uintptr {
			SetErrno(int32(reflect.ValueOf(e).Uint()))
			return
		}
	}
	switch {
	case isErr(err, fs.ErrNotExist):
		SetErrno(ENOENT)
	case isErr(err, fs.ErrExist):
		SetErrno(EEXIST)
	case isErr(err, fs.ErrPermission):
		SetErrno(EACCES)
	case isErr(err, fs.ErrInvalid):
		SetErrno(EINVAL)
	default:
		SetErrno(EIO)
	}
}

// isErr is errors.Is for a target that is comparable, as fs's are.
func isErr(err, target error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e == target {
			return true
		}
		if x, ok := e.(interface{ Is(error) bool }); ok && x.Is(target) {
			return true
		}
	}
	return false
}
