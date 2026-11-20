// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

import (
	"internal/goarch"
	"unsafe"
)

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
func apeinit(c int32, v **byte) {
	apeinitHost()
	apeinitFile(c, v)
}

// The file of a GOOS=ape program is not what its Unix hosts run: the
// script at its start (see cmd/internal/apepack) runs a loader, which
// maps the program from the file and says what the file is: in an
// auxiliary vector entry on an ELF host, and to the entry point on
// darwin (rt0_ape_arm64.s).
//
// apeFile is the program's file, if its script started the program.
var apeFile *byte

// _AT_APE is the auxiliary vector entry the loader adds, of apeFile.
const _AT_APE = 0x657061

// apeFileAuxv sets apeFile from the auxiliary vector, which follows the
// arguments v and the environment.
//
//go:nosplit
func apeFileAuxv(c int32, v **byte) {
	n := c + 1
	for argv_index(v, n) != nil {
		n++
	}
	auxv := (*[1 << 28]uintptr)(add(unsafe.Pointer(v), uintptr(n+1)*goarch.PtrSize))
	for i := 0; auxv[i] != 0; i += 2 {
		if auxv[i] == _AT_APE {
			apeFile = (*byte)(unsafe.Pointer(auxv[i+1]))
		}
	}
}

// os_apeFile returns the name of the program's file, if the program was
// not started as an executable of the host's own.
//
//go:linkname os_apeFile os.apeFile
func os_apeFile() string {
	if apeFile == nil {
		return ""
	}
	return gostring(apeFile)
}
