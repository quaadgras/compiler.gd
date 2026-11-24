// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mman

import (
	c23 "internal/c23"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const MAP_SHARED = 0x01

const MAP_PRIVATE = 0x02

const MAP_FIXED = 0x10

const MAP_ANONYMOUS = 0x20

// mapFailed is MAP_FAILED, (void *)-1.
var mapFailed = c23.FromInt(^uintptr(0))

// Mmap is mmap, of memory C allocates: an anonymous mapping is zeroed
// memory, and a private mapping of a file a copy of what it holds (zeros
// past its end). A shared mapping of a file fails (ENODEV), as does
// MAP_FIXED (EINVAL); prot is not enforced.
func Mmap(addr *c23.Void, length uint64, prot, flags, fd int32, off int64) *c23.Void {
	page := int64(os.Getpagesize())
	share := flags & (MAP_SHARED | MAP_PRIVATE)
	if length == 0 || share == 0 || share == MAP_SHARED|MAP_PRIVATE || flags&MAP_FIXED != 0 || off < 0 || off%page != 0 {
		c23.SetErrno(c23.EINVAL)
		return mapFailed
	}
	if flags&MAP_ANONYMOUS != 0 {
		return c23.MallocAligned(uintptr(page), uintptr(length))
	}
	f := c23.FdOS(fd)
	if f == nil {
		return mapFailed
	}
	if share == MAP_SHARED {
		return mapShared(f, length, off, page)
	}
	p := c23.MallocAligned(uintptr(page), uintptr(length))
	if _, err := f.ReadAt(c23.BytesAt(p, uintptr(length)), off); err != nil && err != io.EOF {
		c23.SetErrnoOf(err)
		return mapFailed
	}
	return p
}

// Munmap is munmap: the collector frees the memory when it is no longer
// reachable.
func Munmap(addr *c23.Void, length uint64) int32 {
	if length == 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return 0
}

// Msync is msync: no mapping is shared with a file.
func Msync(addr *c23.Void, length uint64, flags int32) int32 { return 0 }

// Mprotect is mprotect, which changes nothing.
func Mprotect(addr *c23.Void, length uint64, prot int32) int32 { return 0 }

// Madvise is madvise, which takes no advice.
func Madvise(addr *c23.Void, length uint64, advice int32) int32 { return 0 }

var sharedMu sync.Mutex

var sharedMaps = map[sharedKey]*c23.Void{}

type sharedKey struct {
	file     string
	off, len int64
}

func mapShared(f *os.File, length uint64, off, page int64) *c23.Void {
	name := f.Name()
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	k := sharedKey{name, off, int64(length)}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if p, ok := sharedMaps[k]; ok {
		return p
	}
	p := c23.MallocAligned(uintptr(page), uintptr(length))
	if _, err := f.ReadAt(c23.BytesAt(p, uintptr(length)), off); err != nil && err != io.EOF {
		c23.SetErrnoOf(err)
		return mapFailed
	}
	sharedMaps[k] = p
	return p
}
