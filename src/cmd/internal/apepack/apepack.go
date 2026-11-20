// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package apepack makes the one file of a GOOS=ape program that runs on
// all its hosts, as Cosmopolitan's Actually Portable Executables do.
//
// The linker writes one image of the program for each GOARCH: a PE (if
// Windows is a host of the GOARCH) or a Mach-O (if darwin is) or an ELF
// executable. A PE or Mach-O image is an ELF executable too: its header
// area has an ELF header, and program headers for the same segments, at
// the same addresses and file offsets (see cmd/link/internal/ld's
// ape_image.go); only the first bytes of the file have to be the ELF
// header's for an ELF host to run it.
//
// The file is the PE image, whose DOS header is also the start of a
// shell script, and the other images after it:
//
//	MZgoape='<rest of the DOS header>
//	'
//	<script>
//	<PE headers, sections>
//	<other images>
//
// Windows loads the PE. A Unix kernel does not know the file, so the
// shell that asked it to run the file runs it as a script instead; the
// script finds the image for the system and machine it runs on (uname),
// copies it out of the file into a cache directory once (with the ELF
// header at its start, for an ELF host of a PE or Mach-O image) and runs
// that.
package apepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
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

	// PEELFOff is where a PE has its ELF header (after its own, before
	// its first section).
	PEELFOff = 0xc00

	// MachOELFOff is where a Mach-O has its ELF header: after its own
	// and before MachOELFRoom more bytes of header.
	MachOELFOff  = 0x1000
	MachOELFRoom = 0x1000

	// ELFAlign is the alignment of the segments of an image that is an
	// ELF executable too: of their addresses and file offsets.
	ELFAlign = 0x4000

	scriptOff = 0x40 // after the DOS header
	elfHdrLen = 64   // of an ELF64 header
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

// An Image is the program for the hosts of one GOARCH.
type Image struct {
	GOARCH string
	Hosts  []string // GOOS values
	Data   []byte
}

type kind int

const (
	kindELF kind = iota
	kindPE
	kindMachO
)

func kindOf(b []byte) (kind, error) {
	switch {
	case bytes.HasPrefix(b, []byte(magic)):
		if len(b) < StubSize+4 || string(b[StubSize:StubSize+4]) != "PE\x00\x00" || binary.LittleEndian.Uint32(b[0x3c:]) != StubSize {
			return 0, fmt.Errorf("apepack: a PE not written for GOOS=ape")
		}
		return kindPE, nil
	case bytes.HasPrefix(b, []byte("\xcf\xfa\xed\xfe")):
		return kindMachO, nil
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return kindELF, nil
	}
	return 0, fmt.Errorf("apepack: an image of no known format")
}

// elfOff returns where the ELF header of an image of kind k is, or -1 if
// the image is an ELF executable itself.
func (k kind) elfOff() int {
	switch k {
	case kindPE:
		return PEELFOff
	case kindMachO:
		return MachOELFOff
	}
	return -1
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

// Pack returns the one file of images, of which at most one is a PE.
func Pack(images []Image) ([]byte, error) {
	kinds := make([]kind, len(images))
	pe := -1
	for i, im := range images {
		k, err := kindOf(im.Data)
		if err != nil {
			return nil, err
		}
		kinds[i] = k
		if k == kindPE {
			if pe >= 0 {
				return nil, fmt.Errorf("apepack: two PE images")
			}
			pe = i
		}
	}
	var out []byte
	if pe >= 0 {
		out = append(out, images[pe].Data...)
	} else {
		out = stub("")
	}
	if bytes.IndexByte(out[len(magic):scriptOff], '\'') >= 0 {
		return nil, fmt.Errorf("apepack: quote in the DOS header")
	}

	var manifest, cases strings.Builder
	arches := map[string]bool{}
	for i, im := range images {
		off := 0
		if i != pe {
			for len(out)%ELFAlign != 0 {
				out = append(out, 0)
			}
			off = len(out)
			out = append(out, im.Data...)
		}
		if _, ok := machines[im.GOARCH]; !ok {
			return nil, fmt.Errorf("apepack: no uname -m for GOARCH %s", im.GOARCH)
		}
		arches[im.GOARCH] = true
		fmt.Fprintf(&manifest, "#ape %s %d %d %s\n", im.GOARCH, off, len(im.Data), strings.Join(im.Hosts, ","))

		// The hosts that run the image as it is, and those that run
		// it as an ELF executable.
		var native, elfHosts []string
		for _, h := range im.Hosts {
			if h == "windows" {
				if kinds[i] != kindPE {
					return nil, fmt.Errorf("apepack: windows host of an image that is no PE")
				}
				continue
			}
			u, ok := unames[h]
			if !ok {
				return nil, fmt.Errorf("apepack: no uname -s for GOOS %s", h)
			}
			pat := u + "/" + im.GOARCH
			if h == "darwin" && kinds[i] == kindMachO || kinds[i] == kindELF {
				native = append(native, pat)
			} else {
				elfHosts = append(elfHosts, pat)
			}
		}
		sum := sha256.Sum256(im.Data)
		h := hex.EncodeToString(sum[:8])
		if native != nil {
			fmt.Fprintf(&cases, "%s)s=%d n=%d x= h=%s;;\n", strings.Join(native, "|"), off, len(im.Data), h)
		}
		if elfHosts != nil {
			fmt.Fprintf(&cases, "%s)s=%d n=%d x=%d h=%se;;\n", strings.Join(elfHosts, "|"), off, len(im.Data), off+kinds[i].elfOff(), h)
		}
	}
	var archCases strings.Builder
	for _, a := range []string{"amd64", "arm64"} {
		if arches[a] {
			fmt.Fprintf(&archCases, "%s)a=%s;;", machines[a], a)
		}
	}

	// The script: only POSIX sh, uname, mkdir, tail, head -c, chmod
	// and mv. It copies the image out under its hash (so a new build
	// is copied anew), atomically, with the ELF header at its start if
	// x says where that is, then runs it with the file's name as
	// argv[0] where exec -a can. (One assignment a line: some shells
	// expand all of a line's before they assign.) The #ape lines say
	// where the images are (see Images).
	script := "'\n" + manifest.String() +
		"o=$0\nm=`uname -m`\nu=`uname -s`\n" +
		"case $m in " + archCases.String() + "*)a=$m;;esac\n" +
		"case $u/$a in\n" + cases.String() +
		"*)echo \"$o: no executable for $u/$m in this file\" >&2;exit 1;;\n" +
		"esac\n" +
		"d=${XDG_CACHE_HOME:-${HOME:-${TMPDIR:-/tmp}}/.cache}/goape\ne=$d/$h\n" +
		"if ! [ -x \"$e\" ];then\n" +
		"mkdir -p \"$d\"||exit 1\n" +
		"if [ -n \"$x\" ];then\n" +
		"{ tail -c +$((x+1)) \"$o\"|head -c 64;tail -c +$((s+65)) \"$o\"|head -c $((n-64));}>\"$e.$$\"\n" +
		"else\n" +
		"tail -c +$((s+1)) \"$o\"|head -c $n >\"$e.$$\"\n" +
		"fi&&chmod 755 \"$e.$$\"&&mv -f \"$e.$$\" \"$e\"||{ rm -f \"$e.$$\";exit 1;}\n" +
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

// A Section is where an image is in a packed file, and what for.
type Section struct {
	Off, Size int64
	GOARCH    string
	Hosts     []string
}

// Images returns where the images are in r, if it is a packed file.
func Images(r io.ReaderAt) ([]Section, bool) {
	b := make([]byte, StubSize)
	if _, err := r.ReadAt(b, 0); err != nil || !bytes.HasPrefix(b, []byte(magic)) {
		return nil, false
	}
	var secs []Section
	for _, line := range strings.Split(string(b[scriptOff:]), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 || f[0] != "#ape" {
			continue
		}
		off, err1 := strconv.ParseInt(f[2], 10, 64)
		size, err2 := strconv.ParseInt(f[3], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, false
		}
		secs = append(secs, Section{off, size, f[1], strings.Split(f[4], ",")})
	}
	return secs, true
}
