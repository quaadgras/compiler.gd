// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build portable

package runtime

// The hosts of a GOOS=portable program on amd64, in the order of
// internal/syslist.PortableHosts["amd64"].
const (
	portableLinux = 1 + iota
	portableFreebsd
	portableWindows
	portableDarwin
)

// portableGOOSNames are the hosts' names, by portablehost.
var portableGOOSNames = [...]string{portableLinux: "linux", portableFreebsd: "freebsd", portableWindows: "windows", portableDarwin: "darwin"}

// portableinitFile finds the program's file, as the host's loader says it
// (darwin's says it to the entry point).
//
//go:nosplit
func portableinitFile(c int32, v **byte) {
	if portablehost == portableLinux || portablehost == portableFreebsd {
		portableFileAuxv(c, v)
	}
}
