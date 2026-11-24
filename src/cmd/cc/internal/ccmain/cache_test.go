// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCache checks that cc -cache gives a translation again only while
// what it read is as it was: the files' contents, and the absence of
// the files it looked for before the ones it found.
func TestCache(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write("a.c", "#include <h.h>\nint f(void);\nint main(void) { return N + f(); }\n")
	write("b.c", "#include \"local.h\"\nint f(void) { return M; }\n")
	write("local.h", "#define M 2\n")
	write("second/h.h", "#define N 1\n")
	cache := filepath.Join(dir, "cache")
	out := filepath.Join(dir, "out.go")
	args := []string{"-cache", cache, "-main", "-I", filepath.Join(dir, "first"), "-I", filepath.Join(dir, "second"), "-o", out, filepath.Join(dir, "a.c"), filepath.Join(dir, "b.c")}
	run := func() []byte {
		t.Helper()
		var stderr bytes.Buffer
		if st := Run(args, &stderr, &stderr); st != 0 {
			t.Fatalf("cc: status %d\n%s", st, &stderr)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cached := func() bool {
		t.Helper()
		c, err := cacheFor(cache, args)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := c.get()
		return ok
	}
	if cached() {
		t.Fatal("cached before the first translation")
	}
	first := run()
	if !cached() {
		t.Fatal("not cached after a translation")
	}
	if again := run(); !bytes.Equal(first, again) {
		t.Fatal("the cached translation differs")
	}

	// A file that was read changes.
	write("local.h", "#define M 3\n")
	if cached() {
		t.Fatal("cached though local.h changed")
	}
	if changed := run(); bytes.Equal(first, changed) {
		t.Fatal("the translation did not change with local.h")
	}
	if !cached() {
		t.Fatal("not cached after the second translation")
	}

	// A header appears in a directory searched before the one that had it.
	write("first/h.h", "#define N 5\n")
	if cached() {
		t.Fatal("cached though first/h.h now hides second/h.h")
	}
	run()
	if !cached() {
		t.Fatal("not cached after the third translation")
	}

	// Another command line is another translation.
	args = append([]string{"-undef"}, args...)
	if cached() {
		t.Fatal("cached for another command line")
	}
}
