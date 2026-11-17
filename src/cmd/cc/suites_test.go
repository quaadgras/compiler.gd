// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"cmd/cc/internal/ccmain"
)

// Test suites cmd/cc does not vendor (their licenses are not MIT): given
// a checkout, they run like slimcc's, against a list of the tests that
// pass in testdata.
var (
	ctestsuite = flag.String("ctestsuite", "", "a checkout of https://github.com/c-testsuite/c-testsuite, to run its single-exec tests")
	torture    = flag.String("torture", "", "GCC's gcc/testsuite/gcc.c-torture/execute, to run its tests")
)

// TestCTestsuite runs c-testsuite's single-exec tests, each a program
// whose output must be NAME.c.expected's.
func TestCTestsuite(t *testing.T) {
	if *ctestsuite == "" {
		t.Skip("no -ctestsuite")
	}
	dir := filepath.Join(*ctestsuite, "tests", "single-exec")
	runSuite(t, dir, "testdata/ctestsuite.status", func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name+".c.expected"))
		return string(b), err
	})
}

// TestTorture runs GCC's execute torture tests, each a program that
// aborts if it fails. Only those gcc itself passes (with -std=gnu17)
// count, so gcc is needed.
func TestTorture(t *testing.T) {
	if *torture == "" {
		t.Skip("no -torture")
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	tmp := t.TempDir()
	runSuite(t, *torture, "testdata/torture.status", func(name string) (string, error) {
		bin := filepath.Join(tmp, name)
		if out, err := exec.Command(gcc, "-w", "-std=gnu17", "-O0", "-o", bin, filepath.Join(*torture, name+".c"), "-lm").CombinedOutput(); err != nil {
			return "", fmt.Errorf("skip: gcc: %s", out)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, bin).Run(); err != nil {
			return "", fmt.Errorf("skip: gcc's fails: %v", err)
		}
		return "", nil
	})
}

// runSuite translates, builds and runs every NAME.c in dir: one passes
// if it exits 0 with the output expect says (if expect gives one). An
// error from expect starting "skip:" skips the test.
func runSuite(t *testing.T, dir, statusFile string, expect func(name string) (string, error)) {
	testenv.MustHaveGoBuild(t)
	want := map[string]bool{}
	if b, err := os.ReadFile(statusFile); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				want[l] = true
			}
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil {
		t.Fatal(err)
	}
	mod := t.TempDir()
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module suite\n\ngo 1.27\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var passed []string
	ran := 0
	t.Run("group", func(t *testing.T) {
		for _, file := range files {
			name := strings.TrimSuffix(filepath.Base(file), ".c")
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				exp, err := expect(name)
				if err != nil {
					if strings.HasPrefix(err.Error(), "skip:") {
						t.Skip(err)
					}
					t.Fatal(err)
				}
				err = runOne(t, mod, name, file, exp)
				mu.Lock()
				defer mu.Unlock()
				ran++
				if err == nil {
					passed = append(passed, name)
					if !want[name] {
						t.Logf("now passes")
					}
					return
				}
				t.Log(err)
				if want[name] && !*update {
					t.Errorf("%v", err)
				}
			})
		}
	})
	slices.Sort(passed)
	t.Logf("%d of %d pass", len(passed), ran)
	if *update {
		var b bytes.Buffer
		fmt.Fprintf(&b, "# The tests of %s that cmd/cc passes (-update rewrites this).\n", filepath.Base(statusFile))
		for _, name := range passed {
			b.WriteString(name + "\n")
		}
		if err := os.WriteFile(statusFile, b.Bytes(), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

func runOne(t *testing.T, mod, name, file, expected string) error {
	src, err := ccmain.Program([]string{file}, "main", []string{filepath.Dir(file)})
	if err != nil {
		return fmt.Errorf("cc: %v", err)
	}
	pkg := filepath.Join(mod, name)
	if err := os.MkdirAll(pkg, 0o777); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pkg, "c.go"), src, 0o666); err != nil {
		return err
	}
	bin := filepath.Join(pkg, "t.exe")
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", bin, ".")
	cmd.Dir = pkg
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	run := exec.CommandContext(ctx, bin)
	run.Dir = pkg
	out, err := run.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run: %v\n%s", err, out)
	}
	if expected != "" && string(out) != expected {
		return fmt.Errorf("output differs:\n%s", out)
	}
	return nil
}
