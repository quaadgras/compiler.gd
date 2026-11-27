// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cmd/cc/internal/ccmain"
)

// unitsModule translates files as units (cc -units) into a new module,
// and returns its directory and what cc said.
func unitsModule(t *testing.T, dir string, files ...string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	args := append([]string{"-units", dir, "-main"}, files...)
	if code := ccmain.Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("cc -units: exit %d\n%s", code, stderr.String())
	}
	return stderr.String()
}

// TestUnits compares with g++'s the output of a program of two C++
// files that are translated and compiled separately, and checks that a
// change to one translates that one.
func TestUnits(t *testing.T) {
	gxx, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("no g++")
	}
	testenv.MustHaveGoRun(t)
	src := t.TempDir()
	for _, name := range []string{"a.cc", "b.cc", "shape.h"} {
		b, err := os.ReadFile(filepath.Join("testdata/units", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, name), b, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	a, b := filepath.Join(src, "a.cc"), filepath.Join(src, "b.cc")
	bin := filepath.Join(t.TempDir(), "want")
	if out, err := exec.Command(gxx, "-std=gnu++17", "-w", "-o", bin, a, b).CombinedOutput(); err != nil {
		t.Fatalf("g++: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if said := unitsModule(t, dir, a, b); !strings.Contains(said, "2 of 2 units") {
		t.Errorf("first translation: %s", said)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from g++'s\ngot:\n%s\nwant:\n%s", got, want)
	}
	if said := unitsModule(t, dir, a, b); !strings.Contains(said, "0 of 2 units") {
		t.Errorf("translation with nothing changed: %s", said)
	}
	f, err := os.OpenFile(b, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n// changed\n")
	f.Close()
	if said := unitsModule(t, dir, a, b); !strings.Contains(said, "1 of 2 units") {
		t.Errorf("translation with one file changed: %s", said)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("after a change, output differs from g++'s\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestCxxUnits is TestCxx with each program translated as a unit.
func TestCxxUnits(t *testing.T) {
	gxx, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("no g++")
	}
	testenv.MustHaveGoRun(t)
	if testing.Short() {
		t.Skip("each program is built a second time")
	}
	files, err := filepath.Glob("testdata/cxx/*.cc")
	if err != nil || len(files) == 0 {
		t.Fatalf("no tests: %v", err)
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".cc"), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			unitsModule(t, dir, f)
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
