// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ccmain translates C to Go that keeps C's pointers safe with
// typed capabilities (doc/gd/cc.md). It has no package-level state, so
// any number of translations may run in one process at once.
package ccmain

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Translate translates the C source src, read from file, to the Go
// source of package pkg. With withMain, C's main becomes the program's.
// On an error in generating, the Go source made so far is returned too.
func Translate(file string, src []byte, pkg string, withMain bool) ([]byte, error) {
	toks, err := lex(file, string(src))
	if err != nil {
		return nil, err
	}
	u, err := parse(file, toks)
	if err != nil {
		return nil, err
	}
	out, _, err := generate(file, []*unit{u}, options{pkg: pkg, main: withMain})
	return out, err
}

// Run runs cc with the command-line arguments args (without the command
// name) and returns its exit status.
// limitMemory makes the collector work harder before the translator
// takes most of the memory that is free: many units' trees are kept at
// once, and a large program's are gigabytes. (Only where the system
// says how much is free; GOMEMLIMIT set by the user is kept.)
func limitMemory() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	if avail := memAvailable(); avail > 0 {
		debug.SetMemoryLimit(avail / 2)
	}
}

// memAvailable is the memory that is free, in bytes, where the system
// says (0 if it does not).
func memAvailable() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemAvailable:" {
			if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil && kb > 0 {
				return kb * 1024
			}
		}
	}
	return 0
}

func Run(args []string, stdout, stderr io.Writer) int {
	limitMemory()
	fs := flag.NewFlagSet("cc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pkg := fs.String("pkg", "main", "package of the output")
	withMain := fs.Bool("main", false, "make C's main the program's main")
	undef := fs.Bool("undef", false, "let a call of an undefined function panic when it runs, instead of being an error")
	debug := fs.Bool("debug", false, "write the assertions that Go lays each struct and union out as C does")
	out := fs.String("o", "", "output file")
	cacheDir := fs.String("cache", "", "keep translations in `dir`, and take from it the one of this command line if what it read has not changed")
	unitsDir := fs.String("units", "", "write each C file's Go to `dir` as a file that Go compiles by itself, with the root that makes them a package, and translate only the files that changed")
	var incDirs []string
	fs.Func("I", "search `dir` for #include (repeatable)", func(s string) error {
		incDirs = append(incDirs, s)
		return nil
	})
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cc [-pkg name] [-main] [-I dir]... [-cache dir] -o out.go file.c...")
		fmt.Fprintln(stderr, "Each C file is a translation unit; the Go is one file.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *unitsDir != "" && fs.NArg() >= 1 {
		n, err := translateUnits(fs.Args(), options{pkg: *pkg, main: *withMain, undef: *undef, debug: *debug}, incDirs, *unitsDir)
		if err != nil {
			fmt.Fprintln(stderr, "cc:", err)
			return 1
		}
		fmt.Fprintf(stderr, "cc: %d of %d units translated\n", n, fs.NArg())
		return 0
	}
	if fs.NArg() < 1 || *out == "" {
		fs.Usage()
		return 2
	}
	var cache *ccCache
	var in *inputs
	if *cacheDir != "" {
		var err error
		if cache, err = cacheFor(*cacheDir, args); err != nil {
			fmt.Fprintln(stderr, "cc:", err)
			return 1
		}
		if gosrc, ok := cache.get(); ok {
			if err := os.WriteFile(*out, gosrc, 0o666); err != nil {
				fmt.Fprintln(stderr, "cc:", err)
				return 1
			}
			return 0
		}
		in = newInputs()
	}
	gosrc, err := translateFiles(fs.Args(), options{pkg: *pkg, main: *withMain, undef: *undef, debug: *debug}, incDirs, in)
	if err == nil && cache != nil {
		if err := cache.put(gosrc, in); err != nil {
			fmt.Fprintln(stderr, "cc: cache:", err) // not kept: not an error of the translation
		}
	}
	if gosrc != nil {
		if werr := os.WriteFile(*out, gosrc, 0o666); werr != nil {
			fmt.Fprintln(stderr, "cc:", werr)
			return 1
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "cc:", err)
		return 1
	}
	return 0
}

// Program translates the C files files, a whole program with main, to
// the Go source of package pkg. incDirs are searched for #include.
func Program(files []string, pkg string, incDirs []string) ([]byte, error) {
	return translateFiles(files, options{pkg: pkg, main: true}, incDirs, nil)
}

// translateFiles translates files, each a translation unit, to one Go
// file. What it reads is recorded in in, if in is not nil.
func translateFiles(files []string, o options, incDirs []string, in *inputs) ([]byte, error) {
	units, err := parseFiles(files, incDirs, in)
	if err != nil {
		return nil, err
	}
	out, _, err := generate(files[0], units, o)
	return out, err
}

// parseFiles parses each of files, a translation unit: several at a
// time, since nothing of one is another's until they are linked. The
// error is that of the first file that has one.
func parseFiles(files []string, incDirs []string, in *inputs) ([]*unit, error) {
	units := make([]*unit, len(files))
	errs := make([]error, len(files))
	one := func(i int) {
		f := files[i]
		b, err := in.readFile(f)
		if err != nil {
			errs[i] = err
			return
		}
		toks, err := lexUnitIn([]source{{f, string(b)}}, incDirs, in)
		if err != nil {
			errs[i] = err
			return
		}
		units[i], errs[i] = parse(f, toks)
	}
	if n := min(runtime.GOMAXPROCS(0), len(files)); n > 1 {
		var next atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < n; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(files) {
						return
					}
					one(i)
				}
			}()
		}
		wg.Wait()
	} else {
		for i := range files {
			one(i)
		}
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return units, nil
}
