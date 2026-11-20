// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build portable

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// A GOOS=portable program runs on any of several host systems. The files that
// every host builds are built once; each host's own files are copies,
// made by cmd/portgen, whose names have the host's prefix
// (linux_newosproc), and zportable_dispatch_$GOARCH.go chooses between them
// by portablehost.

// portablehost is the system the program runs on. The entry point sets it,
// before any Go code runs. Other packages' dispatch code reads it.
//
//go:linkname portablehost
var portablehost uint8

// The hosts (internal/syslist.PortableHosts[GOARCH]) and their names, by
// portablehost, are in os_portable_$GOARCH.go.

// portableGOOS is GOOS: the compiler compiles every use of GOOS (and of a
// constant computed from it) as a call of portableGOOS.
//
//go:nosplit
func portableGOOS() string {
	return portableGOOSNames[portablehost]
}

// portableHost is the index of the host in internal/syslist.PortableHosts: the
// compiler compiles a use of a constant that differs between hosts as
// the element portableHost of its hosts' values.
//
//go:nosplit
func portableHost() int {
	return int(portablehost) - 1
}

// portableinit sets what differs between hosts. args calls it first.
//
//go:nosplit
func portableinit(c int32, v **byte) {
	portableinitHost()
	portableinitFile(c, v)
}

// The file of a GOOS=portable program is not what its Unix hosts run: the
// script at its start (see cmd/internal/portpack) runs a loader, which
// maps the program from the file and says what the file is: in an
// auxiliary vector entry on an ELF host, and to the entry point on
// darwin (rt0_portable_arm64.s).
//
// portableFile is the program's file, if its script started the program.
var portableFile *byte

// _AT_PORTABLE is the auxiliary vector entry the loader adds, of portableFile.
const _AT_PORTABLE = 0x706f7274

// portableFileAuxv sets portableFile from the auxiliary vector, which follows the
// arguments v and the environment.
//
//go:nosplit
func portableFileAuxv(c int32, v **byte) {
	n := c + 1
	for argv_index(v, n) != nil {
		n++
	}
	auxv := (*[1 << 28]uintptr)(add(unsafe.Pointer(v), uintptr(n+1)*goarch.PtrSize))
	for i := 0; auxv[i] != 0; i += 2 {
		if auxv[i] == _AT_PORTABLE {
			portableFile = (*byte)(unsafe.Pointer(auxv[i+1]))
		}
	}
}

// os_portableFile returns the name of the program's file, if the program was
// not started as an executable of the host's own.
//
//go:linkname os_portableFile os.portableFile
func os_portableFile() string {
	if portableFile == nil {
		return ""
	}
	return gostring(portableFile)
}
