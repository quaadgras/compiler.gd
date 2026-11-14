// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gd

import (
	"io"
	"os"
	"strconv"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
	"cmd/internal/bio"
)

// Experiment: -d=skipextinst (see golang.org/issue/56718).
//
// Generic instantiations are DUPOK symbols: every package that uses
// Foo[int] compiles its own copy and the linker throws away all but
// one. On instantiation-heavy dependency graphs most of the backend's
// work is compiling functions that a dependency already compiled.
//
// With -d=skipextinst, each compile appends a "__.GDINST" member to
// its output archive listing, one per line, the linker names of the
// instantiation text symbols that are present in this package's
// object file or in one of its (transitive) dependencies. Importing
// compiles read those members for every packagefile in the importcfg
// and skip code generation for any function on the union of the
// lists: the linker will resolve the symbol from the dependency that
// emitted it. The linker ignores the member (short name, no .o
// extension).

const gdinstMember = "__.GDINST"

// extInstAvail returns the set of instantiation symbols known to be
// emitted by this compile's dependencies, loading it on first use.
func extInstAvail(gd *base.Invocation) map[string]string {
	if m, ok := gd.ExtInstAvail.(map[string]string); ok {
		return m
	}
	m := make(map[string]string)
	// Only consult packages this compile actually imports. The importcfg
	// can carry packagefile entries beyond the import graph (e.g. for a
	// test-variant build, cmd/go lists the plain variant of the package
	// under test); those files are neither hashed into this action's
	// cache key nor guaranteed to be in the final link, so a claim from
	// one of them is worthless — and trusting it is unsound.
	for path, p := range types.PkgMapOf(gd) {
		if !p.Direct {
			continue
		}
		if file, ok := gd.Flag.Cfg.PackageFile[path]; ok {
			readExtInst(file, m)
		}
	}
	gd.ExtInstAvail = m
	return m
}

// readExtInst scans the archive at file for a __.GDINST member and
// adds each listed symbol name to m. Errors are ignored: a missing or
// unreadable member just means nothing can be skipped on its account.
func readExtInst(file string, m map[string]string) {
	f, err := os.Open(file)
	if err != nil {
		return
	}
	defer f.Close()

	var magic [8]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "!<arch>\n" {
		return
	}
	off := int64(8)
	var hdr [60]byte
	for {
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return
		}
		name := strings.TrimRight(string(hdr[0:16]), " ")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return
		}
		if name == gdinstMember {
			data := make([]byte, size)
			if _, err := io.ReadFull(io.NewSectionReader(f, off+60, size), data); err != nil {
				return
			}
			for _, line := range strings.Split(string(data), "\n") {
				if line != "" {
					m[line] = file
				}
			}
			return
		}
		off += 60 + size + size&1
	}
}

// dumpExtInst writes the __.GDINST member body: every instantiation
// text symbol this package emitted, plus every one it skipped because
// a dependency already provides it (so availability propagates to
// importers even if their importcfg omits the emitting package).
func dumpExtInst(gd *base.Invocation, bout *bio.Writer) {
	seen := make(map[string]bool)
	for _, s := range gd.Ctxt.Text {
		if strings.Contains(s.Name, "[") && !seen[s.Name] {
			seen[s.Name] = true
			bout.WriteString(s.Name)
			bout.WriteString("\n")
		}
	}
	for _, name := range gd.ExtInstSkipped {
		if !seen[name] {
			seen[name] = true
			bout.WriteString(name)
			bout.WriteString("\n")
		}
	}
}
