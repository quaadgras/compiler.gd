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

// TestScript runs the script with sh on this machine, for a fake image
// that is a shell script: as it is (as darwin's Mach-O) and with a fake
// ELF header (64 bytes of a shell script) in place of its first bytes.
func TestScript(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" && runtime.GOOS != "darwin" {
		t.Skip("needs a Unix shell")
	}
	if _, ok := machines[runtime.GOARCH]; !ok {
		t.Skip("no uname -m for", runtime.GOARCH)
	}
	native := runtime.GOOS == "darwin"
	head := "\xcf\xfa\xed\xfe\n"
	if native {
		head = "#!/bin/sh\n"
	}
	img := []byte(head + strings.Repeat("#", 64-len(head)-1) + "\necho \"ran $*\"\nexit 0\n")
	elf := []byte("#!/bin/sh\n" + strings.Repeat("#", 64-10-1) + "\n")
	img = append(img, make([]byte, MachOELFOff+len(elf)-len(img))...)
	copy(img[MachOELFOff:], elf)
	out, err := Pack([]Image{{GOARCH: runtime.GOARCH, Hosts: []string{runtime.GOOS}, Data: img}})
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
