// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

import (
	"internal/bytealg"
	"internal/goarch"
	"internal/stringslite"
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
// maps the program from the file, or, on darwin, a copy of the program
// in a temporary file. Either way the script says what the file is.
var (
	apeFile *byte // the program's file, if its script started the program
	apeTemp *byte // the temporary file the program is, to be removed
)

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

// apeFileEnv sets apeFile and apeTemp from $GOAPEFILE and $GOAPETEMP,
// if the program is $GOAPETEMP (and they are not what a program that
// started this one was told): darwin has its name after the environment.
//
//go:nosplit
func apeFileEnv(c int32, v **byte) {
	var file, temp *byte
	n := c + 1
	for ; argv_index(v, n) != nil; n++ {
		p := argv_index(v, n)
		if q := apeCutPrefix(p, "GOAPEFILE="); q != nil {
			file = q
		}
		if q := apeCutPrefix(p, "GOAPETEMP="); q != nil {
			temp = q
		}
	}
	exe := argv_index(v, n+1)
	if file == nil || temp == nil || exe == nil {
		return
	}
	if q := apeCutPrefix(exe, "executable_path="); q != nil {
		exe = q
	}
	for i := uintptr(0); ; i++ {
		a, b := *(*byte)(add(unsafe.Pointer(exe), i)), *(*byte)(add(unsafe.Pointer(temp), i))
		if a != b {
			return
		}
		if a == 0 {
			break
		}
	}
	apeFile, apeTemp = file, temp
}

// apeCutPrefix returns what follows prefix in the C string p, or nil.
//
//go:nosplit
func apeCutPrefix(p *byte, prefix string) *byte {
	for i := 0; i < len(prefix); i++ {
		if *(*byte)(add(unsafe.Pointer(p), uintptr(i))) != prefix[i] {
			return nil
		}
	}
	return (*byte)(add(unsafe.Pointer(p), uintptr(len(prefix))))
}

func init() {
	if apeTemp == nil {
		return
	}
	// The temporary file and its directory (the script's) are of no
	// more use, and what the script said is not for other programs.
	temp := gostring(apeTemp)
	apeRemove(apeTemp, false)
	if i := bytealg.LastIndexByteString(temp, '/'); i > 0 {
		dir := []byte(temp[:i] + "\x00")
		apeRemove(&dir[0], true)
	}
	kept := envs[:0]
	for _, e := range envs {
		if !stringslite.HasPrefix(e, "GOAPEFILE=") && !stringslite.HasPrefix(e, "GOAPETEMP=") {
			kept = append(kept, e)
		}
	}
	envs = kept
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
