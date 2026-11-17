package main

import (
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
	toks, err := lex(in, string(src))
	if err != nil {
		t.Fatal(err)
	}
	u, err := parse(in, toks)
	if err != nil {
		t.Fatal(err)
	}
	gosrc, err := generate(in, u, "main", withMain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, gosrc, 0o666); err != nil {
		t.Fatal(err)
	}
}

func goRun(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("go", "run", "./"+dir).CombinedOutput()
	if err != nil {
		t.Fatalf("go run %s: %v\n%s", dir, err, out)
	}
	return string(out)
}

// TestBasic compares the output of ordinary C with gcc's.
func TestBasic(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	translate(t, "testdata/basic.c", "demo/basic/basic_c.go", true)
	bin := filepath.Join(t.TempDir(), "basic")
	if out, err := exec.Command(gcc, "-w", "-o", bin, "testdata/basic.c").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, "demo/basic"); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestInterop checks what Go and C may do with each other's pointers.
func TestInterop(t *testing.T) {
	translate(t, "testdata/interop.c", "demo/interop/interop_c.go", false)
	out := goRun(t, "demo/interop")
	for _, want := range []string{
		"C's list in Go: 1 2 3 4 5\n",
		"Go's list summed by C: 60\n",
		"after 3 collections: Go 5000050000 C 5000050000\n",
		"30 C reads arr[2] of Go's slice",
		"C reads arr[4] of Go's slice                 trapped: ccap: access of 4 bytes out of bounds",
		"Go dereferences it                           trapped: runtime error: invalid memory address",
		"30 C brings it back in bounds (arr[2])          ok",
		"C dereferences it out of bounds              trapped",
		"60 C one-based indexing                         ok",
		"C writes an integer over a pointer word      trapped: ccap: scalar store over a pointer word",
		"C dereferences an integer                    trapped: ccap: access through a pointer with no capability",
		"C hides a pointer in bytes                   trapped: ccap: pointer store into a word the collector does not scan",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
