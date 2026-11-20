// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package apepack

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPack(t *testing.T) {
	pe := append(DOSStub(), []byte("PE\x00\x00rest of the PE")...)
	elf := []byte("\x7fELF linux and freebsd")
	macho := []byte("\xcf\xfa\xed\xfe darwin")
	out, err := Pack(pe, []Exe{
		{GOARCH: "amd64", Hosts: []string{"linux", "freebsd"}, Data: elf},
		{GOARCH: "arm64", Hosts: []string{"darwin"}, Data: macho},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("MZ")) || !bytes.Equal(out[StubSize:len(pe)], pe[StubSize:]) {
		t.Fatalf("the PE is not where it was")
	}
	secs, ok := Exes(bytes.NewReader(out))
	if !ok || len(secs) != 2 {
		t.Fatalf("Exes = %v, %v", secs, ok)
	}
	for i, want := range [][]byte{elf, macho} {
		s := secs[i]
		if s.Off%align != 0 || !bytes.Equal(out[s.Off:s.Off+s.Size], want) {
			t.Errorf("executable %d at %d is not there", i, s.Off)
		}
	}
	if Exes, ok := Exes(bytes.NewReader(pe)); ok && len(Exes) != 0 {
		t.Errorf("a PE alone has executables %v", Exes)
	}
}

// TestScript runs the script with sh on this machine, for a fake
// executable that is a shell script.
func TestScript(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" && runtime.GOOS != "darwin" {
		t.Skip("needs a Unix shell")
	}
	if _, ok := machines[runtime.GOARCH]; !ok {
		t.Skip("no uname -m for", runtime.GOARCH)
	}
	exe := []byte("#!/bin/sh\necho \"ran $*\"\n")
	out, err := Pack(nil, []Exe{{GOARCH: runtime.GOARCH, Hosts: []string{runtime.GOOS}, Data: exe}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "prog")
	if err := os.WriteFile(file, out, 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 { // copying it out, then not
		cmd := exec.Command("sh", file, "a", "b c")
		cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(dir, "cache"))
		got, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(got)) != "ran a b c" {
			t.Fatalf("sh prog: %q, %v", got, err)
		}
	}
}
