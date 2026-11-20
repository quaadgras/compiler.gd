// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package apefiles puts, under GOOS=ape, the files cmd/internal/apegen
// makes of the packages whose files differ between the hosts in place of
// theirs (an fsys overlay): the hosts' copies of what depends on the
// host, and the shared files without it.
//
// The standard library's are made once for a GOROOT's sources (which
// takes a while); other packages' (a module's, with _linux.go and
// _windows.go files) when a build has some. Both are kept in the build
// cache.
//
// A test binary has one host's tests, as host copies: the host named by
// $GOAPETESTHOST, or else the one the go command runs on (the first host
// if that is none of them).
package apefiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"internal/buildcfg"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"cmd/go/internal/base"
	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/go/internal/fsys"
	"cmd/internal/apegen"
)

// Std puts the standard library's files in place.
func Std() {
	if cfg.Goos != "ape" {
		return
	}
	key, err := stdKey()
	if err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	m, err := cached("std-"+key, func() (*apegen.Result, error) {
		fmt.Fprintf(os.Stderr, "go: generating the files of std for %s (once for these sources)\n", strings.Join(buildcfg.APEHosts(cfg.Goarch), ","))
		acfg := config()
		paths, err := apegen.Candidates(acfg)
		if err != nil {
			return nil, err
		}
		return apegen.Generate(acfg, paths)
	})
	if err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	if err := fsys.AddReplacements(m); err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
}

// A Pkg is a package outside the standard library that a build has.
type Pkg struct {
	ImportPath, Dir string
}

// User puts the files of the packages outside the standard library that
// differ between the hosts in place: those of pkgs, and those only some
// hosts' files of them import. It reports whether there were any.
func User(pkgs []Pkg) bool {
	if cfg.Goos != "ape" {
		return false
	}
	acfg := config()
	differ := false
	for _, p := range pkgs {
		if apegen.Differs(acfg, p.Dir) {
			differ = true
			break
		}
	}
	if !differ {
		return false
	}
	// What each host builds of them, and where.
	var roots []string
	for _, p := range pkgs {
		roots = append(roots, p.ImportPath)
	}
	slices.Sort(roots)
	hosts := map[string]map[string]*listed{}
	byDir := map[string]map[string]string{}
	for _, h := range acfg.Hosts {
		list, err := goList(h, roots)
		if err != nil {
			base.Fatalf("go: GOOS=ape: %v", err)
		}
		hosts[h], byDir[h] = map[string]*listed{}, map[string]string{}
		for _, p := range list {
			hosts[h][p.ImportPath] = p
			byDir[h][p.Dir] = p.ImportPath
		}
	}
	acfg.Resolve = func(host, fromDir, path string) (string, string, bool) {
		rpath := path
		if from := hosts[host][byDir[host][fromDir]]; from != nil && from.ImportMap[path] != "" {
			rpath = from.ImportMap[path]
		}
		if p := hosts[host][rpath]; p != nil && !p.Standard && p.Dir != "" {
			return rpath, p.Dir, true
		}
		if fromDir == "" {
			// (The package itself, which some host has.)
			for _, h := range acfg.Hosts {
				if p := hosts[h][path]; p != nil && !p.Standard && p.Dir != "" {
					return path, p.Dir, true
				}
			}
		}
		return "", "", false
	}
	// The packages to generate, and the key of what they are.
	dirs := map[string]string{}
	for _, h := range acfg.Hosts {
		for path, p := range hosts[h] {
			if !p.Standard && p.Dir != "" {
				dirs[path] = p.Dir
			}
		}
	}
	var gen []string
	for path, dir := range dirs {
		if apegen.Differs(acfg, dir) {
			gen = append(gen, path)
		}
	}
	if len(gen) == 0 {
		return false
	}
	slices.Sort(gen)
	stdK, err := stdKey()
	if err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "std %s\n", stdK)
	for _, path := range slices.Sorted(maps.Keys(dirs)) {
		fmt.Fprintf(h, "pkg %s %s\n", path, dirs[path])
		if err := hashDir(h, dirs[path], false); err != nil {
			base.Fatalf("go: GOOS=ape: %v", err)
		}
	}
	for _, host := range acfg.Hosts {
		for _, path := range slices.Sorted(maps.Keys(hosts[host])) {
			fmt.Fprintf(h, "%s %s %v\n", host, path, hosts[host][path].ImportMap)
		}
	}
	key := hex.EncodeToString(h.Sum(nil)[:16])
	m, err := cached("user-"+key, func() (*apegen.Result, error) {
		fmt.Fprintf(os.Stderr, "go: generating the systems' files of %s\n", strings.Join(gen, ", "))
		std, err := apegen.Candidates(acfg)
		if err != nil {
			return nil, err
		}
		acfg.Analyze = std
		return apegen.Generate(acfg, gen)
	})
	if err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	if err := fsys.AddReplacements(m); err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	return true
}

// A listed is a package as go list lists it.
type listed struct {
	ImportPath string
	Dir        string
	Standard   bool
	ImportMap  map[string]string
}

// goList lists the packages host builds of roots and their dependencies.
func goList(host string, roots []string) ([]*listed, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := append([]string{"list", "-e", "-deps", "-json=ImportPath,Dir,Standard,ImportMap"}, roots...)
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "GOOS="+host, "GOARCH="+cfg.Goarch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list for %s: %v\n%s", host, err, stderr.Bytes())
	}
	var list []*listed
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		p := new(listed)
		if err := dec.Decode(p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, nil
}

// cached returns the overlay map of the files gen makes, which it makes
// only once for key.
func cached(key string, gen func() (*apegen.Result, error)) (map[string]string, error) {
	root, _, err := cache.DefaultDir()
	if err != nil || root == "off" {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "ape", key)
	js, err := os.ReadFile(filepath.Join(dir, "overlay.json"))
	if err != nil {
		res, err := gen()
		if err != nil {
			return nil, err
		}
		if js, err = write(dir, res); err != nil {
			return nil, err
		}
	}
	var m map[string]string
	if err := json.Unmarshal(js, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// write writes res's files into dir and returns its overlay map.
func write(dir string, res *apegen.Result) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o777); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+".tmp")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	m := map[string]string{}
	for i, name := range slices.Sorted(maps.Keys(res.Files)) {
		b := res.Files[name]
		if b == nil {
			m[name] = ""
			continue
		}
		// (Each in a directory of its own: names repeat between
		// packages.)
		rel := filepath.Join(fmt.Sprint(i), filepath.Base(name))
		file := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, b, 0o666); err != nil {
			return nil, err
		}
		m[name] = filepath.Join(dir, rel)
	}
	js, err := json.MarshalIndent(m, "", "\t")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "overlay.json"), js, 0o666); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "reports.txt"), []byte(strings.Join(res.Reports, "\n")+"\n"), 0o666); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another go command made it first.
		if js, err2 := os.ReadFile(filepath.Join(dir, "overlay.json")); err2 == nil {
			return js, nil
		}
		return nil, err
	}
	return js, nil
}

// SourcesKey identifies the sources of a build: the standard library's
// and those in dirs.
func SourcesKey(dirs []string) (string, error) {
	stdK, err := stdKey()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "std %s\n", stdK)
	for _, dir := range dirs {
		fmt.Fprintf(h, "dir %s\n", dir)
		if err := hashDir(h, dir, false); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
}

// stdKey identifies the standard library's files: of this go command
// (whose apegen makes them), for these hosts, of these sources.
var stdKey = sync.OnceValues(func() (string, error) {
	h := sha256.New()
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeHash, err := cache.FileHash(exe)
	if err != nil {
		return "", err
	}
	acfg := config()
	fmt.Fprintf(h, "apegen %x %q %q %q %q\n", exeHash, acfg.GOROOT, acfg.Hosts, acfg.Arch, acfg.TestHost)
	if err := hashDir(h, cfg.GOROOTsrc, true); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
})

// hashDir writes the names, sizes and times of dir's Go, assembly and
// header files to h; with all, of its subdirectories' too (but cmd's
// and testdata's).
func hashDir(h io.Writer, dir string, all bool) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if rel != "." && (!all || rel == "cmd" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".s") && !strings.HasSuffix(path, ".h") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %d %d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
}

func config() apegen.Config {
	acfg := apegen.DefaultConfig(cfg.GOROOT, cfg.Goarch)
	switch h := os.Getenv("GOAPETESTHOST"); {
	case h != "":
		acfg.TestHost = h
	case slices.Contains(acfg.Hosts, runtime.GOOS):
		acfg.TestHost = runtime.GOOS
	default:
		acfg.TestHost = acfg.Hosts[0]
	}
	return acfg
}
