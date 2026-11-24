// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"internal/testenv"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCxx compares the output of the C++ programs of testdata/cxx with
// g++'s (doc/gd/cxx.md).
func TestCxx(t *testing.T) {
	gxx, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("no g++")
	}
	testenv.MustHaveGoRun(t)
	files, err := filepath.Glob("testdata/cxx/*.cc")
	if err != nil || len(files) == 0 {
		t.Fatalf("no tests: %v", err)
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".cc"), func(t *testing.T) {
			t.Parallel()
			dir := module(t, f, true)
			bin := filepath.Join(t.TempDir(), "want")
			if out, err := exec.Command(gxx, "-std=gnu++17", "-w", "-o", bin, f).CombinedOutput(); err != nil {
				t.Fatalf("g++: %v\n%s", err, out)
			}
			want, err := exec.Command(bin).Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := goRun(t, dir); got != string(want) {
				t.Errorf("output differs from g++'s\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
