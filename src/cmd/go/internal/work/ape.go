// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/go/internal/fsys"
	"cmd/internal/apegen"
)

// apeOverlay puts, under GOOS=ape, the files cmd/internal/apegen makes
// of the standard packages whose files differ between the hosts in place
// of theirs: the hosts' copies of what depends on the host, and the
// shared files without it. They are made once for a GOROOT's sources
// (which takes minutes) and kept in the build cache.
func apeOverlay() {
	if cfg.Goos != "ape" {
		return
	}
	key, err := apeKey()
	if err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	root, _, err := cache.DefaultDir()
	if err != nil || root == "off" {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "ape", key)
	js, err := os.ReadFile(filepath.Join(dir, "overlay.json"))
	if err != nil {
		if js, err = apeGenerate(dir); err != nil {
			base.Fatalf("go: GOOS=ape: %v", err)
		}
	}
	var m map[string]string
	if err := json.Unmarshal(js, &m); err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
	if err := fsys.AddReplacements(m); err != nil {
		base.Fatalf("go: GOOS=ape: %v", err)
	}
}

// apeGenerate makes the files into dir and returns its overlay map.
func apeGenerate(dir string) ([]byte, error) {
	fmt.Fprintf(os.Stderr, "go: GOOS=ape: generating the host files of std (once for these sources)\n")
	acfg := apegen.DefaultConfig(cfg.GOROOT, cfg.Goarch)
	paths, err := apegen.Candidates(acfg)
	if err != nil {
		return nil, err
	}
	res, err := apegen.Generate(acfg, paths)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o777); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+".tmp")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	src := cfg.GOROOTsrc
	m := map[string]string{}
	for name, b := range res.Files {
		if b == nil {
			m[name] = ""
			continue
		}
		rel, err := filepath.Rel(src, name)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("generated %s outside GOROOT/src", name)
		}
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

// apeKey identifies the files apegen makes: of this go command (whose
// apegen makes them), for these hosts, of these sources.
func apeKey() (string, error) {
	h := sha256.New()
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeHash, err := cache.FileHash(exe)
	if err != nil {
		return "", err
	}
	acfg := apegen.DefaultConfig(cfg.GOROOT, cfg.Goarch)
	fmt.Fprintf(h, "apegen %x %q %q %q\n", exeHash, acfg.GOROOT, acfg.Hosts, acfg.Arch)
	src := cfg.GOROOTsrc
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			if rel == "cmd" || d.Name() == "testdata" {
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
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
}
