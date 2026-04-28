// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

// Package stdembed embeds the Go standard library source tree
// (everything under $GDROOT/src except cmd/) as a single gzip-tar
// archive, and materialises it to $GDPATH/std on first use. cmd/go
// falls through to the materialised tree when the bin/go-detected
// GDROOT has no usable src/ directory (typical when bin/go is
// distributed standalone, without the source tree).
//
// The materialisation is idempotent and content-addressed:
// $GDPATH/std/.embed-hash holds the SHA-256 of the embedded archive
// from the previous successful extraction. On startup the file is
// compared to the current archive's hash; matches skip extraction,
// mismatches wipe and re-extract. The hash drifts only when bin/go
// itself is rebuilt from a different stdlib source, which is the
// only correct trigger to invalidate.
package stdembed

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// archiveData is the embedded archive. Empty when bin/go was built
// without baking the stdlib in (e.g., a development build out of a
// real source tree, where IsEmpty()'s caller should fall through to
// the on-disk GDROOT instead).
//
//go:embed stdlib.tar.gz
var archiveData []byte

// IsEmpty reports whether stdembed has no archive baked in.
func IsEmpty() bool {
	return len(archiveData) == 0
}

// Hash returns the SHA-256 hex of the embedded archive. The empty
// archive's hash is stable, so callers don't need to special-case
// IsEmpty() before comparing.
func Hash() string {
	sum := sha256.Sum256(archiveData)
	return hex.EncodeToString(sum[:])
}

// Materialize extracts the embedded archive to dst (typically
// $GDPATH/std). It writes a .embed-hash sentinel inside dst on
// success; subsequent calls compare the embedded archive's hash to
// the sentinel and skip extraction when they match. A mismatch (or
// missing sentinel) causes dst to be wiped and re-extracted.
//
// Returns nil and does nothing when IsEmpty() — the binary has no
// archive to materialise.
func Materialize(dst string) error {
	if IsEmpty() {
		return nil
	}

	hashFile := filepath.Join(dst, ".embed-hash")
	want := Hash()
	if got, err := os.ReadFile(hashFile); err == nil && string(got) == want {
		return nil // already up to date
	}

	// Wipe any stale extraction. RemoveAll on a non-existent path
	// is a no-op, so the first-run case lands here too.
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("stdembed: clean %s: %w", dst, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("stdembed: mkdir %s: %w", dst, err)
	}

	if err := extract(bytes.NewReader(archiveData), dst); err != nil {
		os.RemoveAll(dst) // don't leave a half-extracted tree
		return fmt.Errorf("stdembed: extract: %w", err)
	}
	if err := os.WriteFile(hashFile, []byte(want), 0o644); err != nil {
		return fmt.Errorf("stdembed: write hash: %w", err)
	}
	return nil
}

func extract(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject paths that escape dst (defence-in-depth; the
		// archive is built by us, but a malicious tar would still
		// be contained).
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || (len(clean) >= 2 && clean[:2] == "..") {
			return fmt.Errorf("stdembed: archive entry escapes root: %q", hdr.Name)
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// Skip other types (devices, FIFOs, etc.) — stdlib
			// has none.
		}
	}
}
