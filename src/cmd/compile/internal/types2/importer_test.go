// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements the (temporary) plumbing to get importing to work.

package types2_test

import (
	"cmd/compile/internal/base"
	gcimporter "cmd/compile/internal/importer"
	"cmd/compile/internal/types2"
	"io"
)

// testGd is the *base.Invocation passed to gcimporter.Import in tests.
// gcimporter only uses gd for diag routing, so an empty Invocation is
// sufficient — there's no Ctxt for these tests because we never report
// positions through it.
var testGd = &base.Invocation{}

func defaultImporter() types2.Importer {
	return &gcimports{
		packages: make(map[string]*types2.Package),
	}
}

type gcimports struct {
	packages map[string]*types2.Package
	lookup   func(path string) (io.ReadCloser, error)
}

func (m *gcimports) Import(path string) (*types2.Package, error) {
	return m.ImportFrom(path, "" /* no vendoring */, 0)
}

func (m *gcimports) ImportFrom(path, srcDir string, mode types2.ImportMode) (*types2.Package, error) {
	if mode != 0 {
		panic("mode must be 0")
	}
	return gcimporter.Import(testGd, m.packages, path, srcDir, m.lookup)
}
