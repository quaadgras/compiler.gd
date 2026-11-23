// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/testenv"
	"os/exec"
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
		{"CheckPtrSize", "fatal error: checkptr: converted pointer straddles multiple allocations\n"},
		{"CheckPtrSmall", "fatal error: checkptr: pointer arithmetic computed bad pointer value\n"},
		{"CheckPtrSliceOK", ""},
		{"CheckPtrSliceFail", "fatal error: checkptr: unsafe.Slice result straddles multiple allocations\n"},
		{"CheckPtrStringOK", ""},
		{"CheckPtrStringFail", "fatal error: checkptr: unsafe.String result straddles multiple allocations\n"},
		{"CheckPtrPointersNoscan", "fatal error: checkptr: converted pointer type has a pointer where the memory has an integer\n"},
		{"CheckPtrPointersScalar", "fatal error: checkptr: converted pointer type has a pointer where the memory has an integer\n"},
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
