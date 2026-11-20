// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package portpack makes the one file of a GOOS=portable program that runs on
// all its hosts, as Cosmopolitan's Actually Portable Executables do.
//
// The linker writes one image of the program for each GOARCH: a PE (if
// Windows is a host of the GOARCH) or a Mach-O (if darwin is) or an ELF
// executable. A PE or Mach-O image is an ELF executable too: its header
// area has an ELF header, and program headers for the same segments, at
// the same addresses and file offsets (see cmd/link/internal/ld's
// portable_image.go); only the first bytes of the file have to be the ELF
// header's for an ELF host to run it.
//
// The file of a program that has Windows for a host is the PE image,
// whose DOS header is also the start of a shell script, and the other
// images after it:
//
//	MZportable='<rest of the DOS header>
//	'
//	<script>
//	<PE headers, sections>
//	<other images>
//
// Windows loads the PE. A Unix kernel does not know the file, so the
// shell that asked it to run the file runs it as a script instead. (The
// file of a program that is not for Windows starts with "#!/bin/sh" and
// the script, which a Unix kernel runs itself.) The
// script finds the image for the system and machine it runs on (uname)
// and runs the system's loader for it, which is in the file too, after
// the images, and maps the image from the program's file. Nothing of the
// program is copied: on an ELF host the loader is a temporary file of a
// few KB that removes itself (loader/loader.c); darwin's
// (loader/loader_darwin.c) is kept, one file for every program.
package portpack

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

const (
	// StubSize is the size of the DOS header and the script after it,
	// and so the offset of the PE header.
	StubSize = 0x800

	// magic starts the file: "MZ" and the start of a shell variable
	// assignment whose single quotes hold the rest of the DOS header.
	magic = "MZportable='\n"

	// shMagic starts a file that is not for Windows.
	shMagic = "#!/bin/sh\n"

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

	// PT_PORTABLE_DARWIN is the type of the program header of an image
	// that says where the table of its darwin imports is, if darwin
	// runs the image as the ELF executable it is (see cmd/link's
	// portable_image.go).
	PT_PORTABLE_DARWIN = 0x6f706f01

	scriptOff = 0x40 // after the DOS header
	elfHdrLen = 64   // of an ELF64 header
)

// DOSStub returns the DOS header of a GOOS=portable PE, which the PE header
// follows at StubSize. Its script says the file is only Windows's part.
func DOSStub() []byte {
	return stub("'\necho \"$0: this is the Windows executable of a GOOS=portable program\" >&2\nexit 1\n")
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
			return 0, fmt.Errorf("portpack: a PE not written for GOOS=portable")
		}
		return kindPE, nil
	case bytes.HasPrefix(b, []byte("\xcf\xfa\xed\xfe")):
		return kindMachO, nil
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return kindELF, nil
	}
	return 0, fmt.Errorf("portpack: an image of no known format")
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
		// Windows's image is the file's start.
		if slices.Contains(im.Hosts, "windows") {
			if k != kindPE {
				return nil, fmt.Errorf("portpack: windows host of an image that is no PE")
			}
			if pe >= 0 {
				return nil, fmt.Errorf("portpack: two images for Windows")
			}
			pe = i
		}
	}
	// The script is in the DOS header of Windows's PE or, in a file
	// that is not for Windows, a script as Unix kernels know them (and
	// run themselves).
	var out []byte
	scriptOff := scriptOff
	if pe >= 0 {
		out = append(out, images[pe].Data...)
		if bytes.IndexByte(out[len(magic):scriptOff], '\'') >= 0 {
			return nil, fmt.Errorf("portpack: quote in the DOS header")
		}
	} else {
		out = bytes.Repeat([]byte{'\n'}, StubSize)
		scriptOff = 0
	}

	var manifest, cases strings.Builder
	var tail []func()
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
			return nil, fmt.Errorf("portpack: no uname -m for GOARCH %s", im.GOARCH)
		}
		arches[im.GOARCH] = true
		fmt.Fprintf(&manifest, "#image %s %d %d %s\n", im.GOARCH, off, len(im.Data), strings.Join(im.Hosts, ","))

		// The hosts whose loader maps the image as it is (darwin's: a
		// Mach-O, or an ELF executable with a table of its imports),
		// and those whose loader maps it as an ELF executable.
		var native, elfHosts []string
		for _, h := range im.Hosts {
			if h == "windows" {
				continue
			}
			u, ok := unames[h]
			if !ok {
				return nil, fmt.Errorf("portpack: no uname -s for GOOS %s", h)
			}
			pat := u + "/" + im.GOARCH
			switch {
			case h == "darwin":
				native = append(native, pat)
			default:
				elfHosts = append(elfHosts, pat)
			}
		}
		if native != nil {
			// The loader is kept, under its hash.
			ld, ok := loaders["darwin/"+im.GOARCH]
			if !ok {
				return nil, fmt.Errorf("portpack: no darwin loader for GOARCH %s", im.GOARCH)
			}
			sum := sha256.Sum256([]byte(ld))
			tail = append(tail, func() {
				for len(out)%ELFAlign != 0 {
					out = append(out, 0)
				}
				fmt.Fprintf(&cases, "%s)l=%d n=%d s=%d h=%s;;\n", strings.Join(native, "|"), len(out), len(ld), off, hex.EncodeToString(sum[:8]))
				out = append(out, ld...)
			})
		}
		if elfHosts != nil {
			ld, ok := loaders[im.GOARCH]
			if !ok {
				return nil, fmt.Errorf("portpack: no loader for GOARCH %s", im.GOARCH)
			}
			x := off
			if kinds[i] != kindELF {
				x += kinds[i].elfOff()
			}
			// (The loaders are after the images.)
			tail = append(tail, func() {
				for len(out)%ELFAlign != 0 {
					out = append(out, 0)
				}
				fmt.Fprintf(&cases, "%s)l=%d n=%d s=%d x=%d;;\n", strings.Join(elfHosts, "|"), len(out), len(ld), off, x)
				out = append(out, ld...)
			})
		}
	}
	for _, f := range tail {
		f()
	}
	var archCases strings.Builder
	for _, a := range []string{"amd64", "arm64"} {
		if arches[a] {
			fmt.Fprintf(&archCases, "%s)a=%s;;", machines[a], a)
		}
	}

	// The script: only POSIX sh, uname, mkdir, dd, head -c, chmod, mv,
	// rm and rmdir. It copies the host's loader, the n bytes at l of the
	// file (a multiple of ELFAlign: some systems' tail -c is slow), to a
	// file and runs it, for the image at s:
	//
	// darwin's (h is its hash) is kept, in the user's temporary
	// directory, for every program and start: darwin checks the
	// signature of a new executable when it first runs, which takes a
	// while.
	//
	// An ELF host's is written at every start, into a new directory of
	// the user's own, and removes itself and the directory. x is where
	// the image's ELF header is.
	//
	// The loaders (loader/loader.c, loader/loader_darwin.c) are told the
	// file's name too, for os.Executable. The #image lines say where the
	// images are (see Images).
	align := strconv.Itoa(ELFAlign)
	start := "'\n" // the end of the DOS header's quote
	if pe < 0 {
		start = shMagic
	}
	script := start + manifest.String() +
		"o=$0\ncase $o in /*)p=$o;;*)p=$PWD/${o#./};;esac\n" +
		"m=`uname -m`\nu=`uname -s`\n" +
		"case $m in " + archCases.String() + "*)a=$m;;esac\n" +
		"case $u/$a in\n" + cases.String() +
		"*)echo \"$o: no executable for $u/$m in this file\" >&2;exit 1;;\n" +
		"esac\n" +
		"c(){ dd if=\"$o\" bs=" + align + " skip=$((l/" + align + ")) 2>/dev/null|head -c $n >\"$1\"&&chmod 700 \"$1\";}\n" +
		"if [ -n \"$h\" ];then\n" +
		"e=${TMPDIR:-$HOME}/.portable-$h\n" +
		"[ -x \"$e\" ]||{ c \"$e.$$\"&&mv -f \"$e.$$\" \"$e\"||{ rm -f \"$e.$$\";exit 1;};}\n" +
		"exec \"$e\" \"$p\" $s \"$o\" \"$@\"\n" +
		"fi\n" +
		"i=0\n" +
		"until t=${TMPDIR:-/tmp}/.portable$$.$i;(umask 077;mkdir \"$t\") 2>/dev/null;do\n" +
		"i=$((i+1));[ $i -lt 99 ]||{ echo \"$o: cannot make a directory in ${TMPDIR:-/tmp}\" >&2;exit 1;}\n" +
		"done\n" +
		"e=$t/${o##*/}\n" +
		"c \"$e\"||{ rm -f \"$e\";rmdir \"$t\";exit 1;}\n" +
		"exec \"$e\" \"$p\" $s $x \"$o\" \"$@\"\n" +
		"exit 1\n"
	if scriptOff+len(script) > StubSize {
		return nil, fmt.Errorf("portpack: the script is %d bytes, more than its %d", len(script), StubSize-scriptOff)
	}
	pad := byte(' ')
	if pe < 0 {
		pad = '\n'
	}
	copy(out[scriptOff:StubSize], bytes.Repeat([]byte{pad}, StubSize-scriptOff))
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
	if _, err := r.ReadAt(b, 0); err != nil {
		return nil, false
	}
	switch {
	case bytes.HasPrefix(b, []byte(magic)):
		b = b[scriptOff:]
	case bytes.HasPrefix(b, []byte(shMagic+"#image ")):
	default:
		return nil, false
	}
	var secs []Section
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 || f[0] != "#image" {
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

// loaders are the loaders, by GOARCH (of the ELF hosts) and darwin/GOARCH.
var loaders = func() map[string]string {
	m := map[string]string{}
	for k, gz := range loadersGz {
		r, err := gzip.NewReader(strings.NewReader(gz))
		if err != nil {
			panic(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			panic(err)
		}
		m[k] = string(b)
	}
	return m
}()
