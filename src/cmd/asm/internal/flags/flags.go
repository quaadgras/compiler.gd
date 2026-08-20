// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flags implements top-level flags and the usage message for the assembler.
//
// gd fork: flags live on a per-invocation Context rather than as
// package-level vars, so cmd/asm/host.Run is safe to call concurrently
// from cmd/go's outer parallelism. The package-level Parse() / globals
// are gone — call Parse(args, stderr) for a fresh Context.
package flags

import (
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Context holds parsed assembler flags for one invocation.
type Context struct {
	Debug      bool
	OutputFile string
	TrimPath   string
	Shared     bool
	Dynlink    bool
	Linkshared bool
	AllErrors  bool
	SymABIs    bool
	Importpath string
	Spectre    string
	Std        bool

	D MultiFlag
	I MultiFlag

	DebugFlags struct {
		CompressInstructions int    `help:"use compressed instructions when possible (if supported by architecture)"`
		MayMoreStack         string `help:"call named function before all stack growth checks"`
		PCTab                string `help:"print named pc-value table\nOne of: pctospadj, pctofile, pctoline, pctoinline, pctopcdata"`
	}

	PrintOut int
	DebugV   bool

	// Args holds the positional arguments left after flag parsing
	// (the .s source files).
	Args []string
}

// New returns a Context with default values applied (matching the
// init() defaults that the package-level vars used to carry).
func New() *Context {
	c := &Context{
		Importpath: obj.UnlinkablePkg,
	}
	c.DebugFlags.CompressInstructions = 1
	return c
}

// MultiFlag allows setting a value multiple times to collect a list, as in -I=dir1 -I=dir2.
type MultiFlag []string

func (m *MultiFlag) String() string {
	if len(*m) == 0 {
		return ""
	}
	return fmt.Sprint(*m)
}

func (m *MultiFlag) Set(val string) error {
	(*m) = append(*m, val)
	return nil
}

// Parse parses args (without argv[0]) into a fresh Context. stderr
// receives usage / error output. Returns the Context with Args
// populated, or an error. Callers should treat the returned error as
// "exit with status 2 and print usage" — Parse already wrote the
// diagnostic.
func Parse(args []string, stderr io.Writer) (*Context, error) {
	ctx := New()
	fs := flag.NewFlagSet("asm", flag.ContinueOnError)
	fs.SetOutput(stderr)

	fs.BoolVar(&ctx.Debug, "debug", false, "dump instructions as they are parsed")
	fs.StringVar(&ctx.OutputFile, "o", "", "output file; default foo.o for /a/b/c/foo.s as first argument")
	fs.StringVar(&ctx.TrimPath, "trimpath", "", "remove prefix from recorded source file paths")
	fs.BoolVar(&ctx.Shared, "shared", false, "generate code that can be linked into a shared library")
	fs.BoolVar(&ctx.Dynlink, "dynlink", false, "support references to Go symbols defined in other shared libraries")
	fs.BoolVar(&ctx.Linkshared, "linkshared", false, "generate code that will be linked against Go shared libraries")
	fs.BoolVar(&ctx.AllErrors, "e", false, "no limit on number of errors reported")
	fs.BoolVar(&ctx.SymABIs, "gensymabis", false, "write symbol ABI information to output file, don't assemble")
	fs.StringVar(&ctx.Importpath, "p", obj.UnlinkablePkg, "set expected package import to path")
	fs.StringVar(&ctx.Spectre, "spectre", "", "enable spectre mitigations in `list` (all, ret)")
	fs.BoolVar(&ctx.Std, "std", false, "building standard library")

	fs.Var(&ctx.D, "D", "predefined symbol with optional simple value -D=identifier=value; can be set multiple times")
	fs.Var(&ctx.I, "I", "include directory; can be set multiple times")
	fs.BoolVar(&ctx.DebugV, "v", false, "print debug output")
	fs.Var(objabi.NewDebugFlag(&ctx.DebugFlags, nil), "d", "enable debugging settings; try -d help")
	objabi.AddVersionFlagFS(fs)
	objabi.FlagcountFS(fs, "S", "print assembly and machine code", &ctx.PrintOut)

	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: asm [options] file.s ...\n")
		fmt.Fprintf(stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return nil, fmt.Errorf("no input files")
	}

	ctx.Args = fs.Args()

	if ctx.OutputFile == "" {
		if len(ctx.Args) != 1 {
			fs.Usage()
			return nil, fmt.Errorf("no -o and multiple input files")
		}
		input := filepath.Base(ctx.Args[0])
		input = strings.TrimSuffix(input, ".s")
		ctx.OutputFile = input + ".o"
	}
	return ctx, nil
}
