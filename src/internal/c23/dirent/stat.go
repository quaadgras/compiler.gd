// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dirent

import (
	c23 "internal/c23"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
)

const DT_UNKNOWN = 0

const DT_FIFO = 1

const DT_CHR = 2

const DT_DIR = 4

const DT_BLK = 6

const DT_REG = 8

const DT_LNK = 10

const DT_SOCK = 12

// A Dir is DIR: a directory's entries, read when opened (and again when
// rewound), with "." and ".." first as the system lists them.
type Dir struct {
	path   string
	ents   []fs.DirEntry
	i      int
	ent    c23.Ptr // the struct dirent readdir returns
	closed bool
}

// dirOf looks Dir's type up when called, not in a package variable:
// made at init, it would link what Dir's fields reach (fs.DirEntry's
// FileInfo, its time.Time, time's formatting) into every program.
func dirOf(p c23.Ptr) *Dir {
	if p.Obj == nil || p.Info() != c23.TinfoOf[Dir]() || p.Off != 0 {
		c23.RaiseFault("not a DIR", c23.Addr(p))
	}
	d := (*Dir)(p.Obj)
	if d.closed {
		c23.RaiseFault("use of a closed DIR", c23.Addr(p))
	}
	return d
}

// dotEntry is the DirEntry of . or .. in a directory.
type dotEntry struct {
	name string
	path string
}

func (e dotEntry) Name() string { return e.name }

func (e dotEntry) IsDir() bool { return true }

func (e dotEntry) Type() fs.FileMode { return fs.ModeDir }

func (e dotEntry) Info() (fs.FileInfo, error) { return os.Stat(e.path) }

func (d *Dir) read() error {
	ents, err := os.ReadDir(d.path)
	if err != nil {
		return err
	}
	d.ents = append([]fs.DirEntry{
		dotEntry{".", d.path},
		dotEntry{"..", filepath.Join(d.path, "..")},
	}, ents...)
	d.i = 0
	return nil
}

func Opendir(path c23.Ptr) c23.Ptr {
	name := c23.GoString(path)
	fi, err := os.Stat(name)
	if err == nil && !fi.IsDir() {
		err = c23.ErrnoErr(c23.ENOTDIR)
	}
	p := c23.New[Dir]()
	d := (*Dir)(p.Obj)
	d.path = name
	if err == nil {
		err = d.read()
	}
	if err != nil {
		c23.SetErrnoOf(err)
		return c23.Ptr{}
	}
	d.ent = c23.Malloc(280)
	return p
}

// Readdir is readdir: the next entry, in an array the next call reuses,
// or null at the end:
//
//	ino_t d_ino; off_t d_off; unsigned short d_reclen;	// 0, 8, 16
//	unsigned char d_type; char d_name[256];		// 18, 19 (280 bytes)
func Readdir(p c23.Ptr) c23.Ptr {
	d := dirOf(p)
	if d.i >= len(d.ents) {
		return c23.Ptr{}
	}
	e := d.ents[d.i]
	d.i++
	var ino uint64
	if fi, err := e.Info(); err == nil {
		ino, _ = c23.SysField(reflect.Indirect(reflect.ValueOf(fi.Sys())), "Ino")
	}
	typ := uint8(DT_UNKNOWN)
	switch t := e.Type(); {
	case t.IsDir():
		typ = DT_DIR
	case t&fs.ModeSymlink != 0:
		typ = DT_LNK
	case t&fs.ModeNamedPipe != 0:
		typ = DT_FIFO
	case t&fs.ModeSocket != 0:
		typ = DT_SOCK
	case t&fs.ModeCharDevice != 0:
		typ = DT_CHR
	case t&fs.ModeDevice != 0:
		typ = DT_BLK
	case t.IsRegular():
		typ = DT_REG
	}
	name := e.Name()
	if len(name) > 255 {
		name = name[:255]
	}
	c23.Store64(d.ent, ino)
	c23.Store64(c23.Add(d.ent, 8), uint64(d.i))
	c23.Store16(c23.Add(d.ent, 16), uint16((19+len(name)+1+7)&^7))
	c23.Store8(c23.Add(d.ent, 18), typ)
	copy(c23.BytesAt(c23.Add(d.ent, 19), uintptr(len(name)+1)), name+"\x00")
	return d.ent
}

func Closedir(p c23.Ptr) int32 {
	dirOf(p).closed = true
	return 0
}

func Rewinddir(p c23.Ptr) {
	d := dirOf(p)
	if d.read() != nil {
		d.ents, d.i = nil, 0
	}
}
