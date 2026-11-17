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

var update = flag.Bool("update", false, "rewrite testdata/slimcc/status.txt with the tests that pass")

// TestSlimcc runs slimcc's tests (testdata/slimcc, MIT; see README), C23
// and the GNU extensions slimcc supports, each linked with its
// host/common.c. testdata/slimcc/status.txt lists those that pass: one
// that fails is an error, one that newly passes is logged, and -update
// rewrites the list.
func TestSlimcc(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("slow")
	}
	const dir = "testdata/slimcc"
	want := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(dir, "status.txt")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				want[l] = true
			}
		}
	}
	tests, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil {
		t.Fatal(err)
	}
	mod := t.TempDir()
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module slimcc\n\ngo 1.27\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var passed []string
	why := map[string]string{}
	t.Run("group", func(t *testing.T) {
		for _, test := range tests {
			name := strings.TrimSuffix(filepath.Base(test), ".c")
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				err := runSlimcc(t, mod, name, test, filepath.Join(dir, "host", "common.c"))
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					passed = append(passed, name)
					if !want[name] {
						t.Logf("now passes")
					}
					return
				}
				why[name] = err.Error()
				if want[name] && !*update {
					t.Errorf("%v", err)
				}
			})
		}
	})
	slices.Sort(passed)
	t.Logf("%d of %d pass", len(passed), len(tests))
	if testing.Verbose() {
		var fails []string
		for name, msg := range why {
			if i := strings.IndexByte(msg, '\n'); i >= 0 {
				msg = msg[:i]
			}
			fails = append(fails, name+": "+msg)
		}
		slices.Sort(fails)
		t.Logf("failures:\n%s", strings.Join(fails, "\n"))
	}
	if *update {
		var b bytes.Buffer
		b.WriteString("# The slimcc tests that cmd/cc passes (go test cmd/cc -run Slimcc -update).\n")
		for _, name := range passed {
			b.WriteString(name + "\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "status.txt"), b.Bytes(), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

// runSlimcc translates, builds and runs one test.
func runSlimcc(t *testing.T, mod, name string, files ...string) error {
	src, err := ccmain.Program(files, "main", []string{filepath.Dir(files[0])})
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
	bin := filepath.Join(pkg, name+".exe")
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", bin, ".")
	cmd.Dir = pkg
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin).CombinedOutput()
	if err != nil {
		return fmt.Errorf("run: %v\n%s", err, out)
	}
	return nil
}
