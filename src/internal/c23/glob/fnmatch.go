// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package glob

import (
	c23 "internal/c23"
	"os"
	"slices"
	"strings"
	"unsafe"
)

const GLOB_ERR = 0x01

const GLOB_MARK = 0x02

const GLOB_NOSORT = 0x04

const GLOB_DOOFFS = 0x08

const GLOB_NOCHECK = 0x10

const GLOB_APPEND = 0x20

const GLOB_NOESCAPE = 0x40

const GLOB_PERIOD = 0x80

const GLOB_ABORTED = 2

const GLOB_NOMATCH = 3

// globT is C's glob_t.
type globT struct {
	pathc uintptr
	pathv unsafe.Pointer
	offs  uintptr
	flags int32
	_     [5]unsafe.Pointer
}

// hasMeta reports whether a pattern's element has *, ? or [ that are not
// quoted.
func hasMeta(p string, noescape bool) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			if !noescape {
				i++
			}
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// unescape removes the quoting backslashes of a pattern's element.
func unescape(p string, noescape bool) string {
	if noescape || !strings.Contains(p, "\\") {
		return p
	}
	var b []byte
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) {
			i++
		}
		b = append(b, p[i])
	}
	return string(b)
}

// Glob is glob: the names of the files pattern matches, in *pglob, sorted
// unless GLOB_NOSORT. A wildcard matches a leading . of a name only with
// GLOB_PERIOD; errfunc, if not null, is called for a directory that
// cannot be read, and glob stops if it returns nonzero or with GLOB_ERR.
func Glob(pattern c23.Ptr, flags int32, errfunc, pglob c23.Ptr) int32 {
	pat := c23.GoString(pattern)
	noescape := flags&GLOB_NOESCAPE != 0
	var errf func(c23.Ptr, int32) int32
	if c23.Addr(errfunc) != 0 {
		errf = c23.FuncOf[func(c23.Ptr, int32) int32](errfunc)
	}
	fnflags := int32(c23.FNM_PERIOD)
	if flags&GLOB_PERIOD != 0 {
		fnflags = 0
	}
	if noescape {
		fnflags |= c23.FNM_NOESCAPE
	}

	var found []string
	aborted := false
	// expand adds the matches of the elements elems in the directory
	// dir (as written in the pattern: "" is the current directory).
	var expand func(dir string, elems []string)
	expand = func(dir string, elems []string) {
		if aborted {
			return
		}
		if len(elems) == 0 {
			if dir != "" {
				if _, err := os.Lstat(dir); err == nil {
					found = append(found, dir)
				}
			}
			return
		}
		e, rest := elems[0], elems[1:]
		join := func(name string) string {
			if dir == "" {
				return name
			}
			if strings.HasSuffix(dir, "/") {
				return dir + name
			}
			return dir + "/" + name
		}
		if e == "" { // a slash: the root, or one of several, or a trailing one
			if dir == "" {
				expand("/", rest)
			} else if len(rest) == 0 {
				if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
					found = append(found, dir+"/")
				}
			} else {
				expand(dir+"/", rest)
			}
			return
		}
		if !hasMeta(e, noescape) {
			expand(join(unescape(e, noescape)), rest)
			return
		}
		d := dir
		if d == "" {
			d = "."
		}
		ents, err := os.ReadDir(d)
		if err != nil {
			// A directory that does not exist has no matches; one that
			// cannot be read is an error.
			if c23.IsErr(err, os.ErrNotExist) {
				return
			}
			c23.SetErrnoOf(err)
			if c23.Errno() == c23.ENOTDIR {
				return
			}
			if errf != nil && errf(c23.CString(d), c23.Errno()) != 0 || flags&GLOB_ERR != 0 {
				aborted = true
			}
			return
		}
		for _, ent := range ents {
			if c23.Fnmatch(e, ent.Name(), fnflags) {
				expand(join(ent.Name()), rest)
			}
		}
	}
	expand("", strings.Split(pat, "/"))
	if aborted {
		return GLOB_ABORTED
	}
	if flags&GLOB_MARK != 0 {
		for i, f := range found {
			if !strings.HasSuffix(f, "/") {
				if fi, err := os.Stat(f); err == nil && fi.IsDir() {
					found[i] = f + "/"
				}
			}
		}
	}
	if flags&GLOB_NOSORT == 0 {
		slices.Sort(found)
	}
	if len(found) == 0 {
		if flags&GLOB_NOCHECK == 0 {
			return GLOB_NOMATCH
		}
		found = []string{pat}
	}

	var g globT
	offPathv, offOffs := int(unsafe.Offsetof(g.pathv)), int(unsafe.Offsetof(g.offs))
	offs := 0
	if flags&GLOB_DOOFFS != 0 {
		offs = int(c23.LoadLong(c23.Add(pglob, offOffs)))
	}
	var old []c23.Ptr
	if flags&GLOB_APPEND != 0 {
		n := int(c23.LoadLong(pglob))
		if pv := c23.LoadPtr(c23.Add(pglob, offPathv)); c23.Addr(pv) != 0 {
			for i := range n {
				old = append(old, c23.LoadPtr(c23.Add(pv, (offs+i)*int(c23.WordSize))))
			}
		}
	}
	n := len(old) + len(found)
	pv := c23.NewArray[unsafe.Pointer](uintptr(offs+n+1) * c23.WordSize)
	for i, p := range old {
		c23.StorePtr(c23.Add(pv, (offs+i)*int(c23.WordSize)), p)
	}
	for i, f := range found {
		c23.StorePtr(c23.Add(pv, (offs+len(old)+i)*int(c23.WordSize)), c23.CString(f))
	}
	c23.StoreLong(pglob, int64(n))
	c23.StorePtr(c23.Add(pglob, offPathv), pv)
	if flags&GLOB_DOOFFS == 0 {
		c23.StoreLong(c23.Add(pglob, offOffs), 0)
	}
	c23.Store32(c23.Add(pglob, int(unsafe.Offsetof(g.flags))), uint32(flags))
	return 0
}

// Globfree is globfree: the collector frees what glob allocated.
func Globfree(pglob c23.Ptr) {
	var g globT
	c23.StoreLong(pglob, 0)
	c23.StorePtr(c23.Add(pglob, int(unsafe.Offsetof(g.pathv))), c23.Ptr(nil))
}
