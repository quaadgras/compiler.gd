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

// TestLibc compares the output of C's library, which internal/c23
// implements, with gcc's and its C library's.
func TestLibc(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/libc.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	bin := filepath.Join(t.TempDir(), "libc")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, src, "-lm").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir() // the program writes files
	want, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestInterop checks what Go and C may do with each other's pointers: an
// ordinary cgo package (testdata/interop), which go build compiles with
// cmd/cc because cgo is off, natively and on wasm (with node).
func TestInterop(t *testing.T) {
	testenv.MustHaveGoRun(t)
	for _, target := range []struct {
		name string
		env  []string
	}{
		{"native", nil},
		{"wasm-linear", []string{"GOOS=js", "GOARCH=wasm", "GOWASM=linear"}},
		{"wasm-split", []string{"GOOS=js", "GOARCH=wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			wasm := target.env != nil
			if wasm {
				if _, err := exec.LookPath("node"); err != nil {
					t.Skip("no node")
				}
			}
			testInterop(t, target.env, target.name == "wasm-split")
		})
	}
}

func testInterop(t *testing.T, env []string, split bool) {
	dir := t.TempDir()
	files := map[string]string{"go.mod": "module cctest\n\ngo 1.27\n"}
	ents, err := os.ReadDir("testdata/interop")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("testdata/interop", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "NODE_OPTIONS=")
	if env != nil {
		cmd.Env = append(cmd.Env, env...)
		cmd.Env = append(cmd.Env, "PATH="+filepath.Join(testenv.GOROOT(t), "lib", "wasm")+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, b)
	}
	out := string(b)
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
		"C keeps a pointer in untyped bytes           ok",
		"variant's list, after a collection: 10 type 2 bytes 8\n",
		"C reads the double as a pointer              trapped: c23: access of 4 bytes out of bounds of {C.struct_node, size 16",
		"objects in a pool, after a collection: 9\n",
		"Go sees the pool's objects only as bytes     trapped: runtime error: invalid memory address",
		"C puns a pointer word of a stack local       trapped: c23: scalar store over a pointer word",
		"Go follows it                                trapped: runtime error: invalid memory address",
		"42 C uses it as the int it is                   ok",
		"C writes a plain field of a Go value         ok",
		"C writes the length of a Go slice            trapped: c23: store into a word of a Go value with invariants (main.goThing)",
		"C writes past a Go value                     trapped: c23: access of 8 bytes out of bounds of {main.goThing, size 32, off 32}",
		"Go's value after: 7 2\n",
	} {
		if split {
			// No panic to recover from (deref_split.go).
			if strings.Contains(want, "trapped: runtime error") {
				want = want[:strings.Index(want, "trapped:")] + "not tried"
			}
		}
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestInternalC23 checks that only Go that cc generated may import
// internal/c23.
func TestInternalC23(t *testing.T) {
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/basic.c", true)
	if out := goRun(t, dir); !strings.Contains(out, "fib(20) = 6765") {
		t.Fatalf("generated code did not run:\n%s", out)
	}
	user := "package main\n\nimport \"internal/c23\"\n\nvar _ = c23.Malloc\n"
	if err := os.WriteFile(filepath.Join(dir, "user.go"), []byte(user), 0o666); err != nil {
		t.Fatal(err)
	}
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "use of internal package internal/c23 not allowed") {
		t.Fatalf("user code imported internal/c23: err %v\n%s", err, out)
	}
}

// TestBuildTag checks which import "C" packages go build compiles with
// cmd/cc when cgo is off: those it can, as if cgo were on (cgo files in,
// !cgo files out); the others as before, without their cgo files.
func TestBuildTag(t *testing.T) {
	testenv.MustHaveGoRun(t)
	for _, tc := range []struct {
		name, preamble, want string
	}{
		{"cc", `#include <stdio.h>
#include "add.h"`, "cgo: 5\n"},
		{"ldflags", `#cgo LDFLAGS: -lm
#include "add.h"`, "no cgo\n"},
		{"system header", `#include <sys/socket.h>
#include "add.h"`, "no cgo\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var pre strings.Builder
			for _, l := range strings.Split(tc.preamble, "\n") {
				pre.WriteString("// " + l + "\n")
			}
			files := map[string]string{
				"go.mod":   "module m\n\ngo 1.27\n",
				"add.h":    "int add(int a, int b);\n",
				"add.c":    "#include \"add.h\"\nint add(int a, int b) { return a + b; }\n",
				"cgo.go":   "package main\n\n" + pre.String() + "import \"C\"\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"cgo:\", C.add(2, 3)) }\n",
				"nocgo.go": "//go:build !cgo\n\npackage main\n\nfunc main() { println(\"no cgo\") }\n",
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o666); err != nil {
					t.Fatal(err)
				}
			}
			cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}
