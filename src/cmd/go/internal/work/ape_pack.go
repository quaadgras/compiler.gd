// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"fmt"
	"internal/syslist"
	"os"
	"slices"
	"strings"
	"sync"

	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/internal/apepack"
)

// apePacked reports whether the link of root writes the one file of a
// GOOS=ape program for all its hosts (see cmd/internal/apepack): when go
// build or go install writes a program, unless its -ldflags ask for one
// -apeformat. (Test binaries and go run's programs are run here, by a
// go_ape_GOARCH_exec, and stay one executable.)
func apePacked(root *Action, buildmode string, ldflags []string) bool {
	if cfg.Goos != "ape" || cfg.BuildN || (cfg.CmdName != "build" && cfg.CmdName != "install") {
		return false
	}
	if root.Package.Internal.TestmainGo != nil || (buildmode != "exe" && buildmode != "pie") {
		return false
	}
	for _, f := range ldflags {
		if strings.Contains(f, "-apeformat") {
			return false
		}
	}
	return true
}

// apePackerID identifies the packer: this go command.
var apePackerID = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	h, err := cache.FileHash(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h)
})

// apeLink links the executable of each format the hosts of GOARCH
// need and packs them into target.
func apeLink(target string, ldflags []string, link func(string, []string) error) error {
	formats := map[string][]string{} // format -> hosts
	for _, h := range syslist.APEHosts[cfg.Goarch] {
		switch h {
		case "windows":
			formats["pe"] = append(formats["pe"], h)
		case "darwin":
			formats["macho"] = append(formats["macho"], h)
		default:
			formats["elf"] = append(formats["elf"], h)
		}
	}
	var pe []byte
	var exes []apepack.Exe
	for _, f := range []string{"pe", "elf", "macho"} {
		hosts := formats[f]
		if hosts == nil {
			continue
		}
		out := target + ".ape-" + f
		err := link(out, append(slices.Clip(ldflags), "-apeformat="+f))
		if err != nil {
			os.Remove(out)
			return err
		}
		data, err := os.ReadFile(out)
		os.Remove(out)
		if err != nil {
			return err
		}
		if f == "pe" {
			pe = data
		} else {
			exes = append(exes, apepack.Exe{GOARCH: cfg.Goarch, Hosts: hosts, Data: data})
		}
	}
	data, err := apepack.Pack(pe, exes)
	if err != nil {
		return fmt.Errorf("GOOS=ape: %v", err)
	}
	return os.WriteFile(target, data, 0o777)
}
