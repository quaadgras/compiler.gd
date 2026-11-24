// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dirent

import (
	c23 "internal/c23"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unsafe"
)

// Dirfd is dirfd: a descriptor of the directory, made when first asked
// for, which closedir closes.
func Dirfd(p *c23.Void) int32 {
	d := dirOf(p)
	if d.fd < 0 {
		fd := c23.Open(d.path, c23.O_RDONLY|c23.O_DIRECTORY|c23.O_CLOEXEC, 0)
		if fd < 0 {
			return -1
		}
		d.fd = fd
	}
	return d.fd
}

// Fdopendir is fdopendir: a DIR of the directory fd is a descriptor of
// (by its name), which closedir closes.
func Fdopendir(fd int32) *c23.Void {
	name, e := c23.FdDirName(fd)
	if e != 0 {
		c23.SetErrno(e)
		return (*c23.Void)(nil)
	}
	p := c23.New[Dir]()
	d := (*Dir)(unsafe.Pointer(p))
	d.path = name
	d.fd = fd
	if err := d.read(); err != nil {
		c23.SetErrnoOf(err)
		return (*c23.Void)(nil)
	}
	d.ent = c23.Malloc(280)
	return p
}

// Telldir is telldir: the position of the next entry (its index).
func Telldir(p *c23.Void) int64 { return int64(dirOf(p).i) }

// Seekdir is seekdir: readdir goes on from a position telldir returned.
func Seekdir(p *c23.Void, loc int64) {
	d := dirOf(p)
	d.i = int(min(max(loc, 0), int64(len(d.ents))))
}

// ReaddirR is readdir_r: the next entry stored at entry, and *result
// entry, or null at the end; 0.
func ReaddirR(p, entry, result *c23.Void) int32 {
	if dirOf(p).next(entry) {
		c23.StorePtr(result, entry)
	} else {
		c23.StorePtr(result, (*c23.Void)(nil))
	}
	return 0
}

// Alphasort is alphasort: the order of the names (strcoll's, which is
// strcmp's in the C locale).
func Alphasort(a, b *c23.Void) int32 {
	return int32(strings.Compare(c23.GoString(c23.Add(c23.LoadPtr(a), 19)), c23.GoString(c23.Add(c23.LoadPtr(b), 19))))
}

// Scandir is scandir: the entries of the directory filter selects (all
// if it is null), each in an array of its own of malloc's, in an array
// of malloc's sorted by compar (in readdir's order if null), at
// *namelist; their number, or -1 with errno set.
func Scandir(dir, namelist, filter, compar *c23.Void) int32 {
	dp := Opendir(dir)
	if c23.Addr(dp) == 0 {
		return -1
	}
	defer Closedir(dp)
	d := dirOf(dp)
	var sel func(*c23.Void) int32
	if c23.Addr(filter) != 0 {
		sel = c23.FuncOf[func(*c23.Void) int32](filter)
	}
	var list []*c23.Void
	for {
		ent := c23.Malloc(280)
		if !d.next(ent) {
			break
		}
		if sel == nil || sel(ent) != 0 {
			list = append(list, ent)
		}
	}
	if c23.Addr(compar) != 0 {
		cmp := c23.FuncOf[func(*c23.Void, *c23.Void) int32](compar)
		pair := c23.Malloc(uint64(2 * c23.WordSize))
		ws := int(c23.WordSize)
		slices.SortStableFunc(list, func(a, b *c23.Void) int {
			c23.StorePtr(pair, a)
			c23.StorePtr(c23.Add(pair, ws), b)
			return int(cmp(pair, c23.Add(pair, ws)))
		})
	}
	arr := c23.Malloc(uint64(max(len(list), 1)) * uint64(c23.WordSize))
	for i, e := range list {
		c23.StorePtr(c23.Add(arr, i*int(c23.WordSize)), e)
	}
	c23.StorePtr(namelist, arr)
	return int32(len(list))
}

// posix_getdents's state of a descriptor: the entries of its directory,
// read at its first call, and how many it returned.
type getdents struct {
	ents []fs.DirEntry
	i    int
}

var (
	getdentsMu sync.Mutex
	getdentsOf = map[*os.File]*getdents{}
)

// PosixGetdents is posix_getdents: the directory's next entries, as
// struct posix_dents (struct dirent's fields, with a name of up to 255
// bytes, each 8-byte aligned), as many as fit in the n bytes at buf; the
// bytes stored, 0 at the end. Its entries are those of readdir, read at
// the first call; lseek does not rewind it.
func PosixGetdents(fd int32, buf *c23.Void, n uint64, flags int32) int64 {
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if flags != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	name, e := c23.FdDirName(fd)
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	getdentsMu.Lock()
	defer getdentsMu.Unlock()
	st := getdentsOf[of.F]
	if st == nil {
		ents, err := os.ReadDir(name)
		if err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
		st = &getdents{ents: append([]fs.DirEntry{
			dotEntry{".", name},
			dotEntry{"..", filepath.Join(name, "..")},
		}, ents...)}
		getdentsOf[of.F] = st
	}
	var off uint64
	for st.i < len(st.ents) {
		e := st.ents[st.i]
		reclen := uint64((19 + min(len(e.Name()), 255) + 1 + 7) &^ 7)
		if off+reclen > n {
			if off == 0 {
				c23.SetErrno(c23.EINVAL)
				return -1
			}
			break
		}
		st.i++
		storeEnt(c23.Add(buf, int(off)), e, uint64(st.i), 255)
		off += reclen
	}
	return int64(off)
}
