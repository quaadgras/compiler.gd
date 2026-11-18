// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHeaderVersions checks that each C23 header cmd/cc provides defines
// its __STDC_VERSION_*_H__ as 202311L. (gcc is no reference here: with
// musl, its limits.h and others are the C library's, which lack them.)
func TestHeaderVersions(t *testing.T) {
	for _, h := range []string{
		"assert", "complex", "fenv", "float", "inttypes", "limits", "math",
		"setjmp", "stdarg", "stdatomic", "stdbit", "stdckdint", "stddef",
		"stdint", "stdio", "stdlib", "string", "tgmath", "time", "uchar", "wchar",
	} {
		name := "__STDC_VERSION_" + strings.ToUpper(h) + "_H__"
		toks, err := preprocess([]source{{"v.c", "#include <" + h + ".h>\n" + name + "\n"}}, nil)
		if err != nil {
			t.Errorf("%s.h: %v", h, err)
			continue
		}
		last := ""
		for _, tk := range toks {
			if tk.kind != tkEOF {
				last = tk.text
			}
		}
		if last != "202311L" {
			t.Errorf("<%s.h>: %s is %q, want 202311L", h, name, last)
		}
	}
}

// TestPreprocessGCC compares the tokens the preprocessor makes of the
// macro tests of testdata/slimcc with those of gcc -E, in the test's own
// file (the headers differ).
func TestPreprocessGCC(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	files, _ := filepath.Glob("../../testdata/slimcc/macro*.c")
	files = append(files, "../../testdata/slimcc/line.c", "../../testdata/slimcc/embed.c")
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			out, err := exec.Command(gcc, "-E", "-std=gnu2x", "-I", filepath.Dir(file), file).Output()
			if err != nil {
				t.Skipf("gcc: %v", err)
			}
			// gcc's tokens in the main file, by its line markers.
			var want []string
			in := true
			for _, l := range strings.Split(string(out), "\n") {
				if f := strings.Fields(l); len(f) > 1 && f[0] == "#" && f[1] == "embed" || strings.HasPrefix(l, "#embed") {
					// This gcc leaves #embed to its compiler proper
					// (gcc 15 writes the bytes as base64 there; gcc 13
					// does not know it): nothing to compare with.
					t.Skipf("gcc keeps #embed as a directive: %.60s", l)
				}
				if strings.HasPrefix(l, "# ") {
					f := strings.Fields(l)
					in = len(f) > 2 && f[2] == `"`+file+`"`
					continue
				}
				if !in {
					continue
				}
				ts, err := lexRaw(file, l, 1)
				if err != nil {
					t.Fatal(err)
				}
				for _, tk := range ts {
					want = append(want, tk.text)
				}
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			toks, err := preprocess([]source{{file, string(src)}}, []string{filepath.Dir(file)})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, tk := range toks {
				if tk.kind != tkEOF && tk.file == file {
					got = append(got, tk.text)
				}
			}
			for i := 0; i < len(got) || i < len(want); i++ {
				g, w := "<end>", "<end>"
				if i < len(got) {
					g = got[i]
				}
				if i < len(want) {
					w = want[i]
				}
				// __DATE__, __TIME__ and __TIMESTAMP__ are fixed.
				if g != w && !(g == `"Jan  1 1970"` || g == `"00:00:00"` || g == `"Thu Jan  1 00:00:00 1970"`) {
					lo := max(i-10, 0)
					t.Fatalf("token %d: got %q, want %q\ngot:  %s\nwant: %s", i, g, w,
						strings.Join(got[lo:min(i+10, len(got))], " "), strings.Join(want[lo:min(i+10, len(want))], " "))
				}
			}
		})
	}
}
