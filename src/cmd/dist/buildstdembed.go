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
	srcDir := pathf("%s/src", goroot)
	if _, err := os.Stat(srcDir); err != nil {
		fatalf("mkstdembed: %s missing: %v", srcDir, err)
	}

	tmp := file + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fatalf("mkstdembed: create %s: %v", tmp, err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	count := 0
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip cmd/ entirely.
		if rel == "cmd" || strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) {
			return fs.SkipDir
		}
		base := filepath.Base(rel)
		if base == "testdata" {
			return fs.SkipDir
		}
		if strings.HasSuffix(base, "_test.go") {
			return nil
		}
		if strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		// Prefix with "src/" so the materialised tree matches the
		// $GDROOT/src/<pkg> layout the rest of the toolchain expects.
		hdr.Name = "src/" + filepath.ToSlash(rel)
		// Use a fixed mtime so the archive content is deterministic
		// from one make.bash run to the next when the source hasn't
		// changed. Otherwise the SHA-256 stdembed.Hash() drifts on
		// every rebuild and $GDPATH/std re-extracts unnecessarily.
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
		fatalf("mkstdembed: walk: %v", walkErr)
	}

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

// unixEpoch is the deterministic mtime stamped into every archive
// entry. Real mtimes would invalidate the embed hash on every
// build, defeating the cached-extraction shortcut in stdembed.
var unixEpoch = time.Unix(0, 0).UTC()
