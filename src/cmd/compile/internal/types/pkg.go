// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"cmd/compile/internal/base"
	"cmd/internal/objabi"
	"fmt"
	"strconv"
	"sync"
)

type Pkg struct {
	Path   string // string literal used in import statement, e.g. "internal/runtime/sys"
	Name   string // package name, e.g. "sys"
	Prefix string // escaped path for use in symbol table
	Syms   map[string]*Sym

	Direct bool // imported directly
	Local  bool // true for the package currently being compiled (set by cmd/compile main on the Pkg returned by NewPkg(gd, gd.Ctxt.Pkgpath, ""))

	// symsMu guards Syms against concurrent reads/writes when Pkg
	// is shared across in-process gd.Main invocations
	// (BuiltinPkg, UnsafePkg). Per-Invocation Pkgs never have
	// concurrent access — but it's cheap to hold the lock for those
	// too rather than gate the lookup behind a "shared?" check.
	symsMu sync.Mutex
}

// pkgMapOf returns gd's per-Invocation Pkg interning map, lazy-
// initialising on first call. Was a package-level map[string]*Pkg
// — see invocation.go for the rationale (cross-invocation aliasing
// of pseudo-runtime Pkgs and their Syms tables).
//
// Caller is responsible for holding pkgMapMu when reading or writing
// the returned map: NewPkg / PkgMapOf both acquire it. Backend
// goroutines call NewPkg concurrently (e.g. via TypeSymLookup), so
// the map needs a real lock — the upstream code held no lock because
// pkgMap was a process-global, the same Pkgs were always present by
// the time the backend ran, and concurrent map READS without writes
// were practically (if not formally) safe. Per-Invocation maps start
// empty and lazily fill, so first-write races with concurrent reads
// are real.
var pkgMapMu sync.Mutex

func pkgMapOfLocked(gd *base.Invocation) map[string]*Pkg {
	m, _ := gd.TypesPkgMap.(map[string]*Pkg)
	if m == nil {
		m = make(map[string]*Pkg)
		gd.TypesPkgMap = m
	}
	return m
}

// NewPkg returns a new Pkg for the given package path and name within
// gd's namespace. Unless name is the empty string, if the package
// exists already, the existing package name and the provided name
// must match.
//
// gd must be non-nil. Tests that don't have an Invocation should call
// NewPkgForTesting instead.
func NewPkg(gd *base.Invocation, path, name string) *Pkg {
	pkgMapMu.Lock()
	pkgMap := pkgMapOfLocked(gd)
	if p := pkgMap[path]; p != nil {
		pkgMapMu.Unlock()
		if name != "" && p.Name != name {
			panic(fmt.Sprintf("conflicting package names %s and %s for path %q", p.Name, name, path))
		}
		return p
	}

	p := new(Pkg)
	p.Path = path
	p.Name = name
	if path == "go.shape" {
		// Don't escape "go.shape", since it's not needed (it's a builtin
		// package), and we don't want escape codes showing up in shape type
		// names, which also appear in names of function/method
		// instantiations.
		p.Prefix = path
	} else {
		p.Prefix = objabi.PathToPrefix(path)
	}
	p.Syms = make(map[string]*Sym)
	pkgMap[path] = p
	pkgMapMu.Unlock()

	return p
}

// PkgMapOf returns a snapshot of gd's per-Invocation interning map.
// Returns a copy because the underlying map is locked by NewPkg —
// callers iterate the snapshot freely without holding the lock.
func PkgMapOf(gd *base.Invocation) map[string]*Pkg {
	pkgMapMu.Lock()
	defer pkgMapMu.Unlock()
	src := pkgMapOfLocked(gd)
	dst := make(map[string]*Pkg, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// NewPkgForTesting is the test-only entry point: routes through a
// process-global Invocation so interning works consistently across
// all NewPkgForTesting calls within a test binary. Tests don't need
// to thread an Invocation through fixture setup.
func NewPkgForTesting(path, name string) *Pkg {
	return NewPkg(testingInvocation, path, name)
}

var testingInvocation = new(base.Invocation)

var nopkg = &Pkg{
	Syms: make(map[string]*Sym),
}

func (pkg *Pkg) Lookup(name string) *Sym {
	s, _ := pkg.LookupOK(name)
	return s
}

// LookupOK looks up name in pkg and reports whether it previously existed.
func (pkg *Pkg) LookupOK(name string) (s *Sym, existed bool) {
	// TODO(gri) remove this check in favor of specialized lookup
	if pkg == nil {
		pkg = nopkg
	}
	pkg.symsMu.Lock()
	defer pkg.symsMu.Unlock()
	if s := pkg.Syms[name]; s != nil {
		return s, true
	}

	s = &Sym{
		Name: name,
		Pkg:  pkg,
	}
	pkg.Syms[name] = s
	return s, false
}

func (pkg *Pkg) LookupBytes(name []byte) *Sym {
	// TODO(gri) remove this check in favor of specialized lookup
	if pkg == nil {
		pkg = nopkg
	}
	pkg.symsMu.Lock()
	if s := pkg.Syms[string(name)]; s != nil {
		pkg.symsMu.Unlock()
		return s
	}
	pkg.symsMu.Unlock()
	str := InternString(name)
	return pkg.Lookup(str)
}

// LookupNum looks up the symbol starting with prefix and ending with
// the decimal n. If prefix is too long, LookupNum panics.
func (pkg *Pkg) LookupNum(prefix string, n int) *Sym {
	var buf [20]byte // plenty long enough for all current users
	copy(buf[:], prefix)
	b := strconv.AppendInt(buf[:len(prefix)], int64(n), 10)
	return pkg.LookupBytes(b)
}

// Selector looks up a selector identifier.
func (pkg *Pkg) Selector(gd *base.Invocation, name string) *Sym {
	if IsExported(name) {
		pkg = LocalPkg(gd)
	}
	return pkg.Lookup(name)
}

var (
	internedStringsmu sync.Mutex // protects internedStrings
	internedStrings   = map[string]string{}
)

func InternString(b []byte) string {
	internedStringsmu.Lock()
	s, ok := internedStrings[string(b)] // string(b) here doesn't allocate
	if !ok {
		s = string(b)
		internedStrings[s] = s
	}
	internedStringsmu.Unlock()
	return s
}
