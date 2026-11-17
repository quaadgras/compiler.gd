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
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pkg := fs.String("pkg", "main", "package of the output")
	withMain := fs.Bool("main", false, "make C's main the program's main")
	out := fs.String("o", "", "output file")
	var incDirs []string
	fs.Func("I", "search `dir` for #include (repeatable)", func(s string) error {
		incDirs = append(incDirs, s)
		return nil
	})
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cc [-pkg name] [-main] [-I dir]... -o out.go file.c...")
		fmt.Fprintln(stderr, "Each C file is a translation unit; the Go is one file.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 || *out == "" {
		fs.Usage()
		return 2
	}
	gosrc, err := translateFiles(fs.Args(), *pkg, *withMain, incDirs)
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
	return translateFiles(files, pkg, true, incDirs)
}

func translateFiles(files []string, pkg string, withMain bool, incDirs []string) ([]byte, error) {
	var units []*unit
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		toks, err := lexUnitDirs([]source{{f, string(b)}}, incDirs)
		if err != nil {
			return nil, err
		}
		u, err := parse(f, toks)
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	out, _, err := generate(files[0], units, options{pkg: pkg, main: withMain})
	return out, err
}
