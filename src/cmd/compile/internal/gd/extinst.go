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
	"cmd/compile/internal/ir"
	"cmd/compile/internal/noder"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/bio"
)

// -d=skipextinst (see golang.org/issue/56718).
//
// Generic instantiations are DUPOK symbols: every package that uses
// Foo[int] compiles its own copy and the linker throws away all but
// one. On instantiation-heavy dependency graphs most of the compiler's
// work is spent on functions that a dependency already compiled.
//
// With -d=skipextinst, each compile appends a "__.GDINST" member to
// its output archive. It lists, one per line, the linker names of the
// instantiation text symbols that are present in this package's
// object file or in one of its (transitive) dependencies, each with
// what export data records of an imported function (noder's
// FuncSummaryText: its ABI, what the analyses found out about its
// parameters and results, its inlining cost). The linker ignores the
// member (short name, no .o extension).
//
// Importing compiles read those members for their DIRECT imports.
//
//   - At -d=skipextinst=1 they skip code generation for any function
//     on the union of the lists: the linker resolves the symbol from
//     the dependency that emitted it.
//
//   - At -d=skipextinst=3 they also stop compiling those functions in
//     the first place: an instantiation on a list is treated exactly
//     like a function imported from another package. Its body is not
//     read, analysed or compiled; callers use the recorded summary,
//     and the body is only read at call sites that inline it. Method
//     wrappers on the lists are not generated either.
//
// Levels 2 and 4 are 1 and 3 with a diagnostic for each skip.
//
// Only directly imported packages are consulted. The importcfg can
// carry packagefile entries beyond the import graph (e.g. for a
// test-variant build, cmd/go lists the plain variant of the package
// under test); those files are neither hashed into this action's
// cache key nor guaranteed to be in the final link, so a claim from
// one of them is worthless, and trusting it is unsound.

const gdinstMember = "__.GDINST"

// gdinstVersion is the first line of a __.GDINST member that records
// summaries; a member without it lists names only.
const gdinstVersion = "gdinst v3"

// An extInstEntry is what the direct imports claim about one
// instantiation symbol.
type extInstEntry struct {
	from string // archive the claim came from (first, for diagnostics)

	// line is what the providing package recorded of the function:
	// "L" or "-" (whether it analysed the function for inlining), a
	// space, and its summary. It is empty if nothing usable was
	// recorded (a wrapper, an older member) or if two imports
	// recorded different things: code generation can still be
	// skipped, but the function must be analysed locally.
	line string
}

// extInstState is the state of -d=skipextinst for one compile.
type extInstState struct {
	avail map[string]*extInstEntry

	// skipped lists the instantiations this compile relied on an
	// import for, in the order they were skipped.
	skipped []string
	seen    map[string]bool
}

func init() {
	noder.ExtInstFunc = func(gd *base.Invocation, fn *ir.Func) (string, bool) {
		if gd.Debug.SkipExtInst < 3 {
			return "", false
		}
		name := extInstName(gd, fn)
		st := extInst(gd)
		e := st.avail[name]
		if e == nil || e.line == "" {
			return "", false
		}
		flag, summary, ok := strings.Cut(e.line, " ")
		if !ok {
			return "", false
		}
		// Without the provider's inlining analysis (it was compiled
		// with -l), a compile that inlines must analyse the body itself.
		if gd.Flag.LowerL != 0 && flag != "L" {
			return "", false
		}
		st.skip(name)
		if gd.Debug.SkipExtInst == 4 {
			gd.WarnfAt(fn.Pos(), "skipextinst: importing %v (from %s)", fn, e.from)
		}
		return summary, true
	}
	noder.ExtInstWrapper = func(gd *base.Invocation, name string) bool {
		if gd.Debug.SkipExtInst < 3 {
			return false
		}
		st := extInst(gd)
		if st.avail[name] == nil {
			return false
		}
		st.skip(name)
		return true
	}
}

// extInstName returns the linker name of fn's text symbol, or "" if
// fn is not an instantiation that another package could provide.
func extInstName(gd *base.Invocation, fn *ir.Func) string {
	s := fn.Sym()
	if s == nil || s.Linkname != "" || !strings.Contains(s.Name, "[") || gd.Ctxt.Flag_linkshared {
		return ""
	}
	return s.Pkg.Prefix + "." + s.Name
}

func (st *extInstState) skip(name string) {
	if !st.seen[name] {
		st.seen[name] = true
		st.skipped = append(st.skipped, name)
	}
}

// extInst returns the state of this compile, with the set of
// instantiation symbols known to be available from its direct
// imports, which is loaded on first use.
func extInst(gd *base.Invocation) *extInstState {
	if st, ok := gd.ExtInstAvail.(*extInstState); ok {
		return st
	}
	st := &extInstState{avail: map[string]*extInstEntry{}, seen: map[string]bool{}}
	if gd.Flag.Cfg.PackageFile != nil {
		for path, p := range types.PkgMapOf(gd) {
			if !p.Direct {
				continue
			}
			if file, ok := gd.Flag.Cfg.PackageFile[path]; ok {
				readExtInst(file, st.avail)
			}
		}
	}
	gd.ExtInstAvail = st
	return st
}

// readExtInst scans the archive at file for a __.GDINST member and
// merges its claims into m. Errors are ignored: a missing or
// unreadable member just means nothing can be skipped on its account.
func readExtInst(file string, m map[string]*extInstEntry) {
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
			lines := strings.Split(string(data), "\n")
			versioned := len(lines) > 0 && lines[0] == gdinstVersion
			if versioned {
				lines = lines[1:]
			}
			for _, line := range lines {
				if line == "" {
					continue
				}
				sym, rest, _ := strings.Cut(line, "\t")
				if !versioned {
					rest = ""
				}
				mergeExtInst(m, sym, rest, file)
			}
			return
		}
		off += 60 + size + size&1
	}
}

// mergeExtInst records one import's claim about sym. Claims are merged
// the same way whatever order the imports are read in: any
// disagreement about the function's summary leaves none.
func mergeExtInst(m map[string]*extInstEntry, sym, line, from string) {
	e := m[sym]
	if e == nil {
		m[sym] = &extInstEntry{from: from, line: line}
		return
	}
	if e.line != line {
		e.line = ""
	}
}

// encodeExtInst returns the line of a function that this package
// compiled: whether it was analysed for inlining, and its summary.
func encodeExtInst(gd *base.Invocation, fn *ir.Func) string {
	flag := "-"
	if gd.Flag.LowerL != 0 {
		flag = "L"
	}
	return flag + " " + noder.FuncSummaryText(fn.Nname)
}

// skipExtInstFunc reports whether an imported package provides the
// code for fn, so that it need not be compiled.
func skipExtInstFunc(gd *base.Invocation, fn *ir.Func) bool {
	if gd.Debug.SkipExtInst == 0 || len(fn.Body) == 0 {
		return false
	}
	name := extInstName(gd, fn)
	if name == "" {
		return false
	}
	st := extInst(gd)
	e := st.avail[name]
	if e == nil {
		return false
	}
	st.skip(name)
	if gd.Debug.SkipExtInst == 2 || gd.Debug.SkipExtInst == 4 {
		gd.WarnfAt(fn.Pos(), "skipextinst: skipping %v (from %s)", fn, e.from)
	}
	return true
}

// dumpExtInst writes the __.GDINST member, if the package has
// anything to say in it: every instantiation text symbol this package
// emitted, with its summary, plus every one it relied on a direct
// import for, with that import's claim (availability so propagates to
// importers, whose links always include this package's dep closure).
// An archive of a package that has none is what it always was.
func dumpExtInst(gd *base.Invocation, bout *bio.Writer) {
	local := make(map[string]*ir.Func)
	for _, fn := range typecheck.Target(gd).Funcs {
		if name := extInstName(gd, fn); name != "" && fn.Nname != nil {
			local[name] = fn
		}
	}
	var b strings.Builder
	seen := make(map[string]bool)
	for _, s := range gd.Ctxt.Text {
		if strings.Contains(s.Name, "[") && !seen[s.Name] {
			seen[s.Name] = true
			b.WriteString(s.Name)
			if fn := local[s.Name]; fn != nil {
				b.WriteString("\t")
				b.WriteString(encodeExtInst(gd, fn))
			}
			b.WriteString("\n")
		}
	}
	st := extInst(gd)
	for _, name := range st.skipped {
		if !seen[name] {
			seen[name] = true
			b.WriteString(name)
			if e := st.avail[name]; e != nil && e.line != "" {
				b.WriteString("\t")
				b.WriteString(e.line)
			}
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		return
	}
	start := startArchiveEntry(bout)
	bout.WriteString(gdinstVersion)
	bout.WriteString("\n")
	bout.WriteString(b.String())
	finishArchiveEntry(bout, start, gdinstMember)
}
