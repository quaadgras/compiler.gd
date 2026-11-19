// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package apegen

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/types"
	"os"
	"path/filepath"
	"strings"
)

// A srcImporter type-checks the packages a host's packages import from
// their sources, once per run for each host. (go/importer's "source"
// importer would ask the go command where a module's package is, and
// type-check each package's imports anew.)
type srcImporter struct {
	u         *universe
	host      string
	ctxt      build.Context
	pkgs      map[string]*types.Package // by resolved path
	importing map[string]bool
}

func (u *universe) importer(host string) *srcImporter {
	if s := u.imps[host]; s != nil {
		return s
	}
	ctxt := build.Default
	ctxt.GOROOT = gorootDir
	ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled = host, *arch, false
	s := &srcImporter{u: u, host: host, ctxt: ctxt, pkgs: map[string]*types.Package{}, importing: map[string]bool{}}
	if u.imps == nil {
		u.imps = map[string]*srcImporter{}
	}
	u.imps[host] = s
	return s
}

func (s *srcImporter) Import(path string) (*types.Package, error) {
	return s.ImportFrom(path, "", 0)
}

func (s *srcImporter) ImportFrom(path, fromDir string, _ types.ImportMode) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if path == "C" {
		return nil, fmt.Errorf("cgo is not supported")
	}
	rpath, dir := s.u.resolve(s.host, fromDir, path)
	if p := s.pkgs[rpath]; p != nil {
		return p, nil
	}
	if s.importing[rpath] {
		return nil, fmt.Errorf("import cycle through %s", rpath)
	}
	s.importing[rpath] = true
	defer delete(s.importing, rpath)
	bp, err := s.ctxt.ImportDir(dir, 0)
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %v", rpath, dir, err)
	}
	var files []*ast.File
	for _, name := range bp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	conf := types.Config{
		Importer: s,
		Sizes:    types.SizesFor("gc", s.ctxt.GOARCH),
		// (What the package declares is what matters; it type-checks
		// when built.)
		Error: func(error) {},
	}
	pkg, _ := conf.Check(rpath, fset, files, nil)
	pkg.MarkComplete()
	s.pkgs[rpath] = pkg
	return pkg, nil
}

// resolve returns the package path and directory that the import path
// means in a file of the directory fromDir, as host builds it.
func (u *universe) resolve(host, fromDir, path string) (string, string) {
	if u.resolveFunc != nil {
		if rpath, dir, ok := u.resolveFunc(host, fromDir, path); ok {
			return rpath, dir
		}
	}
	src := filepath.Join(gorootDir, "src")
	if !isStd(path) {
		// The standard library's vendored packages.
		if rel, err := filepath.Rel(src, fromDir); fromDir != "" && err == nil && !strings.HasPrefix(rel, "..") {
			if dir := filepath.Join(src, "vendor", filepath.FromSlash(path)); isDir(dir) {
				return "vendor/" + path, dir
			}
		}
	}
	return path, filepath.Join(src, filepath.FromSlash(path))
}

// dirOf returns the directory of the package path.
func (u *universe) dirOf(path string) string {
	if u.resolveFunc != nil {
		if _, dir, ok := u.resolveFunc(u.hosts[0], "", path); ok {
			return dir
		}
	}
	return goroot(path)
}

// importOf is ctxt.Import of the package path, by its directory.
func (u *universe) importOf(ctxt build.Context, path string) (*build.Package, error) {
	bp, err := ctxt.ImportDir(u.dirOf(path), 0)
	if bp != nil {
		bp.ImportPath = path
	}
	return bp, err
}

func isStd(path string) bool {
	elem, _, _ := strings.Cut(path, "/")
	return !strings.Contains(elem, ".")
}

func isDir(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
