// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1

package unix

import (
	"syscall"
	"unsafe"
)

// The values of these constants are not part of the WASI API.
const (
	// UTIME_OMIT is the sentinel value to indicate that a time value should not
	// be changed. It is useful for example to indicate for example with UtimesNano
	// to avoid changing AccessTime or ModifiedTime.
	// Its value must match syscall/fs_wasip1.go
	UTIME_OMIT = -0x2

	AT_REMOVEDIR        = 0x200
	AT_SYMLINK_NOFOLLOW = 0x100
)

func Unlinkat(dirfd int, path string, flags int) error {
	if flags&AT_REMOVEDIR == 0 {
		return errnoErr(path_unlink_file(
			int32(dirfd),
			linearString(path),
			size(len(path)),
		))
	} else {
		return errnoErr(path_remove_directory(
			int32(dirfd),
			linearString(path),
			size(len(path)),
		))
	}
}

//go:wasmimport wasi_snapshot_preview1 path_unlink_file
//go:noescape
func path_unlink_file(fd int32, path *byte, pathLen size) syscall.Errno

//go:wasmimport wasi_snapshot_preview1 path_remove_directory
//go:noescape
func path_remove_directory(fd int32, path *byte, pathLen size) syscall.Errno

func Openat(dirfd int, path string, flags int, perm uint32) (int, error) {
	return syscall.Openat(dirfd, path, flags, perm)
}

func Fstatat(dirfd int, path string, stat *syscall.Stat_t, flags int) error {
	var filestatFlags uint32
	if flags&AT_SYMLINK_NOFOLLOW == 0 {
		filestatFlags |= syscall.LOOKUP_SYMLINK_FOLLOW
	}
	// (With GOWASM=gc, stat may be an object, not memory the host can
	// write: then the host writes a Stat_t in linear memory.)
	lst := stat
	if !inLinear(unsafe.Pointer(stat)) {
		lst = &make([]syscall.Stat_t, 1)[0]
	}
	errno := path_filestat_get(
		int32(dirfd),
		uint32(filestatFlags),
		linearString(path),
		size(len(path)),
		unsafe.Pointer(lst),
	)
	*stat = *lst
	return errnoErr(errno)
}

//go:wasmimport wasi_snapshot_preview1 path_filestat_get
//go:noescape
func path_filestat_get(fd int32, flags uint32, path *byte, pathLen size, buf unsafe.Pointer) syscall.Errno

func Readlinkat(dirfd int, path string, buf []byte) (int, error) {
	var nwritten size
	errno := path_readlink(
		int32(dirfd),
		linearString(path),
		size(len(path)),
		&buf[0],
		size(len(buf)),
		&nwritten)
	return int(nwritten), errnoErr(errno)

}

type (
	size = uint32
)

//go:wasmimport wasi_snapshot_preview1 path_readlink
//go:noescape
func path_readlink(fd int32, path *byte, pathLen size, buf *byte, bufLen size, nwritten *size) syscall.Errno

func Mkdirat(dirfd int, path string, mode uint32) error {
	if path == "" {
		return syscall.EINVAL
	}
	return errnoErr(path_create_directory(
		int32(dirfd),
		linearString(path),
		size(len(path)),
	))
}

//go:wasmimport wasi_snapshot_preview1 path_create_directory
//go:noescape
func path_create_directory(fd int32, path *byte, pathLen size) syscall.Errno

func Fchmodat(dirfd int, path string, mode uint32, flags int) error {
	// WASI preview 1 doesn't support changing file modes.
	return syscall.ENOSYS
}

func Fchownat(dirfd int, path string, uid, gid int, flags int) error {
	// WASI preview 1 doesn't support changing file ownership.
	return syscall.ENOSYS
}

func Renameat(olddirfd int, oldpath string, newdirfd int, newpath string) error {
	if oldpath == "" || newpath == "" {
		return syscall.EINVAL
	}
	return errnoErr(path_rename(
		int32(olddirfd),
		linearString(oldpath),
		size(len(oldpath)),
		int32(newdirfd),
		linearString(newpath),
		size(len(newpath)),
	))
}

//go:wasmimport wasi_snapshot_preview1 path_rename
//go:noescape
func path_rename(oldFd int32, oldPath *byte, oldPathLen size, newFd int32, newPath *byte, newPathLen size) syscall.Errno

func Linkat(olddirfd int, oldpath string, newdirfd int, newpath string, flag int) error {
	if oldpath == "" || newpath == "" {
		return syscall.EINVAL
	}
	return errnoErr(path_link(
		int32(olddirfd),
		0,
		linearString(oldpath),
		size(len(oldpath)),
		int32(newdirfd),
		linearString(newpath),
		size(len(newpath)),
	))
}

//go:wasmimport wasi_snapshot_preview1 path_link
//go:noescape
func path_link(oldFd int32, oldFlags uint32, oldPath *byte, oldPathLen size, newFd int32, newPath *byte, newPathLen size) syscall.Errno

func Symlinkat(oldpath string, newdirfd int, newpath string) error {
	if oldpath == "" || newpath == "" {
		return syscall.EINVAL
	}
	return errnoErr(path_symlink(
		linearString(oldpath),
		size(len(oldpath)),
		int32(newdirfd),
		linearString(newpath),
		size(len(newpath)),
	))
}

//go:wasmimport wasi_snapshot_preview1 path_symlink
//go:noescape
func path_symlink(oldPath *byte, oldPathLen size, fd int32, newPath *byte, newPathLen size) syscall.Errno

func errnoErr(errno syscall.Errno) error {
	if errno == 0 {
		return nil
	}
	return errno
}

// linearString returns a pointer to the bytes of s in linear memory,
// which the host can read: with GOWASM=gc, the bytes of a string made
// at run time are an object, so they are copied.
func linearString(s string) *byte {
	p := unsafe.StringData(s)
	if len(s) == 0 || inLinear(unsafe.Pointer(p)) {
		return p
	}
	b := make([]byte, len(s))
	copy(b, s)
	return &b[0]
}

// inLinear reports whether p is an address in linear memory, which the
// host reads and writes (as syscall.inLinear). With GOWASM=gc a pointer to an object of the
// engine's is, as an integer, gcObjects or more (any other is an address
// in linear memory, which is under 2 GB there).
func inLinear(p unsafe.Pointer) bool {
	return uint64(uintptr(p)) < gcObjects
}

// gcObjects is 2^31 with pointers of 4 bytes (wasm32), 2^32 with 8.
const gcObjects = 1 << (31 + unsafe.Sizeof(uintptr(0))/8)
