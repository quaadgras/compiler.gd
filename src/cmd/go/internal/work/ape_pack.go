// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"flag"
	"fmt"
	"internal/syslist"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"cmd/go/internal/apefiles"
	"cmd/go/internal/base"
	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/internal/apepack"
	"cmd/internal/quoted"
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

// apeArches returns the GOARCHes whose executables the one file has:
// $GOAPEARCHES (a comma-separated list), or else every GOARCH GOOS=ape
// has; GOARCH's first.
func apeArches() []string {
	var arches []string
	if s := os.Getenv("GOAPEARCHES"); s != "" {
		arches = strings.Split(s, ",")
	} else {
		arches = slices.Sorted(maps.Keys(syslist.APEHosts))
	}
	out := []string{cfg.Goarch}
	for _, a := range arches {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// apePackedID identifies, for the link action ID of root, what else
// makes its packed file: this go command (the packer), and the GOARCHes
// and, for the other GOARCHes' executables (which another go command
// builds), the sources of std and of root's other packages.
func apePackedID(root *Action) string {
	h := cache.NewHash("apepacked")
	fmt.Fprintf(h, "packer %s\n", apePackerID())
	arches := apeArches()
	fmt.Fprintf(h, "arches %q\n", arches)
	if len(arches) > 1 {
		for _, v := range []string{"GOAMD64", "GOARM64", "GOFLAGS"} {
			fmt.Fprintf(h, "%s=%s\n", v, os.Getenv(v))
		}
		fmt.Fprintf(h, "flags %q\n", apeBuildFlags())
		dirs := map[string]bool{}
		var walk func(a *Action)
		seen := map[*Action]bool{}
		walk = func(a *Action) {
			if seen[a] {
				return
			}
			seen[a] = true
			if p := a.Package; p != nil && !p.Standard && p.Dir != "" {
				dirs[p.Dir] = true
			}
			for _, d := range a.Deps {
				walk(d)
			}
		}
		walk(root)
		key, err := apefiles.SourcesKey(slices.Sorted(maps.Keys(dirs)))
		if err != nil {
			base.Fatalf("go: GOOS=ape: %v", err)
		}
		fmt.Fprintf(h, "sources %s\n", key)
	}
	return fmt.Sprintf("%x", h.Sum())
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

// apeBuildFlags returns the flags of this go build or go install but -o,
// for go build of the other GOARCHes.
func apeBuildFlags() []string {
	cmd := CmdBuild
	if cfg.CmdName == "install" {
		cmd = CmdInstall
	}
	var flags []string
	cmd.Flag.Visit(func(f *flag.Flag) {
		v := f.Value.String()
		switch f.Name {
		case "o":
			return
		case "tags":
			v = strings.Join(cfg.BuildContext.BuildTags, ",")
		case "toolexec":
			v, _ = quoted.Join(cfg.BuildToolexec)
		}
		flags = append(flags, "-"+f.Name+"="+v)
	})
	return flags
}

// apeLink links the executables of the hosts of each GOARCH and packs
// them into target: GOARCH's here, the others' by a go build of each.
func apeLink(root *Action, target string, ldflags []string, link func(string, []string) error) error {
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
	for _, arch := range apeArches()[1:] {
		morePE, more, err := apeBuildArch(root, target, arch)
		if err != nil {
			return err
		}
		if pe == nil {
			pe = morePE
		}
		exes = append(exes, more...)
	}
	data, err := apepack.Pack(pe, exes)
	if err != nil {
		return fmt.Errorf("GOOS=ape: %v", err)
	}
	return os.WriteFile(target, data, 0o777)
}

// apeBuildArch builds root's program for arch, as one file of its own
// hosts, and returns its PE (if it has one) and other executables.
func apeBuildArch(root *Action, target, arch string) (pe []byte, exes []apepack.Exe, err error) {
	if _, ok := syslist.APEHosts[arch]; !ok {
		return nil, nil, fmt.Errorf("GOOS=ape: no GOARCH %s in $GOAPEARCHES", arch)
	}
	p := root.Package
	var pkgs []string
	switch {
	case p.Internal.CmdlineFiles:
		for _, f := range p.GoFiles {
			pkgs = append(pkgs, filepath.Join(p.Dir, f))
		}
	case p.Module != nil && !p.Module.Main && p.Module.Version != "":
		pkgs = []string{p.ImportPath + "@" + p.Module.Version}
	default:
		pkgs = []string{p.ImportPath}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	out := target + ".ape-" + arch
	defer os.Remove(out)
	args := append([]string{"build"}, apeBuildFlags()...)
	args = append(args, "-o", out)
	args = append(args, pkgs...)
	cmd := exec.Command(exe, args...)
	cmd.Dir = base.Cwd()
	cmd.Env = append(os.Environ(), "GOOS=ape", "GOARCH="+arch, "GOAPEARCHES="+arch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("GOOS=ape: building for %s: %v\n%s", arch, err, stderr.Bytes())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, nil, err
	}
	secs, ok := apepack.Exes(bytes.NewReader(data))
	if !ok {
		return nil, nil, fmt.Errorf("GOOS=ape: the %s build wrote no packed file", arch)
	}
	end := int64(len(data))
	for _, s := range secs {
		end = min(end, s.Off)
		exes = append(exes, apepack.Exe{GOARCH: s.GOARCH, Hosts: s.Hosts, Data: data[s.Off : s.Off+s.Size]})
	}
	if apepack.HasPE(data) {
		pe = data[:end]
	}
	return pe, exes, nil
}
