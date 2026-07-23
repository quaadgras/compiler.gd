// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"cmd/compile/internal/amd64"
	"cmd/compile/internal/base"
	"cmd/compile/internal/gd"
	"fmt"
	"internal/buildcfg"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestInProcessSingleCompile drives gd.Main once in the same process
// against a minimal source file via the args-as-slice API. Asserts the
// invocation produces an object file and exits with Status==0.
//
// Smoke-test for the per-Invocation flag plumbing: as long as
// Invocation owns its own *flag.FlagSet and gd.Exit uses
// runtime.Goexit (not os.Exit), an embedder can drive a compile by
// passing args directly. cmd/go's eventual in-process invocation of
// compile hangs on this property.
//
// gd.Main is invoked on a worker goroutine because gd.Exit's
// runtime.Goexit would otherwise deadlock the test goroutine. (The
// cmd/compile/main.go harness uses the same dance.)
//
// A second back-to-back invocation in the same process is NOT yet
// supported — types.pkgMap and ir.Pkgs are still package-level
// globals that get populated during InitRuntime. Migrating them onto
// Invocation is the next step toward cmd/go's in-process embedding;
// see TestInProcessTwoCompiles below, which is intentionally skipped
// until that lands.
func TestInProcessSingleCompile(t *testing.T) {
	if buildcfg.GOARCH != "amd64" || runtime.GOOS == "wasip1" {
		t.Skip("test wired to amd64 host arch only")
	}

	tmp := t.TempDir()
	src := filepath.Join(tmp, "a.go")
	if err := os.WriteFile(src, []byte("package a\n\nfunc Foo() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "a.o")
	if status := runInProcess(t, "a", src, out); status != 0 {
		t.Fatalf("compile: status=%d, want 0", status)
	}
	if fi, err := os.Stat(out); err != nil {
		t.Fatalf("compile output missing: %v", err)
	} else if fi.Size() == 0 {
		t.Fatalf("compile output empty")
	}
}

// TestInProcessTwoCompiles drives gd.Main twice in the same process
// with separate Invocations, asserting both produce object files and
// exit cleanly. This is the milestone test for cmd/go's eventual
// in-process embedding — proves nothing in compile pins per-
// Invocation state into package-level globals.
func TestInProcessTwoCompiles(t *testing.T) {
	if buildcfg.GOARCH != "amd64" || runtime.GOOS == "wasip1" {
		t.Skip("test wired to amd64 host arch only")
	}

	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "a.go")
	src2 := filepath.Join(tmp, "b.go")
	if err := os.WriteFile(src1, []byte("package a\n\nfunc Foo() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("package b\n\nfunc Bar() int { return 2 }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out1 := filepath.Join(tmp, "a.o")
	if status := runInProcess(t, "a", src1, out1); status != 0 {
		t.Fatalf("first compile: status=%d, want 0", status)
	}

	out2 := filepath.Join(tmp, "b.o")
	if status := runInProcess(t, "b", src2, out2); status != 0 {
		t.Fatalf("second compile: status=%d, want 0", status)
	}
}

// TestInProcessErrorEmbedWrappers drives two in-process compiles where
// both packages define a type that embeds the universe error interface
// AND declares its own Error method. Regression test: BuiltinPkg's
// types are constructed once per process, pinning the error
// interface's "Error" method Sym to the FIRST invocation's LocalPkg.
// CalcMethods' promoted-method dedup used Sym pointer identity (the
// Uniq flag), so the second invocation saw its declared Error method
// (a distinct Sym with the same exported name) fail to shadow the
// embedded one and generated a duplicate method wrapper:
//
//	internal compiler error: already generated wrapper T.Error
func TestInProcessErrorEmbedWrappers(t *testing.T) {
	if buildcfg.GOARCH != "amd64" || runtime.GOOS == "wasip1" {
		t.Skip("test wired to amd64 host arch only")
	}

	const src = `package %s

type T struct{ error }

func (T) Error() string { return "boom" }

func New(err error) T { return T{err} }
`

	tmp := t.TempDir()
	for i, pkg := range []string{"a", "b"} {
		file := filepath.Join(tmp, pkg+".go")
		if err := os.WriteFile(file, []byte(fmt.Sprintf(src, pkg)), 0644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(tmp, pkg+".o")
		if status := runInProcess(t, pkg, file, out); status != 0 {
			t.Fatalf("compile %d (package %s): status=%d, want 0", i+1, pkg, status)
		}
	}
}

// runInProcess drives one gd.Main invocation on a worker goroutine
// and returns the resulting Status. Mirrors the dance in
// cmd/compile/main.go for handling gd.Exit's runtime.Goexit.
func runInProcess(t *testing.T, pkg, srcfile, outfile string) int {
	t.Helper()
	gd_ := new(base.Invocation)
	args := []string{
		"-p", pkg,
		"-o", outfile,
		"-pack",
		"-complete",
		"-c=1", // single-threaded keeps the worker pool tame inside a test
		srcfile,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		gd.Main(amd64.Init, gd_, args)
	}()
	<-done
	gd_.RunAtExitFuncs()
	return gd_.Status
}

