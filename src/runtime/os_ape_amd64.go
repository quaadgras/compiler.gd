// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

// The hosts of a GOOS=ape program on amd64, in the order of
// internal/syslist.APEHosts["amd64"].
const (
	apeLinux = 1 + iota
	apeFreebsd
	apeWindows
)

// apeGOOSNames are the hosts' names, by apehost.
var apeGOOSNames = [...]string{apeLinux: "linux", apeFreebsd: "freebsd", apeWindows: "windows"}

// apeinitFile finds the program's file, as the host's loader says it.
//
//go:nosplit
func apeinitFile(c int32, v **byte) {
	if apehost == apeLinux || apehost == apeFreebsd {
		apeFileAuxv(c, v)
	}
}

// apeRemove removes the file or directory name of a host that runs a
// copy of the program: none here.
func apeRemove(name *byte, dir bool) {}
