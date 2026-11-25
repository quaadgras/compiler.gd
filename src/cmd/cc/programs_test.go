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
	"testing"
)

// TestPrograms compares the output of the C programs of
// testdata/programs, translated, with what they print compiled by gcc.
// (TestCxx does the same for the C++ programs of testdata/cxx.)
func TestPrograms(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	files, _ := filepath.Glob("testdata/programs/*.c")
	if len(files) == 0 {
		t.Fatal("no tests")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			if st, err := host.Run([]string{"-main", "-o", filepath.Join(dir, "c.go"), f}, &stderr, &stderr); err != nil || st != 0 {
				t.Fatalf("cc: status %d %v\n%s", st, err, &stderr)
			}
			bin := filepath.Join(t.TempDir(), "want")
			if out, err := exec.Command(gcc, "-w", "-o", bin, f, "-lm").CombinedOutput(); err != nil {
				t.Fatalf("gcc: %v\n%s", err, out)
			}
			want, err := exec.Command(bin).Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := goRun(t, dir); got != string(want) {
				t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
