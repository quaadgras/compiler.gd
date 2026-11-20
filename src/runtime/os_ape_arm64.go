// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

// The hosts of a GOOS=ape program on arm64, in the order of
// internal/syslist.APEHosts["arm64"].
const (
	apeLinux = 1 + iota
	apeDarwin
)

// apeGOOSNames are the hosts' names, by apehost.
var apeGOOSNames = [...]string{apeLinux: "linux", apeDarwin: "darwin"}

// apeinitFile finds the program's file, as the host's loader says it
// (darwin's says it to the entry point).
//
//go:nosplit
func apeinitFile(c int32, v **byte) {
	if apehost == apeLinux {
		apeFileAuxv(c, v)
	}
}
