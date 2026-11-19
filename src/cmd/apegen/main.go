// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Apegen writes the host files of standard packages for GOOS=ape.
//
// A GOOS=ape program runs on several host systems
// (internal/syslist.APEHosts). A file of a package that builds for every
// host is shared: it is built once, as it is. A file that builds only
// for some hosts is copied once per host, with every package-level name
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
//     as large as the largest host's, converted by pointer;
//   - a variable becomes a variable set from the host's.
//
// What a host copy uses of another generated package is that host's:
// os's Linux copy calls syscall.Linux_Stat with a syscall.Linux_Stat_t.
// A host file that imports a package some host does not build (such as
// internal/syscall/windows) imports the host's variant of it,
// ape/HOST/PATH, which cmd/go builds as the host builds it.
//
// What it cannot do is reported, for the package's hand-written ape files.
//
// Usage:
//
//	go run cmd/apegen [-w] [-hosts linux,freebsd] [-arch amd64] pkg...
package main

import (
	"flag"
	"fmt"
	"go/build"
	"internal/syslist"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

var (
	write      = flag.Bool("w", false, "write the files (and remove old ones)")
	hostsFlag  = flag.String("hosts", strings.Join(syslist.APEHosts, ","), "host systems")
	arch       = flag.String("arch", "amd64", "architecture")
	constTypes = flag.String("types", "runtime._SIGURG=uint32", "types of constants that differ between hosts and that shared code does not use")
	tests      = flag.Bool("tests", true, "dispatch what the package's own tests use, and leave out of ape the tests that depend on the host")
	verbose    = flag.Bool("v", false, "report the shared files copied per host and why")
	maxConsts  = flag.String("max", "runtime._NSIG", "constants that are the largest host's value")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("apegen: ")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("usage: apegen [-w] [-hosts h1,h2] [-arch a] pkg...")
	}
	hosts := strings.Split(*hostsFlag, ",")
	u := &universe{hosts: hosts, pkgs: map[string]*pkgGen{}, generate: map[string]bool{}}
	for _, path := range flag.Args() {
		u.generate[path] = true
	}
	var paths []string
	for _, path := range flag.Args() {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	failed := false
	for _, path := range paths {
		g := u.load(path)
		g.run()
		for _, r := range g.report {
			fmt.Printf("%s: %s\n", path, r)
		}
		if err := g.writeAll(); err != nil {
			log.Print(err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
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
	return filepath.Join(build.Default.GOROOT, "src", filepath.FromSlash(path))
}
