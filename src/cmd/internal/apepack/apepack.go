// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package apepack makes the one file of a GOOS=ape program that runs on
// all its hosts, as Cosmopolitan's Actually Portable Executables do.
//
// The file is a Windows PE executable whose DOS header is also the start
// of a shell script:
//
//	MZgoape='<rest of the DOS header>
//	'
//	<script>
//	<PE headers, sections>
//	<ELF and Mach-O executables>
//
// Windows loads the PE. A Unix kernel does not know the file, so the
// shell that asked it to run the file runs it as a script instead; the
// script finds the executable for the system and machine it runs on
// (uname), copies it out of the file into a cache directory once and
// runs that.
//
// The linker writes the DOS header of a GOOS=ape PE (DOSStub); Pack puts
// the script in it and appends the other executables.
package apepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	// StubSize is the size of the DOS header and the script after it,
	// and so the offset of the PE header.
	StubSize = 0x800

	// magic starts the file: "MZ" and the start of a shell variable
	// assignment whose single quotes hold the rest of the DOS header.
	magic = "MZgoape='\n"

	scriptOff = 0x40 // after the DOS header
	align     = 0x1000
)

// DOSStub returns the DOS header of a GOOS=ape PE, which the PE header
// follows at StubSize. Its script says the file is only Windows's part.
func DOSStub() []byte {
	return stub("'\necho \"$0: this is the Windows executable of a GOOS=ape program\" >&2\nexit 1\n")
}

func stub(script string) []byte {
	b := bytes.Repeat([]byte{' '}, StubSize)
	copy(b, magic)
	b[0x3b] = '\n'
	binary.LittleEndian.PutUint32(b[0x3c:], StubSize) // e_lfanew
	copy(b[scriptOff:], script)
	return b
}

// HasPE reports whether the packed file b is a PE, not only a script.
func HasPE(b []byte) bool {
	return len(b) >= StubSize+4 && bytes.HasPrefix(b, []byte(magic)) && string(b[StubSize:StubSize+4]) == "PE\x00\x00"
}

// An Exe is an executable for some hosts of one GOARCH.
type Exe struct {
	GOARCH string
	Hosts  []string // GOOS values: linux, freebsd, darwin
	Data   []byte
}

// unames are the systems' names as uname -s says them.
var unames = map[string]string{
	"linux":   "Linux",
	"freebsd": "FreeBSD",
	"darwin":  "Darwin",
	"netbsd":  "NetBSD",
	"openbsd": "OpenBSD",
}

// machines are uname -m's names of GOARCHes.
var machines = map[string]string{
	"amd64": "x86_64|amd64",
	"arm64": "aarch64|arm64",
}

// Pack returns the file that is pe (written with DOSStub; or nil, for no
// Windows host) and has exes for the other hosts.
func Pack(pe []byte, exes []Exe) ([]byte, error) {
	var out []byte
	if pe != nil {
		if len(pe) < StubSize || !bytes.HasPrefix(pe, []byte(magic)) || binary.LittleEndian.Uint32(pe[0x3c:]) != StubSize {
			return nil, fmt.Errorf("apepack: the PE was not written for GOOS=ape")
		}
		out = append(out, pe...)
	} else {
		out = stub("")
	}
	if bytes.IndexByte(out[len(magic):scriptOff], '\'') >= 0 {
		return nil, fmt.Errorf("apepack: quote in the DOS header")
	}

	var cases strings.Builder
	arches := map[string]bool{}
	for _, e := range exes {
		for len(out)%align != 0 {
			out = append(out, 0)
		}
		off := len(out)
		out = append(out, e.Data...)
		if _, ok := machines[e.GOARCH]; !ok {
			return nil, fmt.Errorf("apepack: no uname -m for GOARCH %s", e.GOARCH)
		}
		arches[e.GOARCH] = true
		var pats []string
		for _, h := range e.Hosts {
			u, ok := unames[h]
			if !ok {
				return nil, fmt.Errorf("apepack: no uname -s for GOOS %s", h)
			}
			pats = append(pats, u+"/"+e.GOARCH)
		}
		sum := sha256.Sum256(e.Data)
		fmt.Fprintf(&cases, "%s)s=%d n=%d h=%s;;\n", strings.Join(pats, "|"), off, len(e.Data), hex.EncodeToString(sum[:8]))
	}
	var archCases strings.Builder
	for _, a := range []string{"amd64", "arm64"} {
		if arches[a] {
			fmt.Fprintf(&archCases, "%s)a=%s;;", machines[a], a)
		}
	}

	// (One assignment a line: some shells expand all of a line's
	// before they assign.)
	//
	// The script: only POSIX sh, uname, mkdir, tail, head -c, chmod
	// and mv. It copies the executable out under its hash (so a new
	// build is copied anew), atomically, then runs it with the file's
	// name as argv[0] where exec -a can.
	script := "'\n" +
		"o=$0\nm=`uname -m`\nu=`uname -s`\n" +
		"case $m in " + archCases.String() + "*)a=$m;;esac\n" +
		"case $u/$a in\n" + cases.String() +
		"*)echo \"$o: no executable for $u/$m in this file\" >&2;exit 1;;\n" +
		"esac\n" +
		"d=${XDG_CACHE_HOME:-${HOME:-${TMPDIR:-/tmp}}/.cache}/goape\ne=$d/$h\n" +
		"if ! [ -x \"$e\" ];then\n" +
		"mkdir -p \"$d\"||exit 1\n" +
		"tail -c +$((s+1)) \"$o\"|head -c $n >\"$e.$$\"&&chmod 755 \"$e.$$\"&&mv -f \"$e.$$\" \"$e\"||{ rm -f \"$e.$$\";exit 1;}\n" +
		"fi\n" +
		"if (exec -a x true) 2>/dev/null;then exec -a \"$o\" \"$e\" \"$@\";fi\n" +
		"exec \"$e\" \"$@\"\n" +
		"exit 1\n"
	if scriptOff+len(script) > StubSize {
		return nil, fmt.Errorf("apepack: the script is %d bytes, more than its %d", len(script), StubSize-scriptOff)
	}
	copy(out[scriptOff:StubSize], bytes.Repeat([]byte{' '}, StubSize-scriptOff))
	copy(out[scriptOff:], script)
	return out, nil
}

var exeRE = regexp.MustCompile(`(?m)^([A-Za-z0-9/|]+)\)s=([0-9]+) n=([0-9]+) h=`)

// A Section is where an executable is in a packed file, and what for.
type Section struct {
	Off, Size int64
	GOARCH    string
	Hosts     []string
}

// Exes returns where the executables after the PE are in r, if it is a
// packed file.
func Exes(r io.ReaderAt) ([]Section, bool) {
	b := make([]byte, StubSize)
	if _, err := r.ReadAt(b, 0); err != nil || !bytes.HasPrefix(b, []byte(magic)) {
		return nil, false
	}
	goos := map[string]string{}
	for g, u := range unames {
		goos[u] = g
	}
	var secs []Section
	for _, m := range exeRE.FindAllSubmatch(b[scriptOff:], -1) {
		off, err1 := strconv.ParseInt(string(m[2]), 10, 64)
		size, err2 := strconv.ParseInt(string(m[3]), 10, 64)
		if err1 != nil || err2 != nil {
			return nil, false
		}
		sec := Section{Off: off, Size: size}
		for _, pat := range strings.Split(string(m[1]), "|") {
			u, arch, ok := strings.Cut(pat, "/")
			if !ok || goos[u] == "" {
				return nil, false
			}
			sec.GOARCH = arch
			sec.Hosts = append(sec.Hosts, goos[u])
		}
		secs = append(secs, sec)
	}
	return secs, true
}
