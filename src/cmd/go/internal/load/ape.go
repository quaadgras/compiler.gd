// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"go/build"
	"internal/syslist"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"cmd/go/internal/cfg"
	"cmd/go/internal/search"
)

// A GOOS=ape program runs on several host systems (syslist.APEHosts).
// A standard package whose files differ between hosts, such as syscall,
// is built once per host, as the host builds it, under the import path
// ape/HOST/PATH: the host files of the runtime (copied per host by
// cmd/apegen) import such variants by that path, and a variant's own
// imports of packages that differ between hosts are its host's variants.

// apeVariantPath splits a variant's import path into its host and the
// path of the package it is a variant of.
func apeVariantPath(path string) (host, rest string, ok bool) {
	if cfg.BuildContext.GOOS != "ape" {
		return "", "", false
	}
	rest, ok = strings.CutPrefix(path, "ape/")
	if !ok {
		return "", "", false
	}
	host, rest, ok = strings.Cut(rest, "/")
	if !ok || !slices.Contains(syslist.APEHosts, host) {
		return "", "", false
	}
	return host, rest, true
}

// apeResolveImport resolves path, imported by parentPath, to a variant.
func apeResolveImport(path, parentPath string) (newPath, dir string, ok bool) {
	if _, rest, ok := apeVariantPath(path); ok {
		return path, filepath.Join(cfg.GOROOTsrc, filepath.FromSlash(rest)), true
	}
	host, _, ok := apeVariantPath(parentPath)
	if !ok || !search.IsStandardImportPath(path) || path == "unsafe" || path == "C" || !apeDiffers(path, host) {
		return "", "", false
	}
	return "ape/" + host + "/" + path, filepath.Join(cfg.GOROOTsrc, filepath.FromSlash(path)), true
}

// apeContext returns the build context of a variant.
func apeContext(path string) (build.Context, bool) {
	host, _, ok := apeVariantPath(path)
	if !ok {
		return build.Context{}, false
	}
	ctx := cfg.BuildContext
	ctx.GOOS = host
	return ctx, true
}

var apeDiffersCache sync.Map // "host path" -> bool

// apeDiffers reports whether the standard package path has other files
// on host than it has for ape.
func apeDiffers(path, host string) bool {
	key := host + " " + path
	if v, ok := apeDiffersCache.Load(key); ok {
		return v.(bool)
	}
	dir := filepath.Join(cfg.GOROOTsrc, filepath.FromSlash(path))
	// A package cmd/apegen generates is one package for every host, and
	// so are internal/goos and the runtime.
	if path == "runtime" || path == "internal/goos" {
		apeDiffersCache.Store(key, false)
		return false
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "zape_dispatch_*")); len(m) > 0 {
		apeDiffersCache.Store(key, false)
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
	apeDiffersCache.Store(key, differs)
	return differs
}
