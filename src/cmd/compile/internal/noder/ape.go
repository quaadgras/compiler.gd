// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"os"
	"strings"

	"cmd/compile/internal/types2"
	"cmd/internal/objabi"
)

// apeVariantLinkname links a function without a body in a GOOS=ape host
// variant of a package (ape/linux/syscall is syscall as Linux builds it)
// to the package's own name for it (syscall.f), which other packages
// push it to by linkname, unless the variant's assembly defines it.
func (pw *pkgWriter) apeVariantLinkname(obj *types2.Func) {
	path := pw.gd.Ctxt.Pkgpath
	orig := objabi.APEVariantOf(path)
	if orig == path {
		return
	}
	// A one-argument //go:linkname names the function by its own path.
	if l, ok := pw.linknames[obj]; ok && l.remote != objabi.PathToPrefix(path)+"."+obj.Name() {
		return
	}
	if pw.apeAsmDefs == nil {
		pw.apeAsmDefs = map[string]bool{}
		if file := pw.gd.Flag.SymABIs; file != "" {
			data, _ := os.ReadFile(file)
			for _, line := range strings.Split(string(data), "\n") {
				if f := strings.Fields(line); len(f) == 3 && f[0] == "def" {
					pw.apeAsmDefs[f[1]] = true
				}
			}
		}
	}
	if pw.apeAsmDefs[path+"."+obj.Name()] {
		return
	}
	pw.linknames[obj] = struct {
		remote string
		std    bool
	}{orig + "." + obj.Name(), false}
}
