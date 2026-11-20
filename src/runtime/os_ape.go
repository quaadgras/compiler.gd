// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

import _ "unsafe" // for linkname

// A GOOS=ape program runs on any of several host systems. The files that
// every host builds are built once; each host's own files are copies,
// made by cmd/apegen, whose names have the host's prefix
// (linux_newosproc), and zape_dispatch_$GOARCH.go chooses between them
// by apehost.

// apehost is the system the program runs on. The entry point sets it,
// before any Go code runs. Other packages' dispatch code reads it.
//
//go:linkname apehost
var apehost uint8

// The hosts (internal/syslist.APEHosts[GOARCH]) and their names, by
// apehost, are in os_ape_$GOARCH.go.

// apeGOOS is GOOS: the compiler compiles every use of GOOS (and of a
// constant computed from it) as a call of apeGOOS.
//
//go:nosplit
func apeGOOS() string {
	return apeGOOSNames[apehost]
}

// apeHost is the index of the host in internal/syslist.APEHosts: the
// compiler compiles a use of a constant that differs between hosts as
// the element apeHost of its hosts' values.
//
//go:nosplit
func apeHost() int {
	return int(apehost) - 1
}

// apeinit sets what differs between hosts. args calls it first.
//
//go:nosplit
func apeinit() {
	apeinitHost()
}
