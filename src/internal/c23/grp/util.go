// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package grp

import (
	"bufio"
	c23 "internal/c23"
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// group is C's struct group.
type group struct {
	name, passwd unsafe.Pointer
	gid          uint32
	mem          unsafe.Pointer
}

var grMu sync.Mutex

var grBuf c23.Ptr

// lookupGroup finds the group of the name, or of gid if the name is "",
// in /etc/group: its fields.
func lookupGroup(name string, gid uint32) ([]string, bool) {
	f, err := os.Open("/etc/group")
	if err != nil {
		return nil, false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		e := strings.Split(s.Text(), ":")
		if len(e) != 4 || strings.HasPrefix(e[0], "#") {
			continue
		}
		if name != "" && e[0] == name {
			return e, true
		}
		if name == "" {
			if g, err := strconv.ParseUint(e[2], 10, 32); err == nil && uint32(g) == gid {
				return e, true
			}
		}
	}
	return nil, false
}

// grResult returns the static struct group of e, or null.
func grResult(e []string, ok bool) c23.Ptr {
	if !ok {
		return c23.Ptr{}
	}
	grMu.Lock()
	defer grMu.Unlock()
	if grBuf.Obj == nil {
		grBuf = c23.New[group]()
	}
	gid, _ := strconv.ParseUint(e[2], 10, 32)
	var members []string
	if e[3] != "" {
		members = strings.Split(e[3], ",")
	}
	mem := c23.NewArray[unsafe.Pointer](uintptr(len(members)+1) * c23.WordSize)
	for i, m := range members {
		c23.StorePtr(c23.Add(mem, i*int(c23.WordSize)), c23.CString(m))
	}
	var gr group
	c23.StorePtr(grBuf, c23.CString(e[0]))
	c23.StorePtr(c23.Add(grBuf, int(unsafe.Offsetof(gr.passwd))), c23.CString(e[1]))
	c23.Store32(c23.Add(grBuf, int(unsafe.Offsetof(gr.gid))), uint32(gid))
	c23.StorePtr(c23.Add(grBuf, int(unsafe.Offsetof(gr.mem))), mem)
	return grBuf
}

// Getgrnam is getgrnam: the group of the name in /etc/group, or null.
func Getgrnam(name c23.Ptr) c23.Ptr {
	n := c23.GoString(name)
	if n == "" {
		return c23.Ptr{}
	}
	return grResult(lookupGroup(n, 0))
}

// Getgrgid is getgrgid: the group of gid in /etc/group, or null.
func Getgrgid(gid uint32) c23.Ptr { return grResult(lookupGroup("", gid)) }
