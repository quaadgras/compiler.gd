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
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPack(t *testing.T) {
	pe := append(DOSStub(), []byte("PE\x00\x00rest of the PE")...)
	macho := []byte("\xcf\xfa\xed\xfe linux and darwin")
	out, err := Pack([]Image{
		{GOARCH: "amd64", Hosts: []string{"linux", "freebsd", "windows"}, Data: pe},
		{GOARCH: "arm64", Hosts: []string{"linux", "darwin"}, Data: macho},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("MZ")) || !bytes.Equal(out[StubSize:len(pe)], pe[StubSize:]) {
		t.Fatalf("the PE is not where it was")
	}
	secs, ok := Images(bytes.NewReader(out))
	if !ok || len(secs) != 2 {
		t.Fatalf("Images = %v, %v", secs, ok)
	}
	if s := secs[0]; s.Off != 0 || s.Size != int64(len(pe)) || s.GOARCH != "amd64" || strings.Join(s.Hosts, ",") != "linux,freebsd,windows" {
		t.Errorf("PE image: %+v", s)
	}
	if s := secs[1]; s.Off%ELFAlign != 0 || !bytes.Equal(out[s.Off:s.Off+s.Size], macho) || s.GOARCH != "arm64" {
		t.Errorf("Mach-O image: %+v", s)
	}
}

// TestScript runs the script with sh on this machine, with a shell
// script in the loader's place that prints its arguments.
func TestScript(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		t.Skip("needs an ELF host")
	}
	old, ok := loaders[runtime.GOARCH]
	if !ok {
		t.Skip("no loader for", runtime.GOARCH)
	}
	defer func() { loaders[runtime.GOARCH] = old }()
	loaders[runtime.GOARCH] = "#!/bin/sh\necho \"$0\"\nfor a;do echo \"$a\";done\n"

	pe := append(DOSStub(), []byte("PE\x00\x00rest of the PE")...)
	out, err := Pack([]Image{{GOARCH: runtime.GOARCH, Hosts: []string{runtime.GOOS, "windows"}, Data: pe}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "prog")
	if err := os.WriteFile(file, out, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o777); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "./prog", "a", "b c")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "PWD="+dir)
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh prog: %q, %v", got, err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	loader := lines[0]
	if filepath.Base(loader) != "prog" || filepath.Dir(filepath.Dir(loader)) != tmp {
		t.Errorf("the loader was %s", loader)
	}
	if fi, err := os.Stat(filepath.Dir(loader)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the loader's directory: %v, %v", fi, err)
	}
	want := []string{file, "0", strconv.Itoa(PEELFOff), "./prog", "a", "b c"}
	if !slices.Equal(lines[1:], want) {
		t.Errorf("the loader's arguments were %q, not %q", lines[1:], want)
	}
}

// TestPackNoWindows checks the file of a program that is not for
// Windows: a script a Unix kernel runs itself, with its images after it.
func TestPackNoWindows(t *testing.T) {
	pe := append(DOSStub(), []byte("PE\x00\x00rest of the PE")...)
	macho := []byte("\xcf\xfa\xed\xfe linux and darwin")
	out, err := Pack([]Image{
		{GOARCH: "amd64", Hosts: []string{"linux", "darwin"}, Data: pe},
		{GOARCH: "arm64", Hosts: []string{"linux", "darwin"}, Data: macho},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("#!/bin/sh\n")) {
		t.Fatalf("the file starts %q", out[:16])
	}
	secs, ok := Images(bytes.NewReader(out))
	if !ok || len(secs) != 2 {
		t.Fatalf("Images = %v, %v", secs, ok)
	}
	for i, im := range [][]byte{pe, macho} {
		s := secs[i]
		if s.Off == 0 || s.Off%ELFAlign != 0 || !bytes.Equal(out[s.Off:s.Off+s.Size], im) {
			t.Errorf("image %d: %+v", i, s)
		}
	}
}
