// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"bytes"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPtr(t *testing.T) {
	// This test requires rebuilding packages with -d=checkptr=1,
	// so it's somewhat slow.
	if testing.Short() {
		t.Skip("skipping test in -short mode")
	}

	t.Parallel()
	testenv.MustHaveGoRun(t)

	exe, err := buildTestProg(t, "testprog", "-gcflags=all=-d=checkptr=1")
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		cmd  string
		want string
	}{
		{"CheckPtrAlignmentPtr", "fatal error: checkptr: misaligned pointer conversion\n"},
		{"CheckPtrAlignmentNoPtr", ""},
		{"CheckPtrAlignmentNilPtr", ""},
		{"CheckPtrArithmetic", "fatal error: checkptr: pointer arithmetic result points to invalid allocation\n"},
		{"CheckPtrExposedOK", ""},
		{"CheckPtrNotExposed", "fatal error: checkptr: pointer arithmetic result points to invalid allocation\n"},
		// gd: the integer is in an object whose address was taken as an
		// integer and kept, which makes it a pointer to that object.
		{"CheckPtrArithmetic2", ""},
		{"CheckPtrArithmeticUnsafeAdd", "fatal error: checkptr: pointer arithmetic result points to invalid allocation\n"},
		// gd: a pointer to an object of the heap that is moved to
		// where nothing of the program's is.
		{"CheckPtrArithmeticLeavesHeap", "fatal error: checkptr: pointer arithmetic result is outside the object it was computed from\n"},
		{"CheckPtrSize", "fatal error: checkptr: converted pointer straddles multiple allocations\n"},
		// gd: bytes are no smaller than what they are over.
		{"CheckPtrBytesLonger", "fatal error: checkptr: converted pointer straddles multiple allocations\n"},
		{"CheckPtrBytesSliced", ""},
		{"CheckPtrBytesSlicedLonger", "fatal error: checkptr: converted pointer straddles multiple allocations\n"},
		{"CheckPtrSmall", "fatal error: checkptr: pointer arithmetic computed bad pointer value\n"},
		{"CheckPtrSliceOK", ""},
		{"CheckPtrSliceFail", "fatal error: checkptr: unsafe.Slice result straddles multiple allocations\n"},
		{"CheckPtrStringOK", ""},
		{"CheckPtrStringFail", "fatal error: checkptr: unsafe.String result straddles multiple allocations\n"},
		{"CheckPtrPointersNoscan", "fatal error: checkptr: converted pointer type has a pointer where the memory has an integer"},
		{"CheckPtrPointersScalar", "fatal error: checkptr: converted pointer type has an integer where the memory has a pointer"},
		{"CheckPtrPointersOK", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got, err := testenv.CleanCmdEnv(exec.Command(exe, tc.cmd)).CombinedOutput()
			if err != nil {
				t.Log(err)
			}
			if tc.want == "" {
				if len(got) > 0 {
					t.Errorf("output:\n%s\nwant no output", got)
				}
				return
			}
			if !strings.HasPrefix(string(got), tc.want) {
				t.Errorf("output:\n%s\n\nwant output starting with: %s", got, tc.want)
			}
		})
	}
}

// TestCheckPtrViews runs the views of strings, slices and interfaces of
// testprog's checkptr_whole.go, with the checks as they are by default.
func TestCheckPtrViews(t *testing.T) {
	t.Parallel()
	testenv.MustHaveGoRun(t)

	exe, err := buildTestProg(t, "testprog")
	if err != nil {
		t.Fatal(err)
	}
	list, err := testenv.CleanCmdEnv(exec.Command(exe, "CheckPtrViewList")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, list)
	}
	const fatal = "fatal error: checkptr: converted pointer is a partial view of a string, a slice or an interface\n"
	for _, name := range strings.Fields(string(list)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := testenv.CleanCmdEnv(exec.Command(exe, name)).CombinedOutput()
			if strings.HasPrefix(name, "CheckPtrPartial") {
				if !strings.HasPrefix(string(got), fatal) {
					t.Errorf("output:\n%s\n\nwant output starting with: %s", got, fatal)
				}
				return
			}
			if err != nil || len(got) > 0 {
				t.Errorf("%v, output:\n%s\nwant no output", err, got)
			}
		})
	}
}

// TestCheckPtrCompat runs the conversions of testprog's
// checkptr_compat.go to types that are compatible with the memory, and
// that are not, with the checks as they are by default.
func TestCheckPtrCompat(t *testing.T) {
	t.Parallel()
	testenv.MustHaveGoRun(t)

	exe, err := buildTestProg(t, "testprog")
	if err != nil {
		t.Fatal(err)
	}
	list, err := testenv.CleanCmdEnv(exec.Command(exe, "CheckPtrCompatList")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, list)
	}
	const fatal = "fatal error: checkptr: converted pointer type has "
	for _, name := range strings.Fields(string(list)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := testenv.CleanCmdEnv(exec.Command(exe, name)).CombinedOutput()
			if strings.HasPrefix(name, "CheckPtrIncompat") {
				if !strings.HasPrefix(string(got), fatal) {
					t.Errorf("output:\n%s\n\nwant output starting with: %s", got, fatal)
				}
				return
			}
			if err != nil || len(got) > 0 {
				t.Errorf("%v, output:\n%s\nwant no output", err, got)
			}
		})
	}
}

// TestCheckPtrVars is the checks of the pointers to the variables of the
// stack and of the program's data, each of which is an allocation with a
// type (testprog/checkptr_vars.go).
func TestCheckPtrVars(t *testing.T) {
	t.Parallel()
	testenv.MustHaveGoRun(t)

	exe, err := buildTestProg(t, "testprog")
	if err != nil {
		t.Fatal(err)
	}
	list, err := testenv.CleanCmdEnv(exec.Command(exe, "CheckPtrVarList")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, list)
	}
	const fatal = "fatal error: checkptr: "
	for _, name := range strings.Fields(string(list)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := testenv.CleanCmdEnv(exec.Command(exe, name)).CombinedOutput()
			if strings.HasPrefix(name, "CheckPtrVarBad") {
				if !strings.HasPrefix(string(got), fatal) {
					t.Errorf("output:\n%s\n\nwant output starting with: %s", got, fatal)
				}
				return
			}
			if err != nil || len(got) > 0 {
				t.Errorf("%v, output:\n%s\nwant no output", err, got)
			}
		})
	}
}

func TestCheckPtr2(t *testing.T) {
	// This test requires rebuilding packages with -d=checkptr=2,
	// so it's somewhat slow.
	if testing.Short() {
		t.Skip("skipping test in -short mode")
	}

	t.Parallel()
	testenv.MustHaveGoRun(t)

	exe, err := buildTestProg(t, "testprog", "-gcflags=all=-d=checkptr=2")
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		cmd  string
		want string
	}{
		{"CheckPtrAlignmentNested", "fatal error: checkptr: converted pointer straddles multiple allocations\n"},
	}

	for _, tc := range testCases {
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got, err := testenv.CleanCmdEnv(exec.Command(exe, tc.cmd)).CombinedOutput()
			if err != nil {
				t.Log(err)
			}
			if tc.want == "" {
				if len(got) > 0 {
					t.Errorf("output:\n%s\nwant no output", got)
				}
				return
			}
			if !strings.HasPrefix(string(got), tc.want) {
				t.Errorf("output:\n%s\n\nwant output starting with: %s", got, tc.want)
			}
		})
	}
}

// TestUnsafeGo runs the programs of testdata/testunsafe, each of which
// uses package unsafe to reach memory that it was not given: past an
// object, through an integer, through a pointer of another type. A
// program prints UNSAFE if what it tried went through, and OK if what it
// tried did nothing; it is also right for it to be stopped, or for the
// compiler to refuse it. status.txt lists the ones that still print
// UNSAFE (doc/gd/unsafe.md). cmd/cc's TestUnsafe is the same for C.
func TestUnsafeGo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in -short mode")
	}
	testenv.MustHaveGoRun(t)
	status, err := os.ReadFile("testdata/testunsafe/status.txt")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, name := range strings.Fields(string(status)) {
		known[name] = true
	}
	files, err := filepath.Glob("testdata/testunsafe/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no programs: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".go")
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module unsafetest\n\ngo 1.27\n"), 0o666); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), src, 0o666); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "prog")
			build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", exe, ".")
			build.Dir = dir
			if out, err := build.CombinedOutput(); err != nil {
				if known[name] {
					t.Errorf("the program is in status.txt and the compiler refuses it now: take it out\n%s", out)
				}
				return
			}
			out, err := testenv.CleanCmdEnv(exec.Command(exe)).CombinedOutput()
			unsafe := bytes.Contains(out, []byte("UNSAFE: "))
			switch {
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
