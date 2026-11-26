// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"internal/testenv"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeadcode(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	t.Parallel()

	tmpdir := t.TempDir()

	tests := []struct {
		src      string
		pos, neg []string // positive and negative patterns
		flags    string   // more flags for the linker
	}{
		{"reflectcall", nil, []string{"main.T.M"}, ""},
		// gd: a live variable does bring its type descriptor live
		// if the link has the table of the data's variables, which
		// has their types (datavars.go).
		{"typedesc", nil, []string{"type:main.T"}, " -datavars=false"},
		{"typedesc", []string{"type:main.T"}, nil, ""},
		{"ifacemethod", nil, []string{"main.T.M"}, ""},
		{"ifacemethod2", []string{"main.T.M"}, nil, ""},
		{"ifacemethod3", []string{"main.S.M"}, nil, ""},
		{"ifacemethod4", nil, []string{"main.T.M"}, ""},
		{"ifacemethod5", []string{"main.S.M"}, nil, ""},
		{"ifacemethod6", []string{"main.S.M"}, []string{"main.S.N"}, ""},
		{"structof_funcof", []string{"main.S.M"}, []string{"main.S.N"}, ""},
		// gd: reflect.TypeFor[T]().Methods() keeps the exported methods
		// of T, and of no other type.
		{"typeformethods", []string{"main.T.M", "main.(*P).M", "main.G[int].M", "main.B.M"},
			[]string{"main.T.m", "main.S.M", "main.(*T).N"}, ""},
		{"globalmap", []string{"main.small", "main.effect"},
			[]string{"main.large"}, ""},
	}
	for _, test := range tests {
		t.Run(test.src+test.flags, func(t *testing.T) {
			t.Parallel()
			src := filepath.Join("testdata", "deadcode", test.src+".go")
			exe := filepath.Join(tmpdir, test.src+strings.TrimSpace(test.flags)+".exe")
			cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-ldflags=-dumpdep"+test.flags, "-o", exe, src)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %v:\n%s", cmd.Args, err, out)
			}
			for _, pos := range test.pos {
				if !bytes.Contains(out, []byte(pos+"\n")) {
					t.Errorf("%s should be reachable. Output:\n%s", pos, out)
				}
			}
			for _, neg := range test.neg {
				if bytes.Contains(out, []byte(neg+"\n")) {
					t.Errorf("%s should not be reachable. Output:\n%s", neg, out)
				}
			}
		})
	}
}
