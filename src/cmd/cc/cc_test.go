// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"cmd/cc/host"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func translate(t *testing.T, in, out string, withMain bool) {
	t.Helper()
	src, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	gosrc, err := host.Translate(in, src, "main", withMain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, gosrc, 0o666); err != nil {
		t.Fatal(err)
	}
}

// module translates in into a temporary module, beside copies of the Go
// files goFiles, and returns the module's directory.
func module(t *testing.T, in string, withMain bool, goFiles ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	translate(t, in, filepath.Join(dir, "c.go"), withMain)
	for _, f := range goFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goRun(t *testing.T, dir string) string {
	t.Helper()
	cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	return string(out)
}

// TestBasic compares the output of ordinary C with gcc's.
func TestBasic(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/basic.c", true)
	bin := filepath.Join(t.TempDir(), "basic")
	if out, err := exec.Command(gcc, "-w", "-o", bin, "testdata/basic.c").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestInterop checks what Go and C may do with each other's pointers.
func TestInterop(t *testing.T) {
	testenv.MustHaveGoRun(t)
	out := goRun(t, module(t, "testdata/interop.c", false, "testdata/interop/main.go"))
	for _, want := range []string{
		"C's list in Go: 1 2 3 4 5\n",
		"Go's list summed by C: 60\n",
		"after 3 collections: Go 5000050000 C 5000050000\n",
		"Go's stack pair summed by C: 3\n",
		"C's locals: 213\n",
		"allocations per call: sumPair 0 locals 0\n",
		"30 C reads arr[2] of Go's slice                 ok",
		"C reads arr[3] of Go's slice                 trapped: c23: access of 4 bytes out of bounds of {int32, size 12, off 12}",
		"Go dereferences it                           trapped: runtime error: invalid memory address",
		"30 C brings it back in bounds (arr[2])          ok",
		"C dereferences it out of bounds              trapped",
		"30 ...stored in Go's heap object instead        ok",
		"30 ...stored in Go's stack, which then moves    ok",
		"30 C keeps a slice's array in Go's int* field   ok",
		"...and indexes past it                       trapped: c23: access of 4 bytes out of bounds of {int32, size 12, off 12}",
		"60 C one-based indexing                         ok",
		"C writes an integer over a pointer word      trapped: c23: scalar store over a pointer word",
		"C dereferences an integer                    trapped: c23: access through a pointer with no capability",
		"C hides a pointer in bytes                   trapped: c23: pointer store into a word the collector does not scan",
		"C puns a pointer word of a stack local       trapped: c23: scalar store over a pointer word",
		"Go follows it                                trapped: runtime error: invalid memory address",
		"42 C uses it as the int it is                   ok",
		"C writes a plain field of a Go value         ok",
		"C writes the length of a Go slice            trapped: c23: store into a word of a Go value with invariants (main.goThing)",
		"C writes past a Go value                     trapped: c23: access of 8 bytes out of bounds of {main.goThing, size 32, off 32}",
		"Go's value after: 7 2\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
