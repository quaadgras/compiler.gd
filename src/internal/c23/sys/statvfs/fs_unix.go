// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || linux || openbsd

package statvfs

import (
	c23 "internal/c23"
	"os"
	"reflect"
	"runtime"
	"syscall"
)

// statfs is the file system of the file at path.
func statfs(path string) (fsInfo, error) {
	if path == "" {
		return fsInfo{}, c23.ErrnoErr(c23.ENOENT)
	}
	var st syscall.Statfs_t
	for {
		err := syscall.Statfs(path, &st)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return fsInfo{}, err
		}
		return convert(&st), nil
	}
}

// fstatfs is the file system of f's file.
func fstatfs(f *os.File) (fsInfo, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return fsInfo{}, err
	}
	var st syscall.Statfs_t
	var serr error
	if err := rc.Control(func(fd uintptr) {
		for {
			if serr = syscall.Fstatfs(int(fd), &st); serr != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		return fsInfo{}, err
	}
	if serr != nil {
		return fsInfo{}, serr
	}
	return convert(&st), nil
}

// convert reads the system's Statfs_t, whose fields have other names
// and types on each system.
func convert(st *syscall.Statfs_t) fsInfo {
	v := reflect.ValueOf(st).Elem()
	get := func(def uint64, names ...string) uint64 {
		if n, ok := c23.SysField(v, names...); ok {
			return n
		}
		return def
	}
	var fi fsInfo
	fi.typ = get(0, "Type", "F_type")
	fi.bsize = get(4096, "Bsize", "F_bsize")
	fi.frsize = get(fi.bsize, "Frsize")
	if fi.frsize == 0 {
		fi.frsize = fi.bsize
	}
	fi.blocks = get(0, "Blocks", "F_blocks")
	fi.bfree = get(0, "Bfree", "F_bfree")
	fi.bavail = get(0, "Bavail", "F_bavail")
	fi.files = get(0, "Files", "F_files")
	fi.ffree = get(0, "Ffree", "F_ffree")
	fi.namemax = get(255, "Namelen", "Namemax", "F_namemax")
	flags := get(0, "Flags", "F_flags")
	if runtime.GOOS == "linux" {
		fi.flags = flags &^ 0x20 // ST_VALID
	} else {
		// The BSDs' MNT_RDONLY and MNT_NOSUID.
		if flags&0x1 != 0 {
			fi.flags |= ST_RDONLY
		}
		if flags&0x8 != 0 {
			fi.flags |= ST_NOSUID
		}
	}
	for _, name := range []string{"Fsid", "F_fsid"} {
		f := v.FieldByName(name)
		if !f.IsValid() || f.Kind() != reflect.Struct || f.NumField() == 0 {
			continue
		}
		a := f.Field(0)
		if a.Kind() == reflect.Array && a.Len() == 2 && a.Index(0).CanInt() {
			fi.fsid = [2]int32{int32(a.Index(0).Int()), int32(a.Index(1).Int())}
		}
	}
	return fi
}
