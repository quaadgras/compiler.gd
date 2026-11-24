// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package utsname

import (
	c23 "internal/c23"
	"os"
	"runtime"
	"strings"
)

// unameSys are uname's sysname of GOOS.
var unameSys = map[string]string{
	"linux": "Linux", "android": "Linux", "darwin": "Darwin", "ios": "Darwin",
	"freebsd": "FreeBSD", "netbsd": "NetBSD", "openbsd": "OpenBSD",
	"dragonfly": "DragonFly", "solaris": "SunOS", "illumos": "SunOS",
	"aix": "AIX", "windows": "Windows_NT", "plan9": "Plan9", "js": "js",
	"wasip1": "WASI",
}

// unameMachine are uname's machine of GOARCH, as Linux names them.
var unameMachine = map[string]string{
	"amd64": "x86_64", "386": "i686", "arm64": "aarch64", "arm": "armv7l",
	"loong64": "loongarch64", "wasm": "wasm32",
}

// Uname is uname: sysname from GOOS, as the system calls itself; nodename
// from os.Hostname; release, version and domainname from Linux's
// /proc/sys/kernel (elsewhere, empty); machine from GOARCH, as Linux
// calls it (x86_64 for amd64, and on darwin arm64).
func Uname(name *c23.Void) int32 {
	sys := unameSys[runtime.GOOS]
	if sys == "" {
		sys = runtime.GOOS
	}
	mach := unameMachine[runtime.GOARCH]
	if mach == "" || runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		mach = runtime.GOARCH
	}
	host, _ := os.Hostname()
	kernel := func(f string) string {
		b, err := os.ReadFile("/proc/sys/kernel/" + f)
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(string(b), "\n")
	}
	fields := [6]string{sys, host, kernel("osrelease"), kernel("version"), mach, kernel("domainname")}
	if runtime.GOOS == "linux" && fields[5] == "" {
		fields[5] = "(none)"
	}
	b := c23.BytesAt(name, 6*65)
	clear(b)
	for i, s := range fields {
		copy(b[i*65:i*65+64], s)
	}
	return 0
}
