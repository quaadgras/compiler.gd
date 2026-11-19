// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package apegen makes the host files of standard packages for GOOS=ape.
// cmd/go runs it when it builds for GOOS=ape, and puts what it makes
// in place of the packages' files (an overlay); cmd/apegen shows it.
//
// A GOOS=ape program runs on several host systems
// (internal/syslist.APEHosts). A file of a package that builds for every
// host is shared: it is built once, as it is, except for the
// declarations in it that depend on the host, which are copied per host
// (zape_ape_FILE is the rest of it). A file that builds only for some
// hosts is copied once per host, with every package-level name
// it declares prefixed by the host ("newosproc" becomes
// "linux_newosproc", "Stat" becomes "Linux_Stat"), and the generated
// zape_dispatch_$GOARCH.go gives the package what the hosts have in
// common:
//
//   - a type every host declares the same way is declared once, and
//     the hosts' copies use it;
//   - a function every host declares becomes a function that calls the
//     copy of the host the program runs on (apehost, set at startup),
//     if the shared files use it or it is exported; so does a method a
//     host file declares on a shared type;
//   - a constant with the same value everywhere stays a constant; one
//     that differs becomes a variable holding the host's value;
//   - a type that differs becomes the first host's type if shared code
//     uses its fields or it is exported (functions convert the other
//     hosts' field by field), a struct of every host's type if a shared
//     struct embeds it (the runtime's mOS, with the fields shared code
//     uses hoisted out of the hosts' types), and otherwise an opaque type
//     converted by pointer (as large as the largest host's if held by
//     value, of no size if only ever behind a pointer);
//   - a variable of the same type everywhere is one variable, set from
//     the host's, which the hosts' copies use too; another becomes a
//     variable set from the host's.
//
// Generated files have line directives: positions in them are the
// original file's.
//
// What a host copy uses of another generated package is that host's:
// os's Linux copy calls syscall.Linux_Stat with a syscall.Linux_Stat_t.
// A host file that imports a package some host does not build (such as
// internal/syscall/windows) imports the host's variant of it,
// ape/HOST/PATH, which cmd/go builds as the host builds it.
//
// What it cannot do is reported, for the package's hand-written ape files.
package apegen

import (
	"fmt"
	"go/build"
	"go/token"
	"internal/syslist"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// Config is what Generate generates for.
type Config struct {
	GOROOT string
	Hosts  []string // internal/syslist.APEHosts
	Arch   string

	// Tests dispatches what the package's own tests use, and leaves out
	// of ape the tests that depend on the host.
	Tests bool
	// Verbose reports the shared declarations copied per host and why.
	Verbose bool
	// MaxConsts is the constants that are the largest host's value
	// (pkg.Name, comma-separated).
	MaxConsts string
	// ConstTypes is the types of constants that differ between hosts
	// and that shared code does not use (pkg.Name=type, comma-separated).
	ConstTypes string
}

// DefaultConfig is the configuration cmd/go uses.
func DefaultConfig(goroot, arch string) Config {
	return Config{
		GOROOT:     goroot,
		Hosts:      syslist.APEHosts,
		Arch:       arch,
		Tests:      true,
		MaxConsts:  "runtime._NSIG",
		ConstTypes: "runtime._SIGURG=uint32",
	}
}

// A Result is what GOOS=ape builds of the packages instead of their
// files.
type Result struct {
	// Files maps a file (its absolute name) to its contents under
	// GOOS=ape: a generated file, or a file of the package with only
	// what is shared. A file mapped to nil does not build for ape.
	Files map[string][]byte
	// Reports is what apegen could not do, and with Verbose what it
	// copied per host and why.
	Reports []string
}

// The configuration of the current run.
var (
	arch       = new(string)
	tests      = new(bool)
	verbose    = new(bool)
	maxConsts  = new(string)
	constTypes = new(string)
	gorootDir  string
)

// fatal stops a run: Generate returns its error.
type fatal struct{ err error }

func fatalf(format string, args ...any) {
	panic(fatal{fmt.Errorf(format, args...)})
}

// Generate makes the host files of the standard packages paths, which
// are all the packages whose files differ between the hosts.
func Generate(cfg Config, paths []string) (res *Result, err error) {
	defer func() {
		if e := recover(); e != nil {
			f, ok := e.(fatal)
			if !ok {
				panic(e)
			}
			res, err = nil, f.err
		}
	}()
	fset = token.NewFileSet()
	splits = map[*token.File][][2]int{}
	*arch, *tests, *verbose, *maxConsts, *constTypes = cfg.Arch, cfg.Tests, cfg.Verbose, cfg.MaxConsts, cfg.ConstTypes
	gorootDir = cfg.GOROOT

	u := &universe{hosts: cfg.Hosts, pkgs: map[string]*pkgGen{}, generate: map[string]bool{}}
	for _, path := range paths {
		u.generate[path] = true
	}
	paths = slices.Sorted(slices.Values(paths))
	res = &Result{Files: map[string][]byte{}}
	for _, path := range paths {
		g := u.load(path)
		g.run()
		for _, r := range g.report {
			res.Reports = append(res.Reports, path+": "+r)
		}
		g.collect(res)
	}
	return res, nil
}

// Candidates returns the standard packages whose files differ between
// the hosts: those Generate makes host files of.
func Candidates(cfg Config) ([]string, error) {
	src := filepath.Join(cfg.GOROOT, "src")
	var out []string
	err := filepath.WalkDir(src, func(dir string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, dir)
		rel = filepath.ToSlash(rel)
		switch name := d.Name(); {
		case rel == "cmd", name == "testdata", rel != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")):
			return filepath.SkipDir
		case rel == ".", rel == "internal/goos", rel == "runtime/cgo":
			// (internal/goos has an ape file of its own; runtime/cgo is
			// cgo's, which GOOS=ape does not have.)
			return nil
		}
		var first []string
		built := false // by some host: not just tests
		for i, h := range cfg.Hosts {
			ctxt := build.Default
			ctxt.GOROOT = gorootDir
			ctxt.GOROOT, ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled = cfg.GOROOT, h, cfg.Arch, false
			files := []string{}
			if bp, err := ctxt.ImportDir(dir, 0); err == nil {
				built = built || len(bp.GoFiles) > 0
				files = slices.Concat(bp.GoFiles, bp.SFiles, []string{"|"}, bp.TestGoFiles, bp.XTestGoFiles)
			} else if _, ok := err.(*build.NoGoError); !ok {
				files = []string{"?" + h}
			}
			if i == 0 {
				first = files
			} else if !slices.Equal(files, first) && built {
				out = append(out, rel)
				break
			}
		}
		return nil
	})
	return out, err
}

// A universe is the packages of one run.
type universe struct {
	hosts       []string
	pkgs        map[string]*pkgGen
	generate    map[string]bool            // packages this run generates
	builds      map[string]bool            // package path -> builds for every host
	fileDiffers map[string]bool            // "host path" -> files differ from ape's
	fromAPE     map[string]map[string]bool // package path -> types other packages convert
}

func (u *universe) load(path string) *pkgGen {
	if g := u.pkgs[path]; g != nil {
		return g
	}
	g := newPkgGen(u, path)
	u.pkgs[path] = g
	return g
}

// buildsForAll reports whether the standard package path builds for
// every host.
func (u *universe) buildsForAll(path string) bool {
	if u.builds == nil {
		u.builds = map[string]bool{}
	}
	if v, ok := u.builds[path]; ok {
		return v
	}
	ok := true
	for _, h := range u.hosts {
		ctxt := build.Default
		ctxt.GOROOT = gorootDir
		ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled = h, *arch, false
		if _, err := ctxt.Import(path, "", 0); err != nil {
			ok = false
		}
	}
	u.builds[path] = ok
	return ok
}

// variant reports whether a host copy imports the host's variant of the
// standard package path: one this run does not generate whose files
// differ between the host and ape.
func (u *universe) variant(path, host string) bool {
	if u.generate[path] || path == "internal/goos" || path == "runtime" {
		return false
	}
	key := host + " " + path
	if v, ok := u.fileDiffers[key]; ok {
		return v
	}
	hctx := build.Default
	hctx.GOROOT = gorootDir
	hctx.GOOS, hctx.GOARCH, hctx.CgoEnabled = host, *arch, false
	actx := hctx
	actx.GOOS = "ape"
	hp, herr := hctx.Import(path, "", 0)
	ap, aerr := actx.Import(path, "", 0)
	v := (herr == nil) != (aerr == nil) || herr == nil && !slices.Equal(hp.GoFiles, ap.GoFiles)
	if u.fileDiffers == nil {
		u.fileDiffers = map[string]bool{}
	}
	u.fileDiffers[key] = v
	return v
}

// needFromAPE notes that another package converts path's public type
// name from the ape type to a host's.
func (u *universe) needFromAPE(path, name string) {
	if u.fromAPE == nil {
		u.fromAPE = map[string]map[string]bool{}
	}
	if u.fromAPE[path] == nil {
		u.fromAPE[path] = map[string]bool{}
	}
	u.fromAPE[path][name] = true
}

func goroot(path string) string {
	return filepath.Join(gorootDir, "src", filepath.FromSlash(path))
}
