// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// gen builds the stdembed archive: a gzip-tar of $GDROOT/src
// excluding src/cmd, written to ../stdlib.tar.gz so cmd/internal/
// stdembed picks it up via //go:embed.
//
// Run from the cmd/internal/stdembed/gen directory:
//
//	go run gen.go --goroot=/path/to/goroot
//
// Or from cmd/dist's bootstrap path, which arranges the cwd and
// env. Skips _test.go, testdata/, and dot-files; keeps
// architecture-tagged sources (build tags filter at compile time,
// not extract time).
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	goroot = flag.String("goroot", "", "GOROOT to read src/ from (defaults to runtime.GOROOT)")
	out    = flag.String("out", "../stdlib.tar.gz", "output archive path (relative to gen.go's dir)")
)

func main() {
	flag.Parse()
	if *goroot == "" {
		// Resolve relative to gen.go: ../../../../../ from
		// cmd/internal/stdembed/gen.
		exe, _ := os.Executable()
		_ = exe
		// Default to current GOROOT via runtime if running with go run
		if env := os.Getenv("GOROOT"); env != "" {
			*goroot = env
		} else {
			fatalf("--goroot required (or set GOROOT)")
		}
	}

	srcDir := filepath.Join(*goroot, "src")
	if _, err := os.Stat(srcDir); err != nil {
		fatalf("src not found at %s: %v", srcDir, err)
	}

	tmp := *out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fatalf("create %s: %v", tmp, err)
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
		// Skip _test.go and testdata.
		base := filepath.Base(rel)
		if base == "testdata" {
			return fs.SkipDir
		}
		if strings.HasSuffix(base, "_test.go") {
			return nil
		}
		// Skip dot files (.git, .gitignore, etc.).
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
		// Use forward slashes; tar requires it and the extractor
		// re-resolves with filepath.FromSlash.
		hdr.Name = filepath.ToSlash(rel)
		hdr.ModTime = info.ModTime()
		hdr.AccessTime = hdr.ModTime
		hdr.ChangeTime = hdr.ModTime

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
			return nil // skip non-regular files
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
		fatalf("walk: %v", walkErr)
	}

	if err := tw.Close(); err != nil {
		fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		fatalf("close gzip: %v", err)
	}
	if err := f.Close(); err != nil {
		fatalf("close: %v", err)
	}
	if err := os.Rename(tmp, *out); err != nil {
		fatalf("rename: %v", err)
	}

	st, _ := os.Stat(*out)
	fmt.Printf("stdembed: wrote %s (%d files, %d bytes)\n", *out, count, st.Size())
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "stdembed/gen: "+format+"\n", args...)
	os.Exit(1)
}
