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
// MAP_FIXED (EINVAL): nothing is ever mapped where the program says, so
// no mapping is over memory that something else has. prot is not
// enforced.
func Mmap(addr *c23.Void, length uint64, prot, flags, fd int32, off int64) *c23.Void {
	page := int64(os.Getpagesize())
	share := flags & (MAP_SHARED | MAP_PRIVATE)
	if length == 0 || share == 0 || share == MAP_SHARED|MAP_PRIVATE || flags&MAP_FIXED != 0 || off < 0 || off%page != 0 {
		c23.SetErrno(c23.EINVAL)
		return mapFailed
	}
	if flags&MAP_ANONYMOUS != 0 {
		return newMapping(length, page)
	}
	f := c23.FdOS(fd)
	if f == nil {
		return mapFailed
	}
	if share == MAP_SHARED {
		return mapShared(f, length, off, page)
	}
	p := newMapping(length, page)
	if c23.Addr(p) == c23.Addr(mapFailed) {
		return p
	}
	if _, err := f.ReadAt(c23.Writable(p, uintptr(length)), off); err != nil && err != io.EOF {
		c23.SetErrnoOf(err)
		dropMapping(p)
		return mapFailed
	}
	return p
}

// The mappings that mmap made and munmap did not take away, by their
// addresses, with their lengths. A mapping is an object of C's as any
// that malloc gives, and its bounds are that object's: the functions
// that take an address and a length of mapped memory (munmap, mprotect,
// msync) are for these and nothing else, so none of them is told
// anything of the heap, the stack or the program's code. The table
// keeps a mapping allocated until it is unmapped whole, as the system
// would.
var (
	mapsMu sync.Mutex
	maps   = map[uintptr]mapping{}
)

type mapping struct {
	p      *c23.Void
	length uint64
	shared bool // a shared mapping of a file (mapShared), which stays
}

// newMapping allocates a mapping of length bytes: at a multiple of the
// page's size, and, where the allocator gives it so, ending where the
// mapping does.
func newMapping(length uint64, page int64) *c23.Void {
	if length > 1<<47 {
		c23.SetErrno(c23.ENOMEM)
		return mapFailed
	}
	p := c23.AllocRaw(length)
	if p != nil && c23.Addr(p)%uintptr(page) != 0 {
		p = c23.MallocAligned(uintptr(page), uintptr(length))
	}
	if p == nil {
		c23.SetErrno(c23.ENOMEM)
		return mapFailed
	}
	mapsMu.Lock()
	maps[c23.Addr(p)] = mapping{p, length, false}
	mapsMu.Unlock()
	return p
}

func dropMapping(p *c23.Void) {
	mapsMu.Lock()
	delete(maps, c23.Addr(p))
	mapsMu.Unlock()
}

// mapped returns the address of the mapping that the length bytes at
// addr are in, and whether they are in one. The caller holds mapsMu.
func mapped(addr *c23.Void, length uint64) (uintptr, bool) {
	a := c23.Addr(addr)
	for start, m := range maps {
		if a >= start && uint64(a-start) <= m.length && length <= m.length-uint64(a-start) {
			return start, true
		}
	}
	return 0, false
}

// inMapping reports whether the length bytes at addr are in a mapping.
func inMapping(addr *c23.Void, length uint64) bool {
	mapsMu.Lock()
	defer mapsMu.Unlock()
	_, ok := mapped(addr, length)
	return ok
}

// Munmap is munmap, of all or of part of a mapping that mmap made:
// anything else is EINVAL. A mapping that is unmapped whole is the
// collector's to free when nothing points to it; part of one stays as
// it is.
func Munmap(addr *c23.Void, length uint64) int32 {
	if length == 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	mapsMu.Lock()
	defer mapsMu.Unlock()
	start, ok := mapped(addr, length)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if m := maps[start]; c23.Addr(addr) == start && length >= m.length {
		if !m.shared {
			delete(maps, start)
		}
	}
	return 0
}

// Msync is msync: no mapping is shared with a file. Memory that mmap
// did not map is ENOMEM.
func Msync(addr *c23.Void, length uint64, flags int32) int32 {
	if !inMapping(addr, length) {
		c23.SetErrno(c23.ENOMEM)
		return -1
	}
	return 0
}

// Mprotect is mprotect, which changes nothing, of memory that mmap
// mapped: of anything else (the heap, the stack, the program's code) it
// is ENOMEM.
func Mprotect(addr *c23.Void, length uint64, prot int32) int32 {
	if !inMapping(addr, length) {
		c23.SetErrno(c23.ENOMEM)
		return -1
	}
	return 0
}

// advisable reports whether the length bytes at addr are memory that
// the program has: a function that would only advise the system of it,
// or keep it from being paged out, does nothing, and says that it did
// for such memory and ENOMEM for any other.
func advisable(addr *c23.Void, length uint64) bool {
	if length == 0 || inMapping(addr, length) {
		return true
	}
	return c23.Has(addr, length)
}

// Madvise is madvise, which takes no advice.
func Madvise(addr *c23.Void, length uint64, advice int32) int32 {
	if !advisable(addr, length) {
		c23.SetErrno(c23.ENOMEM)
		return -1
	}
	return 0
}

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
	p := newMapping(length, page)
	if c23.Addr(p) == c23.Addr(mapFailed) {
		return p
	}
	if _, err := f.ReadAt(c23.Writable(p, uintptr(length)), off); err != nil && err != io.EOF {
		c23.SetErrnoOf(err)
		dropMapping(p)
		return mapFailed
	}
	// The mapping is every mmap's of the same part of the file: it
	// stays when one of them is unmapped.
	mapsMu.Lock()
	maps[c23.Addr(p)] = mapping{p, length, true}
	mapsMu.Unlock()
	sharedMaps[k] = p
	return p
}
