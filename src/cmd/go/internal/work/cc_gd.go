// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"fmt"
	"os"
	"path/filepath"

	cchost "cmd/cc/host"
	"cmd/go/internal/cfg"
)

// buildC runs cmd/cc in-process on a's package, a package that imports "C"
// built with cgo off (load.ccContext), and returns the Go files it wrote
// to the object directory: the package's import "C" files rewritten as cgo
// rewrites them, and its C translated to Go (gd fork, doc/gd/cc.md).
func (b *Builder) buildC(a *Action) ([]string, error) {
	p := a.Package
	sh := b.Shell(a)
	if cfg.BuildN || cfg.BuildX {
		sh.ShowCmd(p.Dir, "go tool cc -pkg %s (in-process) %v %v", p.Name, p.Internal.CCFiles, p.CFiles)
	}
	if cfg.BuildN {
		return nil, nil
	}
	files, err := cchost.Package(p.Dir, p.Name, p.Internal.CCFiles, p.CFiles)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", p.ImportPath, err)
	}
	var out []string
	for _, f := range files {
		name := filepath.Join(a.Objdir, f.Name)
		if err := os.WriteFile(name, f.Data, 0o666); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, nil
}
