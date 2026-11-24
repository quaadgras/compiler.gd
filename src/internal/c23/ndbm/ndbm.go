// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ndbm is ndbm.h's functions for cmd/cc: a database of keys and
// data in a file. A database is file.pag, a log of its stores and
// deletes, which dbm_open reads and dbm_close rewrites compactly, and
// file.dir, empty, which dbm_open makes as the classic ndbm does (POSIX
// leaves the files' format to the library). Its keys come in sorted
// order.
package ndbm

import (
	"bytes"
	"internal/c23"
	"io"
	"maps"
	"os"
	"slices"
	"sync"
)

const (
	DBM_INSERT  = 0
	DBM_REPLACE = 1
)

// A db is a DBM.
type db struct {
	path   string
	f      *os.File
	m      map[string][]byte
	keys   []string // dbm_firstkey's snapshot, which dbm_nextkey walks
	next   int
	err    bool
	rdonly bool
}

var (
	mu  sync.Mutex
	dbs = map[uintptr]*db{} // by the address of the C object DBM * points to
)

func dbOf(p c23.Ptr) *db {
	mu.Lock()
	defer mu.Unlock()
	return dbs[c23.Addr(p)]
}

// goFlags converts open's flags (Linux's values) to os.OpenFile's.
func goFlags(flags int32) int {
	var f int
	switch flags & c23.O_ACCMODE {
	case c23.O_RDONLY:
		f = os.O_RDONLY
	case c23.O_WRONLY, c23.O_RDWR:
		f = os.O_RDWR // a database is read to be written
	}
	if flags&c23.O_CREAT != 0 {
		f |= os.O_CREATE
	}
	if flags&c23.O_EXCL != 0 {
		f |= os.O_EXCL
	}
	if flags&c23.O_TRUNC != 0 {
		f |= os.O_TRUNC
	}
	return f
}

// DbmOpen is dbm_open.
func DbmOpen(file c23.Ptr, flags int32, mode uint32) c23.Ptr {
	name := c23.GoString(file)
	f, err := os.OpenFile(name+".pag", goFlags(flags), os.FileMode(mode&0o777))
	if err != nil {
		c23.SetErrnoOf(err)
		return c23.Ptr(nil)
	}
	if flags&c23.O_CREAT != 0 {
		if d, err := os.OpenFile(name+".dir", os.O_RDWR|os.O_CREATE, os.FileMode(mode&0o777)); err == nil {
			d.Close()
		}
	}
	d := &db{path: name + ".pag", f: f, m: map[string][]byte{}, rdonly: flags&c23.O_ACCMODE == c23.O_RDONLY}
	if err := d.load(); err != nil {
		f.Close()
		c23.SetErrnoOf(err)
		return c23.Ptr(nil)
	}
	p := c23.Malloc(1)
	mu.Lock()
	dbs[c23.Addr(p)] = d
	mu.Unlock()
	return p
}

// load replays the log.
func (d *db) load() error {
	b, err := io.ReadAll(d.f)
	if err != nil {
		return err
	}
	for len(b) >= 9 {
		op := b[0]
		kn, dn := le32(b[1:]), le32(b[5:])
		b = b[9:]
		if uint64(len(b)) < uint64(kn)+uint64(dn) {
			break // a record cut short: the end
		}
		k, v := string(b[:kn]), bytes.Clone(b[kn:kn+dn])
		b = b[kn+dn:]
		if op == 'S' {
			d.m[k] = v
		} else {
			delete(d.m, k)
		}
	}
	return nil
}

// record appends a store ('S') or delete ('D') to the log.
func (d *db) record(op byte, k string, v []byte) error {
	var h [9]byte
	h[0] = op
	putLE32(h[1:], uint32(len(k)))
	putLE32(h[5:], uint32(len(v)))
	_, err := d.f.Write(slices.Concat(h[:], []byte(k), v))
	return err
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// DbmClose is dbm_close: a database open for writing is rewritten with
// one record a key.
func DbmClose(p c23.Ptr) {
	mu.Lock()
	d := dbs[c23.Addr(p)]
	delete(dbs, c23.Addr(p))
	mu.Unlock()
	if d == nil {
		return
	}
	if !d.rdonly {
		tmp := d.path + ".tmp"
		if f, err := os.Create(tmp); err == nil {
			nd := &db{f: f}
			ok := true
			for _, k := range slices.Sorted(maps.Keys(d.m)) {
				if nd.record('S', k, d.m[k]) != nil {
					ok = false
				}
			}
			if f.Close() == nil && ok {
				os.Rename(tmp, d.path)
			} else {
				os.Remove(tmp)
			}
		}
	}
	d.f.Close()
}

func datumBytes(p c23.Ptr, n uint64) []byte {
	if n == 0 {
		return nil
	}
	return c23.BytesAt(p, uintptr(n))
}

// storeDatum stores b as a datum {void *dptr; size_t dsize;} at out, in
// memory of its own; nil is the null datum.
func storeDatum(out c23.Ptr, b []byte, ok bool) {
	ws := int(c23.WordSize)
	if !ok {
		c23.StorePtr(out, c23.Ptr(nil))
		c23.StoreLong(c23.Add(out, ws), 0)
		return
	}
	p := c23.Malloc(uint64(max(len(b), 1)))
	copy(c23.BytesAt(p, uintptr(len(b))), b)
	c23.StorePtr(out, p)
	c23.StoreLong(c23.Add(out, ws), int64(len(b)))
}

// DbmStore is dbm_store: 1 if DBM_INSERT finds the key there.
func DbmStore(p, k c23.Ptr, kn uint64, v c23.Ptr, vn uint64, mode int32) int32 {
	d := dbOf(p)
	if d == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if d.rdonly {
		d.err = true
		c23.SetErrno(c23.EPERM)
		return -1
	}
	key := string(datumBytes(k, kn))
	if _, ok := d.m[key]; ok && mode == DBM_INSERT {
		return 1
	}
	val := bytes.Clone(datumBytes(v, vn))
	if err := d.record('S', key, val); err != nil {
		d.err = true
		c23.SetErrnoOf(err)
		return -1
	}
	d.m[key] = val
	return 0
}

// DbmFetch is dbm_fetch, its datum stored at out.
func DbmFetch(p, k c23.Ptr, kn uint64, out c23.Ptr) {
	d := dbOf(p)
	if d == nil {
		storeDatum(out, nil, false)
		return
	}
	v, ok := d.m[string(datumBytes(k, kn))]
	storeDatum(out, v, ok)
}

// DbmDelete is dbm_delete.
func DbmDelete(p, k c23.Ptr, kn uint64) int32 {
	d := dbOf(p)
	if d == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if d.rdonly {
		d.err = true
		c23.SetErrno(c23.EPERM)
		return -1
	}
	key := string(datumBytes(k, kn))
	if _, ok := d.m[key]; !ok {
		return -1
	}
	if err := d.record('D', key, nil); err != nil {
		d.err = true
		c23.SetErrnoOf(err)
		return -1
	}
	delete(d.m, key)
	return 0
}

// DbmFirstkey is dbm_firstkey: the keys from here are those there now,
// sorted.
func DbmFirstkey(p, out c23.Ptr) {
	d := dbOf(p)
	if d == nil {
		storeDatum(out, nil, false)
		return
	}
	d.keys = slices.Sorted(maps.Keys(d.m))
	d.next = 0
	DbmNextkey(p, out)
}

// DbmNextkey is dbm_nextkey.
func DbmNextkey(p, out c23.Ptr) {
	d := dbOf(p)
	if d == nil || d.next >= len(d.keys) {
		storeDatum(out, nil, false)
		return
	}
	k := d.keys[d.next]
	d.next++
	storeDatum(out, []byte(k), true)
}

// DbmError is dbm_error.
func DbmError(p c23.Ptr) int32 {
	if d := dbOf(p); d != nil && d.err {
		return 1
	}
	return 0
}

// DbmClearerr is dbm_clearerr.
func DbmClearerr(p c23.Ptr) int32 {
	if d := dbOf(p); d != nil {
		d.err = false
	}
	return 0
}
