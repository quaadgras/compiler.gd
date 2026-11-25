// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"cmd/cc/host"
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChecks runs C whose pointers leave their objects: the checks stop
// it where the compiled code checks them against what was looked up once
// for the pointer they are made of (cmd/compile/internal/ssa/cheaders.go),
// as where the runtime checks them, and with -d=cheaders=1.
func TestChecks(t *testing.T) {
	testenv.MustHaveGoRun(t)
	const pastEnd = "fatal error: checkptr: pointer arithmetic result is past the end of its object"
	tests := []struct {
		name, src, want string
	}{
		{"loop", `
			#include <stdio.h>
			#include <stdlib.h>
			int main(void) {
				int n = 64;
				long *a = malloc(n * sizeof(long));
				long s = 0;
				for (int i = 0; i <= n + 8; i++)
					s += *(a + i);
				printf("%ld\n", s);
				return 0;
			}`, pastEnd},
		{"step", `
			#include <stdio.h>
			#include <stdlib.h>
			int main(void) {
				char *s = malloc(32);
				long n = 0;
				for (char *p = s; n < 100; p++)
					n += *p + 1;
				printf("%ld\n", n);
				return 0;
			}`, pastEnd},
		{"phi", `
			#include <stdio.h>
			#include <stdlib.h>
			int main(int argc, char **argv) {
				long *a = malloc(16 * sizeof(long)), *b = malloc(64 * sizeof(long));
				long *p = argc > 5 ? b : a;
				long s = 0;
				for (int i = 0; i < 64; i++)
					s += p[i];
				printf("%ld\n", s);
				return 0;
			}`, pastEnd},
		{"raw", `
			#include <stdio.h>
			#include <stdlib.h>
			int main(void) {
				void *raw = malloc(8 * sizeof(long));
				long *a = raw;
				for (int i = 0; i < 12; i++)
					a[i] = i;
				printf("%ld\n", a[3]);
				return 0;
			}`, pastEnd},
		{"inside", `
			#include <stdio.h>
			#include <stdlib.h>
			int main(void) {
				int n = 64;
				void *raw = malloc(n * sizeof(long));
				long *a = raw, *end = a + n, s = 0;
				for (long *p = a; p != end; p++)
					*p = p - a;
				for (long *p = end; p != a;)
					s += *--p;
				printf("%ld\n", s);
				return 0;
			}`, "2016\n"},
	}
	for _, tc := range tests {
		for _, flags := range []string{"", "-gcflags=-d=cheaders=1"} {
			name := tc.name
			if flags != "" {
				name += "-calls"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				src := filepath.Join(t.TempDir(), "t.c")
				if err := os.WriteFile(src, []byte(tc.src), 0o666); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
					t.Fatal(err)
				}
				var stderr bytes.Buffer
				if st, err := host.Run([]string{"-main", "-o", filepath.Join(dir, "c.go"), src}, &stderr, &stderr); err != nil || st != 0 {
					t.Fatalf("cc: status %d %v\n%s", st, err, &stderr)
				}
				args := []string{"run"}
				if flags != "" {
					args = append(args, flags)
				}
				cmd := testenv.Command(t, testenv.GoToolPath(t), append(args, ".")...)
				cmd.Dir = dir
				out, _ := cmd.CombinedOutput()
				if !strings.HasPrefix(string(out), tc.want) {
					t.Errorf("got:\n%s\nwant it to begin with:\n%s", out, tc.want)
				}
			})
		}
	}
}
