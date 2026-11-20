// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Portgen shows what cmd/go builds of the standard packages for GOOS=portable
// in place of their files (see cmd/internal/portgen).
//
// Usage:
//
//	go run cmd/portgen [-v] [-o dir] [-arch amd64] [pkg...]
//
// With no packages, it does the packages whose files differ between the
// hosts, as cmd/go does. It prints what it could not do (with -v, also
// what it copied per host and why), and with -o writes the files it
// makes into dir, by their names relative to GOROOT/src; a file that
// does not build for portable is listed.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"cmd/internal/portgen"
)

var (
	verbose = flag.Bool("v", false, "report the shared declarations copied per host and why")
	outDir  = flag.String("o", "", "write the files to `dir`")
	arch    = flag.String("arch", "amd64", "architecture")
	list    = flag.Bool("l", false, "list the packages whose files differ between the hosts")
	tHost   = flag.String("testhost", "linux", "the host whose tests the packages have")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("portgen: ")
	flag.Parse()
	cfg := portgen.DefaultConfig(runtime.GOROOT(), *arch)
	cfg.Verbose = *verbose
	cfg.TestHost = *tHost
	paths := flag.Args()
	if len(paths) == 0 || *list {
		var err error
		if paths, err = portgen.Candidates(cfg); err != nil {
			log.Fatal(err)
		}
		if *list {
			fmt.Println(strings.Join(paths, "\n"))
			return
		}
	}
	res, err := portgen.Generate(cfg, paths)
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range res.Reports {
		fmt.Println(r)
	}
	if *outDir == "" {
		return
	}
	src := filepath.Join(cfg.GOROOT, "src")
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range res.Files {
			if !yield(n) {
				return
			}
		}
	}) {
		rel, _ := filepath.Rel(src, name)
		b := res.Files[name]
		if b == nil {
			fmt.Printf("not for portable: %s\n", rel)
			continue
		}
		dst := filepath.Join(*outDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o666); err != nil {
			log.Fatal(err)
		}
	}
}
