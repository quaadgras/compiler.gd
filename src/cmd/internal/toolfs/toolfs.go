// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package toolfs is the filesystem abstraction the gd fork toolchain
// (cmd/compile, cmd/asm, cmd/link, cmd/cgo) reads source and object
// files through. The standalone tools and the in-process tools share
// the same FS interface; the standalone path uses OSFS, which is a
// thin wrapper around os.Open / os.Stat / os.ReadDir, while in-process
// callers (cmd/go, cmd/go via cmd/{compile,link,asm,cgo}/host) can
// substitute an FS that serves files out of memory — used for the
// embedded-stdlib feature and as a precondition for compiling the
// toolchain itself to WebAssembly, which has no usable on-disk
// filesystem.
//
// FS extends fs.FS with the operations bio.Reader and the toolchain
// need: Stat (existence checks, mtime for cache invalidation),
// ReadDir (link walks .a archive directories), ReadFile (asm slurps
// .s sources whole), and a File type that implements io.Seeker —
// bio.Reader.MustSeek requires random-access reads.
package toolfs

import (
	"io"
	"io/fs"
	"os"
)

// FS is the toolchain filesystem abstraction.
type FS interface {
	// Open opens the named file for reading. The returned File
	// supports both sequential reads (io.Reader) and random access
	// (io.Seeker). bio.Reader and the cmd/asm lexer need both.
	Open(name string) (File, error)

	// Stat returns file info for name. Used for existence checks
	// and mtime-based cache invalidation.
	Stat(name string) (fs.FileInfo, error)

	// ReadDir lists the named directory.
	ReadDir(name string) ([]fs.DirEntry, error)

	// ReadFile reads the named file and returns its contents.
	// Equivalent to os.ReadFile for OSFS.
	ReadFile(name string) ([]byte, error)
}

// File is what FS.Open returns. It extends fs.File with io.Seeker
// because bio.Reader (used by cmd/link to read .a archives and by
// cmd/compile to read export data) seeks within the file.
type File interface {
	fs.File
	io.Seeker
}

// OSFS is the default FS: a transparent wrapper around the os
// package. The standalone cmd/{compile,asm,link,cgo} binaries use
// it; in-process callers can substitute another FS.
var OSFS FS = osFS{}

type osFS struct{}

func (osFS) Open(name string) (File, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (osFS) Stat(name string) (fs.FileInfo, error)        { return os.Stat(name) }
func (osFS) ReadDir(name string) ([]fs.DirEntry, error)   { return os.ReadDir(name) }
func (osFS) ReadFile(name string) ([]byte, error)         { return os.ReadFile(name) }
