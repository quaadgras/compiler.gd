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
	"sync"
	"unsafe"
)

// passwd is C's struct passwd.
type passwd struct {
	name, passwd unsafe.Pointer
	uid, gid     uint32
	gecos, dir   unsafe.Pointer
	shell        unsafe.Pointer
}

var pwMu sync.Mutex

var pwBuf c23.Ptr

// pwEntry is a user: the fields of a line of /etc/passwd.
type pwEntry [7]string

// lookupUser finds the user of the name, or of uid if the name is "",
// in /etc/passwd, or else the program's own user as the environment
// tells of it.
func lookupUser(name string, uid uint32) (pwEntry, bool) {
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			e := strings.Split(s.Text(), ":")
			if len(e) != 7 || strings.HasPrefix(e[0], "#") {
				continue
			}
			if name != "" && e[0] == name {
				return pwEntry(e), true
			}
			if name == "" {
				if u, err := strconv.ParseUint(e[2], 10, 32); err == nil && uint32(u) == uid {
					return pwEntry(e), true
				}
			}
		}
	}
	me := os.Getenv("USER")
	if me == "" {
		me = os.Getenv("USERNAME")
	}
	if me == "" || (name != "" && name != me) || (name == "" && uid != uint32(os.Getuid())) {
		return pwEntry{}, false
	}
	home, _ := os.UserHomeDir()
	return pwEntry{me, "x", strconv.FormatUint(uint64(uint32(os.Getuid())), 10),
		strconv.FormatUint(uint64(uint32(os.Getgid())), 10), "", home, os.Getenv("SHELL")}, true
}

// pwResult returns the static struct passwd of e, or null (errno
// unchanged: no such user).
func pwResult(e pwEntry, ok bool) c23.Ptr {
	if !ok {
		return c23.Ptr{}
	}
	pwMu.Lock()
	defer pwMu.Unlock()
	if pwBuf.Obj == nil {
		pwBuf = c23.New[passwd]()
	}
	storePasswd(pwBuf, e, c23.CString)
	return pwBuf
}

// Getpwuid is getpwuid.
func Getpwuid(uid uint32) c23.Ptr { return pwResult(lookupUser("", uid)) }

// Getpwnam is getpwnam.
func Getpwnam(name c23.Ptr) c23.Ptr {
	n := c23.GoString(name)
	if n == "" {
		return c23.Ptr{}
	}
	return pwResult(lookupUser(n, 0))
}
