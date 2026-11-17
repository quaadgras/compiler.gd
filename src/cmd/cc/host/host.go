// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package host exposes public entry points for running cmd/cc in-process,
// as cmd/compile/host and cmd/cgo/host do for theirs.
//
// gd fork specific. cmd/cc keeps no package-level state, so calls need no
// serialising and may run concurrently.
package host

import (
	"io"

	"cmd/cc/internal/ccmain"
)

// Run drives one cmd/cc invocation in the calling process. args is the
// argv that the standalone binary would have received as os.Args[1:].
// status is non-zero when cc failed; err is reserved for failures to run
// it at all.
func Run(args []string, stdout, stderr io.Writer) (status int, err error) {
	return ccmain.Run(args, stdout, stderr), nil
}

// Translate translates the C source src, read from file, to the Go source
// of package pkg. With withMain, C's main becomes the program's main.
func Translate(file string, src []byte, pkg string, withMain bool) ([]byte, error) {
	return ccmain.Translate(file, src, pkg, withMain)
}

// A File is a Go file that Package writes.
type File = ccmain.File

// Package builds the C of a Go package that imports "C" with cmd/cc
// instead of cgo, as cmd/go does when cgo is off: goFiles are the files
// that import "C" and cFiles the C files, in dir. It returns the Go to
// compile with the package's other files.
func Package(dir, pkg string, goFiles, cFiles []string) ([]File, error) {
	return ccmain.Package(dir, pkg, goFiles, cFiles)
}
