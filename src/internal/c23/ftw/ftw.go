// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ftw is ftw.h's functions for cmd/cc: file tree walks on os's
// files, with each object's struct stat filled as stat's is.
package ftw

import (
	c23 "internal/c23"
	"internal/c23/sys/stat"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const (
	FTW_F   = 0
	FTW_D   = 1
	FTW_DNR = 2
	FTW_NS  = 3
	FTW_SL  = 4
	FTW_DP  = 5
	FTW_SLN = 6

	FTW_PHYS         = 1
	FTW_MOUNT        = 2
	FTW_CHDIR        = 4
	FTW_DEPTH        = 8
	FTW_ACTIONRETVAL = 16

	FTW_CONTINUE      = 0
	FTW_STOP          = 1
	FTW_SKIP_SUBTREE  = 2
	FTW_SKIP_SIBLINGS = 3
)

// devIno identifies a directory, so that a walk that follows symbolic
// links visits each once.
type devIno struct{ dev, ino uint64 }

func idOf(fi fs.FileInfo) (devIno, bool) {
	sys := reflect.Indirect(reflect.ValueOf(fi.Sys()))
	if !sys.IsValid() || sys.Kind() != reflect.Struct {
		return devIno{}, false
	}
	dev, ok1 := c23.SysField(sys, "Dev")
	ino, ok2 := c23.SysField(sys, "Ino")
	return devIno{dev, ino}, ok1 && ok2 && ino != 0
}

// A walk is one call of nftw's.
type walk struct {
	fn      func(path string, flag int32, fi fs.FileInfo, base, level int) int32
	flags   int32
	dev     devIno // the starting point's device, for FTW_MOUNT
	hasDev  bool
	visited map[devIno]bool
}

func (w *walk) has(f int32) bool { return w.flags&f != 0 }

// statOf stats the object at path (the walk's own name for it) and gives
// its flag, FTW_F, FTW_D, FTW_SL, FTW_SLN or FTW_NS; for FTW_NS, err is
// why.
func (w *walk) statOf(path string) (fs.FileInfo, int32, error) {
	var fi fs.FileInfo
	var err error
	if w.has(FTW_PHYS) {
		fi, err = os.Lstat(path)
	} else {
		fi, err = os.Stat(path)
	}
	if err != nil {
		if !w.has(FTW_PHYS) {
			if lfi, lerr := os.Lstat(path); lerr == nil && lfi.Mode()&fs.ModeSymlink != 0 {
				return lfi, FTW_SLN, nil
			}
		}
		return nil, FTW_NS, err
	}
	switch {
	case fi.IsDir():
		return fi, FTW_D, nil
	case fi.Mode()&fs.ModeSymlink != 0:
		return fi, FTW_SL, nil
	}
	return fi, FTW_F, nil
}

// visit walks the object cpath (the name C sees; path is the walk's,
// absolute with FTW_CHDIR), whose name starts at base, level deep.
func (w *walk) visit(cpath, path string, base, level int, fi fs.FileInfo, flag int32) int32 {
	if flag != FTW_D {
		return w.fn(cpath, flag, fi, base, level)
	}
	id, hasID := idOf(fi)
	if w.has(FTW_MOUNT) && w.hasDev && hasID && id.dev != w.dev.dev {
		return 0
	}
	if hasID && !w.has(FTW_PHYS) {
		if w.visited[id] {
			return 0
		}
		w.visited[id] = true
	}
	ents, err := os.ReadDir(path)
	if err != nil {
		return w.fn(cpath, FTW_DNR, fi, base, level)
	}
	if !w.has(FTW_DEPTH) {
		r := w.fn(cpath, FTW_D, fi, base, level)
		if r != 0 {
			if w.has(FTW_ACTIONRETVAL) && r == FTW_SKIP_SUBTREE {
				return 0
			}
			return r
		}
	}
	if w.has(FTW_CHDIR) {
		if err := os.Chdir(path); err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
	}
	prefix := cpath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	r := int32(0)
	for _, e := range ents {
		name := e.Name()
		p := filepath.Join(path, name)
		efi, eflag, err := w.statOf(p)
		if eflag == FTW_NS {
			c23.SetErrnoOf(err)
		}
		if r = w.visit(prefix+name, p, len(prefix), level+1, efi, eflag); r != 0 {
			break
		}
	}
	if w.has(FTW_ACTIONRETVAL) && r == FTW_SKIP_SIBLINGS {
		r = 0
	}
	if w.has(FTW_CHDIR) && r != -1 {
		if err := os.Chdir(filepath.Dir(path)); err != nil && r == 0 {
			c23.SetErrnoOf(err)
			r = -1
		}
	}
	if r == 0 && w.has(FTW_DEPTH) {
		r = w.fn(cpath, FTW_DP, fi, base, level)
	}
	return r
}

// run walks the tree at path, calling fn for each object.
func run(path string, flags int32, fn func(path string, flag int32, fi fs.FileInfo, base, level int) int32) int32 {
	if path == "" {
		c23.SetErrno(c23.ENOENT)
		return -1
	}
	w := &walk{fn: fn, flags: flags, visited: map[devIno]bool{}}
	walkPath := path
	if w.has(FTW_CHDIR) {
		abs, err := filepath.Abs(path)
		if err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
		walkPath = abs
		wd, err := os.Getwd()
		if err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
		defer os.Chdir(wd)
	}
	fi, flag, err := w.statOf(walkPath)
	if flag == FTW_NS {
		// The starting point must exist, or be one the walk cannot
		// look at.
		c23.SetErrnoOf(err)
		if c23.Errno() != c23.EACCES {
			return -1
		}
	}
	if fi != nil {
		w.dev, w.hasDev = idOf(fi)
	}
	trimmed := strings.TrimRight(path, "/")
	base := strings.LastIndexByte(trimmed, '/') + 1
	if w.has(FTW_CHDIR) {
		if err := os.Chdir(filepath.Dir(walkPath)); err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
	}
	r := w.visit(path, walkPath, base, 0, fi, flag)
	if w.has(FTW_ACTIONRETVAL) && (r == FTW_SKIP_SUBTREE || r == FTW_SKIP_SIBLINGS) {
		r = 0
	}
	return r
}

// call makes the C arguments of a callback: the path, and the struct stat
// of fi (zeros if the object could not be looked at).
type call struct {
	sb *c23.Void
}

func (c *call) stat(fi fs.FileInfo) *c23.Void {
	if c.sb == nil {
		c.sb = c23.Malloc(stat.StatSize)
	}
	if fi == nil {
		c23.Memset(c.sb, 0, stat.StatSize)
	} else {
		stat.FillStat(c.sb, fi)
	}
	return c.sb
}

// Nftw is nftw: fd_limit is not needed, as the walk keeps no directory
// open; the entries of a directory are in the order of their names. A
// directory is visited once even if symbolic links lead to it again.
// FTW_CHDIR changes the working directory to each directory before its
// entries, so that fn runs in the directory of the object it is given,
// and back to the walk's at the end; FTW_ACTIONRETVAL is glibc's.
func Nftw(path, fn *c23.Void, fdLimit, flags int32) int32 {
	f := c23.FuncOf[func(*c23.Void, *c23.Void, int32, *c23.Void) int32](fn)
	var c call
	ftw := c23.Malloc(8)
	return run(c23.GoString(path), flags, func(p string, flag int32, fi fs.FileInfo, base, level int) int32 {
		c23.Store32(ftw, uint32(base))
		c23.Store32(c23.Add(ftw, 4), uint32(level))
		return f(c23.CString(p), c.stat(fi), flag, ftw)
	})
}

// Ftw is ftw: nftw without flags (symbolic links are followed, a
// dangling one is FTW_SLN, as glibc's).
func Ftw(path, fn *c23.Void, fdLimit int32) int32 {
	f := c23.FuncOf[func(*c23.Void, *c23.Void, int32) int32](fn)
	var c call
	return run(c23.GoString(path), 0, func(p string, flag int32, fi fs.FileInfo, base, level int) int32 {
		return f(c23.CString(p), c.stat(fi), flag)
	})
}
