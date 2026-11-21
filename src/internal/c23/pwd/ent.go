// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pwd

import (
	"bufio"
	c23 "internal/c23"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

// users reads the users of /etc/passwd, or, if there is none, the
// program's own user as the environment tells of it.
func users() []pwEntry {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		if e, ok := lookupUser("", uint32(os.Getuid())); ok {
			return []pwEntry{e}
		}
		return nil
	}
	defer f.Close()
	var us []pwEntry
	s := bufio.NewScanner(f)
	for s.Scan() {
		e := strings.Split(s.Text(), ":")
		if len(e) != 7 || strings.HasPrefix(e[0], "#") {
			continue
		}
		us = append(us, pwEntry(e))
	}
	return us
}

// getpwent's position: the users, read at the first call after setpwent
// (or the first), and how many it returned.
var (
	entUsers []pwEntry
	entRead  bool
	entNext  int
)

// Getpwent is getpwent: the next user of /etc/passwd, or null at the
// end.
func Getpwent() c23.Ptr {
	pwMu.Lock()
	if !entRead {
		entUsers, entRead, entNext = users(), true, 0
	}
	if entNext >= len(entUsers) {
		pwMu.Unlock()
		return c23.Ptr{}
	}
	e := entUsers[entNext]
	entNext++
	pwMu.Unlock()
	return pwResult(e, true)
}

// Setpwent is setpwent: getpwent starts again.
func Setpwent() {
	pwMu.Lock()
	defer pwMu.Unlock()
	entUsers, entRead, entNext = nil, false, 0
}

// Endpwent is endpwent.
func Endpwent() { Setpwent() }

// storePasswd stores e as the struct passwd at p, its strings made by
// str.
func storePasswd(p c23.Ptr, e pwEntry, str func(string) c23.Ptr) {
	uid, _ := strconv.ParseUint(e[2], 10, 32)
	gid, _ := strconv.ParseUint(e[3], 10, 32)
	var pw passwd
	c23.StorePtr(p, str(e[0]))
	c23.StorePtr(c23.Add(p, int(unsafe.Offsetof(pw.passwd))), str(e[1]))
	c23.Store32(c23.Add(p, int(unsafe.Offsetof(pw.uid))), uint32(uid))
	c23.Store32(c23.Add(p, int(unsafe.Offsetof(pw.gid))), uint32(gid))
	c23.StorePtr(c23.Add(p, int(unsafe.Offsetof(pw.gecos))), str(e[4]))
	c23.StorePtr(c23.Add(p, int(unsafe.Offsetof(pw.dir))), str(e[5]))
	c23.StorePtr(c23.Add(p, int(unsafe.Offsetof(pw.shell))), str(e[6]))
}

// pwResultR is the result of getpwuid_r and getpwnam_r: e as the struct
// passwd at pw, its strings in the n bytes at buf (ERANGE if they do not
// fit), and *result pw, or null if there is no such user.
func pwResultR(e pwEntry, ok bool, pw, buf c23.Ptr, n uint64, result c23.Ptr) int32 {
	c23.StorePtr(result, c23.Ptr{})
	if !ok {
		return 0
	}
	var need uint64
	for _, i := range [...]int{0, 1, 4, 5, 6} {
		need += uint64(len(e[i]) + 1)
	}
	if need > n {
		return c23.ERANGE
	}
	off := 0
	storePasswd(pw, e, func(s string) c23.Ptr {
		p := c23.Add(buf, off)
		copy(c23.BytesAt(p, uintptr(len(s)+1)), s+"\x00")
		off += len(s) + 1
		return p
	})
	c23.StorePtr(result, pw)
	return 0
}

// GetpwuidR is getpwuid_r.
func GetpwuidR(uid uint32, pw, buf c23.Ptr, n uint64, result c23.Ptr) int32 {
	e, ok := lookupUser("", uid)
	return pwResultR(e, ok, pw, buf, n, result)
}

// GetpwnamR is getpwnam_r.
func GetpwnamR(name, pw, buf c23.Ptr, n uint64, result c23.Ptr) int32 {
	s := c23.GoString(name)
	var e pwEntry
	ok := false
	if s != "" {
		e, ok = lookupUser(s, 0)
	}
	return pwResultR(e, ok, pw, buf, n, result)
}
