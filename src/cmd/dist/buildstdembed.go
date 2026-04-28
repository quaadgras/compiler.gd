// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mkstdembed regenerates cmd/internal/stdembed/stdlib.tar.gz: a
// gzip-tar of $GOROOT/src excluding src/cmd, embedded into bin/go
// so distributed binaries can materialise the stdlib at $GDPATH/std
// without shipping a separate src tree. Skips _test.go,
// testdata/, and dot-files. Symlinks are preserved.
//
// Wired into the gentab list in build.go so make.bash and
// rebuild-tools.sh both regenerate the archive on every run, which
// keeps it in sync with whatever sources are about to be compiled
// into bin/go.
func mkstdembed(dir, file string) {
	tmp := file + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fatalf("mkstdembed: create %s: %v", tmp, err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	count := 0

	// Walk $GOROOT/src into archive entries prefixed with "src/", with
	// the usual exclusions (cmd/, testdata/, _test.go, dotfiles).
	count += embedTree(tw, pathf("%s/src", goroot), "src/", embedSkipFilter)

	// pkg/include — small (~12 KB) but every stdlib asm file
	// includes textflag.h / funcdata.h, and gcc gets -I $GOROOT/
	// pkg/include during cgo compiles. Without it the toolchain
	// can't build runtime.
	count += embedTree(tw, pathf("%s/pkg/include", goroot), "pkg/include/", nil)

	// Top-level config / version files. Tiny, but cmd/go reads
	// go.env at startup and runtime.Version() / build cache hashing
	// peek at VERSION.
	count += embedFile(tw, pathf("%s/VERSION", goroot), "VERSION")
	count += embedFile(tw, pathf("%s/go.env", goroot), "go.env")

	if err := tw.Close(); err != nil {
		fatalf("mkstdembed: close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		fatalf("mkstdembed: close gzip: %v", err)
	}
	if err := f.Close(); err != nil {
		fatalf("mkstdembed: close: %v", err)
	}
	if err := os.Rename(tmp, file); err != nil {
		fatalf("mkstdembed: rename: %v", err)
	}

	st, _ := os.Stat(file)
	xprintf("stdembed: %d files, %d bytes -> %s\n", count, st.Size(), file)
}

// embedSkipFilter is the WalkDir filter used for the src/ tree:
// drop cmd/, testdata/, _test.go, and dotfiles. Returns (skipDir,
// skipFile) — true means stop descending / don't include this file.
func embedSkipFilter(rel string, d fs.DirEntry) (skipDir, skipFile bool) {
	if rel == "cmd" || strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) {
		return true, false
	}
	base := filepath.Base(rel)
	if base == "testdata" {
		return true, false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false, true
	}
	if strings.HasPrefix(base, ".") {
		return d.IsDir(), !d.IsDir()
	}
	return false, false
}

// embedTree walks root into the tar writer, prefixing every entry's
// archive name with prefix. filter (optional) decides what to skip.
// Returns the number of regular files written.
func embedTree(tw *tar.Writer, root, prefix string, filter func(rel string, d fs.DirEntry) (skipDir, skipFile bool)) int {
	if _, err := os.Stat(root); err != nil {
		fatalf("mkstdembed: %s missing: %v", root, err)
	}
	count := 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if filter != nil {
			skipDir, skipFile := filter(rel, d)
			if skipDir {
				return fs.SkipDir
			}
			if skipFile {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = prefix + filepath.ToSlash(rel)
		hdr.ModTime = unixEpoch
		hdr.AccessTime = unixEpoch
		hdr.ChangeTime = unixEpoch

		if d.IsDir() {
			hdr.Name += "/"
			hdr.Typeflag = tar.TypeDir
			return tw.WriteHeader(hdr)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = target
			return tw.WriteHeader(hdr)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		hdr.Typeflag = tar.TypeReg
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, src); err != nil {
			src.Close()
			return err
		}
		src.Close()
		count++
		return nil
	})
	if walkErr != nil {
		fatalf("mkstdembed: walk %s: %v", root, walkErr)
	}
	return count
}

// embedFile adds a single file at path to the tar writer under the
// given archive name. Skips silently when the file is missing — some
// optional GOROOT files (like go.env on very old trees) may not be
// present.
func embedFile(tw *tar.Writer, path, name string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		fatalf("mkstdembed: header %s: %v", path, err)
	}
	hdr.Name = name
	hdr.ModTime = unixEpoch
	hdr.AccessTime = unixEpoch
	hdr.ChangeTime = unixEpoch
	hdr.Typeflag = tar.TypeReg
	if err := tw.WriteHeader(hdr); err != nil {
		fatalf("mkstdembed: write header %s: %v", path, err)
	}
	f, err := os.Open(path)
	if err != nil {
		fatalf("mkstdembed: open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(tw, f); err != nil {
		fatalf("mkstdembed: copy %s: %v", path, err)
	}
	return 1
}

// unixEpoch is the deterministic mtime stamped into every archive
// entry. Real mtimes would invalidate the embed hash on every
// build, defeating the cached-extraction shortcut in stdembed.
var unixEpoch = time.Unix(0, 0).UTC()
