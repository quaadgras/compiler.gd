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
	"unsafe"
)

// groups reads the groups of /etc/group (none if there is none).
func groups() [][]string {
	f, err := os.Open("/etc/group")
	if err != nil {
		return nil
	}
	defer f.Close()
	var gs [][]string
	s := bufio.NewScanner(f)
	for s.Scan() {
		e := strings.Split(s.Text(), ":")
		if len(e) != 4 || strings.HasPrefix(e[0], "#") {
			continue
		}
		gs = append(gs, e)
	}
	return gs
}

// getgrent's position: the groups, read at the first call after
// setgrent (or the first), and how many it returned.
var (
	entGroups [][]string
	entRead   bool
	entNext   int
)

// Getgrent is getgrent: the next group of /etc/group, or null at the
// end.
func Getgrent() *c23.Void {
	grMu.Lock()
	if !entRead {
		entGroups, entRead, entNext = groups(), true, 0
	}
	if entNext >= len(entGroups) {
		grMu.Unlock()
		return (*c23.Void)(nil)
	}
	e := entGroups[entNext]
	entNext++
	grMu.Unlock()
	return grResult(e, true)
}

// Setgrent is setgrent: getgrent starts again.
func Setgrent() {
	grMu.Lock()
	defer grMu.Unlock()
	entGroups, entRead, entNext = nil, false, 0
}

// Endgrent is endgrent.
func Endgrent() { Setgrent() }

// grResultR is the result of getgrgid_r and getgrnam_r: e as the struct
// group at gr, its strings in the n bytes at buf (ERANGE if they and the
// array of members do not fit), and *result gr, or null if there is no
// such group. The array of members is allocated: the caller's buffer is
// bytes, which hold no pointer, and the collector frees the array with
// the last pointer to it.
func grResultR(e []string, ok bool, gr, buf *c23.Void, n uint64, result *c23.Void) int32 {
	c23.StorePtr(result, (*c23.Void)(nil))
	if !ok {
		return 0
	}
	var members []string
	if e[3] != "" {
		members = strings.Split(e[3], ",")
	}
	ws := uint64(c23.WordSize)
	pad := (ws - uint64(c23.Addr(buf))%ws) % ws
	need := pad + uint64(len(members)+1)*ws + uint64(len(e[0])+1+len(e[1])+1)
	for _, m := range members {
		need += uint64(len(m) + 1)
	}
	if need > n {
		return c23.ERANGE
	}
	mem := (*c23.Void)(unsafe.Pointer(c23.AllocOf[*int8](uint64(len(members)+1) * ws)))
	off := 0
	str := func(s string) *c23.Void {
		p := c23.Add(buf, off)
		copy(c23.BytesAt(p, uintptr(len(s)+1)), s+"\x00")
		off += len(s) + 1
		return p
	}
	for i, m := range members {
		c23.StorePtr(c23.Add(mem, i*int(ws)), str(m))
	}
	c23.StorePtr(c23.Add(mem, len(members)*int(ws)), (*c23.Void)(nil))
	gid, _ := strconv.ParseUint(e[2], 10, 32)
	var g group
	c23.StorePtr(gr, str(e[0]))
	c23.StorePtr(c23.Add(gr, int(unsafe.Offsetof(g.passwd))), str(e[1]))
	c23.Store32(c23.Add(gr, int(unsafe.Offsetof(g.gid))), uint32(gid))
	c23.StorePtr(c23.Add(gr, int(unsafe.Offsetof(g.mem))), mem)
	c23.StorePtr(result, gr)
	return 0
}

// GetgrgidR is getgrgid_r.
func GetgrgidR(gid uint32, gr, buf *c23.Void, n uint64, result *c23.Void) int32 {
	e, ok := lookupGroup("", gid)
	return grResultR(e, ok, gr, buf, n, result)
}

// GetgrnamR is getgrnam_r.
func GetgrnamR(name, gr, buf *c23.Void, n uint64, result *c23.Void) int32 {
	s := c23.GoString(name)
	var e []string
	ok := false
	if s != "" {
		e, ok = lookupGroup(s, 0)
	}
	return grResultR(e, ok, gr, buf, n, result)
}
