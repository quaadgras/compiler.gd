// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package utmpx is utmpx.h's functions for cmd/cc: the user accounting
// database, read from Linux's /var/run/utmp.
package utmpx

import (
	"bytes"
	c23 "internal/c23"
	"os"
	"runtime"
	"sync"
	"unsafe"
)

// ut_type's values: glibc's.
const (
	EMPTY         = 0
	RUN_LVL       = 1
	BOOT_TIME     = 2
	NEW_TIME      = 3
	OLD_TIME      = 4
	INIT_PROCESS  = 5
	LOGIN_PROCESS = 6
	USER_PROCESS  = 7
	DEAD_PROCESS  = 8
)

// recSize is the size of glibc's struct utmpx on x86-64 (and 386), which
// is a record of /var/run/utmp:
//
//	short ut_type; pid_t ut_pid; char ut_line[32];	// 0, 4, 8
//	char ut_id[4]; char ut_user[32];		// 40, 44
//	char ut_host[256];				// 76
//	struct exit_status ut_exit;			// 332
//	int32_t ut_session;				// 336
//	struct { int32_t tv_sec, tv_usec; } ut_tv;	// 340
//	int32_t ut_addr_v6[4]; char __unused[20];	// 348, 364 (384 bytes)
const (
	recSize = 384
	offLine = 8
	offID   = 40
)

var (
	mu    sync.Mutex
	recs  [][]byte // the database's records, read when first asked for
	read  bool
	next  int
	entry c23.Ptr // the struct utmpx the functions return
)

// load reads the database, once after setutxent: the records of
// /var/run/utmp, where they are glibc's records of x86-64 (on another
// architecture, a file whose size is not a multiple of theirs has none).
// Elsewhere there are none.
func load() {
	if read {
		return
	}
	read, next, recs = true, 0, nil
	if runtime.GOOS != "linux" {
		return
	}
	b, err := os.ReadFile("/var/run/utmp")
	if err != nil {
		if b, err = os.ReadFile("/run/utmp"); err != nil {
			return
		}
	}
	if len(b)%recSize != 0 && runtime.GOARCH != "amd64" && runtime.GOARCH != "386" {
		return
	}
	for len(b) >= recSize {
		recs = append(recs, b[:recSize])
		b = b[recSize:]
	}
}

// result returns rec as the static struct utmpx.
func result(rec []byte) c23.Ptr {
	if c23.Addr(entry) == 0 {
		entry = c23.Malloc(recSize)
	}
	copy(c23.BytesAt(entry, recSize), rec)
	return entry
}

// recType is a record's ut_type.
func recType(rec []byte) int16 { return *(*int16)(unsafe.Pointer(&rec[0])) }

// Setutxent is setutxent: the database is read again, from its first
// entry.
func Setutxent() {
	mu.Lock()
	defer mu.Unlock()
	read = false
}

// Endutxent is endutxent.
func Endutxent() { Setutxent() }

// find returns the next record match accepts, or null.
func find(match func(rec []byte) bool) c23.Ptr {
	mu.Lock()
	defer mu.Unlock()
	load()
	for next < len(recs) {
		rec := recs[next]
		next++
		if match(rec) {
			return result(rec)
		}
	}
	return c23.Ptr(nil)
}

// Getutxent is getutxent: the next entry, or null at the end.
func Getutxent() c23.Ptr { return find(func([]byte) bool { return true }) }

// field is the NUL-terminated field of n bytes at off of rec.
func field(rec []byte, off, n int) []byte {
	f := rec[off : off+n]
	if i := bytes.IndexByte(f, 0); i >= 0 {
		f = f[:i]
	}
	return f
}

// Getutxid is getutxid: the next entry of ut's ut_type if that is
// RUN_LVL, BOOT_TIME, NEW_TIME or OLD_TIME; else the next of a process
// (INIT_PROCESS, LOGIN_PROCESS, USER_PROCESS or DEAD_PROCESS) with ut's
// ut_id.
func Getutxid(ut c23.Ptr) c23.Ptr {
	typ := int16(c23.Load16(ut))
	id := bytes.Clone(c23.BytesAt(c23.Add(ut, offID), 4))
	if i := bytes.IndexByte(id, 0); i >= 0 {
		id = id[:i]
	}
	return find(func(rec []byte) bool {
		t := recType(rec)
		switch typ {
		case RUN_LVL, BOOT_TIME, NEW_TIME, OLD_TIME:
			return t == typ
		case INIT_PROCESS, LOGIN_PROCESS, USER_PROCESS, DEAD_PROCESS:
			return (t == INIT_PROCESS || t == LOGIN_PROCESS || t == USER_PROCESS || t == DEAD_PROCESS) &&
				bytes.Equal(field(rec, offID, 4), id)
		}
		return false
	})
}

// Getutxline is getutxline: the next entry of type LOGIN_PROCESS or
// USER_PROCESS with ut's ut_line.
func Getutxline(ut c23.Ptr) c23.Ptr {
	line := bytes.Clone(c23.BytesAt(c23.Add(ut, offLine), 32))
	if i := bytes.IndexByte(line, 0); i >= 0 {
		line = line[:i]
	}
	return find(func(rec []byte) bool {
		t := recType(rec)
		return (t == LOGIN_PROCESS || t == USER_PROCESS) && bytes.Equal(field(rec, offLine, 32), line)
	})
}

// Pututxline is pututxline, which cannot write the database: null, errno
// EPERM (as glibc's for a process that may not).
func Pututxline(ut c23.Ptr) c23.Ptr {
	c23.SetErrno(c23.EPERM)
	return c23.Ptr(nil)
}
