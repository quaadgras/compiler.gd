// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"cmd/cc/host"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// thinNotYet are the programs the 1:1 translation does not translate
// yet, or not rightly, and why.
var thinNotYet = map[string]string{
	"hiding.cc":  "bit-fields",
	"library.cc": "_Atomic",
	"virtual.cc": "delete[] takes the array's length from its allocation, which is its size class",
}

// TestThin compares the output of programs translated 1:1 (cc -thin:
// C's objects as Go variables, its pointers as Go's) with gcc's and
// g++'s: the C programs of testdata/thin and the C++ programs of
// testdata/cxx.
func TestThin(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	gxx, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("no g++")
	}
	testenv.MustHaveGoRun(t)
	c, _ := filepath.Glob("testdata/thin/*.c")
	cxx, _ := filepath.Glob("testdata/cxx/*.cc")
	files := append(c, cxx...)
	if len(c) == 0 || len(cxx) == 0 {
		t.Fatal("no tests")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			if why := thinNotYet[filepath.Base(f)]; why != "" {
				t.Skip("not in the 1:1 translation yet: " + why)
			}
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			if st, err := host.Run([]string{"-thin", "-main", "-o", filepath.Join(dir, "c.go"), f}, &stderr, &stderr); err != nil || st != 0 {
				t.Fatalf("cc -thin: status %d %v\n%s", st, err, &stderr)
			}
			bin := filepath.Join(t.TempDir(), "want")
			cmd := exec.Command(gcc, "-w", "-o", bin, f, "-lm")
			if strings.HasSuffix(f, ".cc") {
				cmd = exec.Command(gxx, "-std=gnu++17", "-w", "-o", bin, f)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
			}
			want, err := exec.Command(bin).Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := goRun(t, dir); got != string(want) {
				t.Errorf("output differs from the C compiler's\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
