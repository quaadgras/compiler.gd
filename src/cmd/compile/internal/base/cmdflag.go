// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"cmd/internal/objabi"
	"flag"
	"fmt"
	"internal/buildcfg"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

// Per-Invocation flag plumbing: small re-implementations of the helpers
// in cmd/internal/objabi (Flagcount, Flagfn1, AddVersionFlag,
// Flagparse, Flagprint, expandArgs) wired to gd.Flagset instead of
// flag.CommandLine, and to gd.Exit instead of os.Exit. Keeps multiple
// compile invocations in one process from clobbering each other's
// flag state and lets gd.Exit deliver a status code via runtime.Goexit
// rather than terminating the process.

// flagCount is a flag.Value that behaves like a flag.Bool and a
// flag.Int. -name increments, -name=x sets. Used for the various
// CountFlag fields.
type flagCount int

func (c *flagCount) String() string { return strconv.Itoa(int(*c)) }

func (c *flagCount) Set(s string) error {
	switch s {
	case "true":
		*c++
	case "false":
		*c = 0
	default:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid count %q", s)
		}
		*c = flagCount(n)
	}
	return nil
}

func (c *flagCount) Get() any         { return int(*c) }
func (c *flagCount) IsBoolFlag() bool { return true }

// flagFn1 wraps a func(string) as a flag.Value (for flags that
// accumulate or invoke side effects on parse, like -I).
type flagFn1 func(string)

func (f flagFn1) Set(s string) error { f(s); return nil }
func (f flagFn1) String() string     { return "" }

// flagcount registers a CountFlag on gd.Flagset.
func (gd *Invocation) flagcount(name, usage string, val *int) {
	gd.Flagset.Var((*flagCount)(val), name, usage)
}

// flagfn1 registers a func(string) flag on gd.Flagset.
func (gd *Invocation) flagfn1(name, usage string, f func(string)) {
	gd.Flagset.Var(flagFn1(f), name, usage)
}

// flagprint writes the per-Invocation flag set's defaults to w.
func (gd *Invocation) flagprint(w io.Writer) {
	gd.Flagset.SetOutput(w)
	gd.Flagset.PrintDefaults()
}

// versionFlag is the per-Invocation -V flag. On Set it prints the
// compiler version and (for -V=full or -V=goexperiment) any extra
// build identifiers, then calls gd.Exit(0). Unlike the objabi
// equivalent it doesn't os.Exit, so an in-process embedder survives.
type versionFlag struct {
	gd      *Invocation
	binName string // typically "compile"; falls back to os.Args[0] basename
}

func (v versionFlag) IsBoolFlag() bool { return true }
func (v versionFlag) Get() any         { return nil }
func (v versionFlag) String() string   { return "" }
func (v versionFlag) Set(s string) error {
	name := v.binName
	if name == "" {
		// best-effort fallback for callers that didn't supply a name
		name = os.Args[0]
		name = name[strings.LastIndex(name, `/`)+1:]
		name = name[strings.LastIndex(name, `\`)+1:]
		name = strings.TrimSuffix(name, ".exe")
	}
	p := ""
	if goexperiment := buildcfg.Experiment.String(); goexperiment != "" {
		p = " X:" + goexperiment
	}
	if s == "full" {
		// gd fork: emit buildID for both upstream "devel" and our
		// "gd" version so cmd/go's contentID invalidates rebuilt
		// caches. cmd/go/internal/work/buildid.go routes this through
		// contentID. Borrow objabi's BuildID through the public
		// accessor so we don't carry two copies.
		if strings.Contains(buildcfg.Version, "devel") || strings.HasPrefix(buildcfg.Version, "gd") {
			if id := objabi.BuildID(); id != "" {
				p += " buildID=" + id
			}
		}
	}
	fmt.Printf("%s version %s%s\n", name, buildcfg.Version, p)
	v.gd.Exit(0)
	return nil
}

// addVersionFlag registers -V on gd.Flagset.
func (gd *Invocation) addVersionFlag(binName string) {
	gd.Flagset.Var(versionFlag{gd: gd, binName: binName}, "V", "print version and exit")
}

// expandResponseFiles expands any "@file" arguments in args by
// reading the named file and substituting its whitespace-separated,
// GCC-style quoted tokens (parsed via objabi.ParseArgs). Mirrors
// objabi.expandArgs but is a pure function: no os.Args mutation, no
// global state.
//
// Returned slice may alias args when nothing was expanded.
func expandResponseFiles(args []string) []string {
	var out []string
	for i, s := range args {
		if strings.HasPrefix(s, "@") {
			if out == nil {
				out = make([]string, 0, len(args)*2)
				out = append(out, args[:i]...)
			}
			slurp, err := os.ReadFile(s[1:])
			if err != nil {
				log.Fatal(err)
			}
			tokens := objabi.ParseArgs(slurp)
			out = append(out, expandResponseFiles(tokens)...)
		} else if out != nil {
			out = append(out, s)
		}
	}
	if out == nil {
		return args
	}
	return out
}

// flagparse expands response files in args, then parses them into
// gd.Flagset. usage is the function called on parse error / -h.
//
// Routes flag-package errors through gd.Exit so the in-process
// embedder doesn't see flag's built-in os.Exit — relies on
// gd.Flagset being configured with flag.ContinueOnError (set by
// ParseFlags above).
func (gd *Invocation) flagparse(args []string, usage func()) {
	gd.Flagset.Usage = usage
	args = expandResponseFiles(args)
	if err := gd.Flagset.Parse(args); err != nil {
		if err == flag.ErrHelp {
			// -h / -help: flag already printed usage. Status 0.
			gd.Exit(0)
		}
		// Parse error: flag printed the error + usage. Status 2.
		gd.Exit(2)
	}
}
