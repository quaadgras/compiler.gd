// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syslog

import (
	c23 "internal/c23"
	"os"
	"strconv"
	"strings"
	"sync"
)

var syslogState = struct {
	sync.Mutex
	ident  string
	opened bool
	option int32
	mask   int32
}{mask: 0xff}

const logPID = 0x01

const logPerror = 0x20

func Openlog(ident c23.Ptr, option, facility int32) {
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	st.ident = ""
	if c23.Addr(ident) != 0 {
		st.ident = c23.GoString(ident)
	}
	st.option = option
	st.opened = true
}

func Closelog() {
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	st.ident, st.option, st.opened = "", 0, false
}

// Setlogmask is setlogmask: it sets the mask of the priorities logged,
// unless mask is 0, and returns the previous one.
func Setlogmask(mask int32) int32 {
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	old := st.mask
	if mask != 0 {
		st.mask = mask
	}
	return old
}

func Syslog(priority int32, format c23.Ptr, va ...any) { Vsyslog(priority, format, c23.VaStart(va)) }

// Vsyslog is vsyslog. The format's %m is the message of errno.
func Vsyslog(priority int32, format, ap c23.Ptr) {
	e := c23.Errno()
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	if st.mask&(1<<(priority&7)) == 0 || st.option&logPerror == 0 {
		return
	}
	f := strings.ReplaceAll(c23.GoString(format), "%m", strings.ReplaceAll(c23.ErrnoString(e), "%", "%%"))
	msg := string(c23.FormatC(f, c23.VaOf(ap)))
	ident := st.ident
	if ident == "" {
		ident = c23.Progname()
	}
	if st.option&logPID != 0 {
		ident += "[" + strconv.Itoa(os.Getpid()) + "]"
	}
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	c23.FileOf(c23.Stdio(2)).Write([]byte(ident + ": " + msg))
}
