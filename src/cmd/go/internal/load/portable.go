// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"go/build"
	"internal/buildcfg"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"cmd/go/internal/portfiles"
	"cmd/go/internal/cfg"
	"cmd/go/internal/search"
)

// A GOOS=portable program runs on several host systems (syslist.PortableHosts).
// A standard package whose files differ between hosts, such as syscall,
// is built once per host, as the host builds it, under the import path
// portable/HOST/PATH: the host files of the runtime (copied per host by
// cmd/portgen) import such variants by that path, and a variant's own
// imports of packages that differ between hosts are its host's variants.

// portableVariantPath splits a variant's import path into its host and the
// path of the package it is a variant of.
func portableVariantPath(path string) (host, rest string, ok bool) {
	if cfg.BuildContext.GOOS != "portable" {
		return "", "", false
	}
	rest, ok = strings.CutPrefix(path, "portable/")
	if !ok {
		return "", "", false
	}
	host, rest, ok = strings.Cut(rest, "/")
	if !ok || !slices.Contains(buildcfg.PortableHosts(cfg.BuildContext.GOARCH), host) {
		return "", "", false
	}
	return host, rest, true
}

// portableResolveImport resolves path, imported by parentPath, to a variant.
func portableResolveImport(path, parentPath string) (newPath, dir string, ok bool) {
	if _, rest, ok := portableVariantPath(path); ok {
		return path, filepath.Join(cfg.GOROOTsrc, filepath.FromSlash(rest)), true
	}
	host, _, ok := portableVariantPath(parentPath)
	if !ok || !search.IsStandardImportPath(path) || path == "unsafe" || path == "C" || !portableDiffers(path, host) {
		return "", "", false
	}
	return "portable/" + host + "/" + path, filepath.Join(cfg.GOROOTsrc, filepath.FromSlash(path)), true
}

// portableContext returns the build context of a variant.
func portableContext(path string) (build.Context, bool) {
	host, _, ok := portableVariantPath(path)
	if !ok {
		return build.Context{}, false
	}
	ctx := cfg.BuildContext
	ctx.GOOS = host
	return ctx, true
}

var portableDiffersCache sync.Map // "host path" -> bool

// portableDiffers reports whether the standard package path has other files
// on host than it has for portable.
func portableDiffers(path, host string) bool {
	key := host + " " + path
	if v, ok := portableDiffersCache.Load(key); ok {
		return v.(bool)
	}
	dir := filepath.Join(cfg.GOROOTsrc, filepath.FromSlash(path))
	// A package cmd/portgen generates is one package for every host, and
	// so are internal/goos and the runtime.
	if path == "runtime" || path == "internal/goos" {
		portableDiffersCache.Store(key, false)
		return false
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "zportable_dispatch_*")); len(m) > 0 {
		portableDiffersCache.Store(key, false)
		return false
	}
	hctx := cfg.BuildContext
	hctx.GOOS = host
	hp, herr := hctx.ImportDir(dir, 0)
	ap, aerr := cfg.BuildContext.ImportDir(dir, 0)
	differs := false
	switch {
	case herr != nil || aerr != nil:
		differs = (herr == nil) != (aerr == nil)
	default:
		differs = !slices.Equal(hp.GoFiles, ap.GoFiles) || !slices.Equal(hp.SFiles, ap.SFiles)
	}
	portableDiffersCache.Store(key, differs)
	return differs
}

var portableUserDone bool

// portableUser puts, once, portgen's files of the packages outside the
// standard library that pkgs have (with their dependencies and tests'
// imports) in place, and reports whether there were any.
func portableUser(pkgs []*Package) bool {
	if cfg.BuildContext.GOOS != "portable" || portableUserDone {
		return false
	}
	portableUserDone = true
	var list []portfiles.Pkg
	seen := map[string]bool{}
	for _, p := range PackageList(pkgs) {
		if p.Standard || p.Dir == "" || seen[p.ImportPath] {
			continue
		}
		seen[p.ImportPath] = true
		list = append(list, portfiles.Pkg{ImportPath: p.ImportPath, Dir: p.Dir})
	}
	return len(list) > 0 && portfiles.User(list)
}
