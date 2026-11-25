// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"cmd/cc/host"
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnsafe runs the programs of testdata/unsafe, each of which does
// one thing that C does not define and that would let it reach memory
// it was not given: a store past an object, an integer used as a
// pointer, a pointer of one type used as another's. A program prints
// UNSAFE if what it tried went through, and OK if what it tried did
// nothing; it is also right for it to be stopped. status.txt lists the
// ones that are not stopped yet: what is left to do (doc/gd/cc.md).
func TestUnsafe(t *testing.T) {
	testenv.MustHaveGoRun(t)
	status, err := os.ReadFile("testdata/unsafe/status.txt")
	if err != nil {
		t.Fatal(err)
	}
	// A name alone is a program that prints UNSAFE; one that is
	// followed by "corrupts" goes wrong in some other way, as one that
	// wrote over the runtime's own data does.
	known := map[string]bool{}
	corrupts := map[string]bool{}
	for _, line := range strings.Split(string(status), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 1:
			known[f[0]] = true
		case len(f) == 2 && f[1] == "corrupts":
			corrupts[f[0]] = true
		}
	}
	files, err := filepath.Glob("testdata/unsafe/*.c")
	if err != nil || len(files) == 0 {
		t.Fatalf("no programs: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".c")
		src, err := filepath.Abs(file)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			if st, err := host.Run([]string{"-main", "-o", filepath.Join(dir, "c.go"), src}, &stderr, &stderr); err != nil || st != 0 {
				t.Fatalf("cc: status %d %v\n%s", st, err, &stderr)
			}
			exe := filepath.Join(dir, "prog")
			build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", exe, ".")
			build.Dir = dir
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			cmd := testenv.Command(t, exe)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			unsafe := bytes.Contains(out, []byte("UNSAFE: "))
			switch {
			case corrupts[name]:
			case unsafe && !known[name]:
				t.Errorf("what the program tried went through:\n%s", out)
			case !unsafe && known[name]:
				t.Errorf("the program is in status.txt and is stopped now (%v): take it out\n%s", err, out)
			case !unsafe && err == nil && !bytes.Contains(out, []byte("OK\n")):
				t.Errorf("the program ended with neither UNSAFE nor OK:\n%s", out)
			}
		})
	}
}
