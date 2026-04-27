// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package host exposes a public entry point for invoking cmd/asm
// in-process. cmd/go imports this package to skip the per-.s-file
// fork/exec overhead when building.
//
// gd fork specific: package-level state (flags vars, parser globals)
// has been migrated onto a per-invocation flags.Context / Parser
// fields so two assembles in the same process don't alias state.
package host

import (
	"bufio"
	"fmt"
	"internal/buildcfg"
	"io"
	"log"
	"os"
	"sync"

	"cmd/asm/internal/arch"
	"cmd/asm/internal/asm"
	"cmd/asm/internal/flags"
	"cmd/asm/internal/lex"

	"cmd/internal/bio"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/telemetry/counter"
)

// counter.Open writes process-global state; serialise it across
// concurrent host.Run invocations.
var counterOpenOnce sync.Once

// Run drives one cmd/asm invocation in the calling process. args is
// the argv that would be passed to the standalone `asm` binary, NOT
// including argv[0]. stdout/stderr are reserved for future use; today
// the assembler internals write directly to os.Stdout/os.Stderr (as
// the standalone binary did) and Bso flushes to os.Stdout. Errors
// from the assembler (status != 0) are returned via status.
//
// Concurrent invocations: per-invocation flag state lives on a fresh
// flags.Context, the obj.Link is allocated fresh per call, and the
// Parser carries Debug/AllErrors/etc. as fields rather than reading
// process-globals. Architecture tables (x86.Anames, obj.Anames, etc.)
// are init-time-immutable and safe to share.
func Run(args []string, stdout, stderr io.Writer) (status int, err error) {
	_ = stdout
	_ = stderr

	log.SetFlags(0)
	log.SetPrefix("asm: ")

	counterOpenOnce.Do(counter.Open)

	if buildcfg.Error != nil {
		fmt.Fprintf(os.Stderr, "asm: %v\n", buildcfg.Error)
		return 2, nil
	}
	GOARCH := buildcfg.GOARCH

	ctx, perr := flags.Parse(args, os.Stderr)
	if perr != nil {
		// flags.Parse already printed usage / error to os.Stderr.
		return 2, nil
	}
	counter.Inc("asm/invocations")

	architecture := arch.Set(GOARCH, ctx.Shared || ctx.Dynlink)
	if architecture == nil {
		fmt.Fprintf(os.Stderr, "asm: unrecognized architecture %s\n", GOARCH)
		return 1, nil
	}
	ctxt := obj.Linknew(architecture.LinkArch)
	ctxt.CompressInstructions = ctx.DebugFlags.CompressInstructions != 0
	ctxt.Debugasm = ctx.PrintOut
	ctxt.Debugvlog = ctx.DebugV
	ctxt.Flag_dynlink = ctx.Dynlink
	ctxt.Flag_linkshared = ctx.Linkshared
	ctxt.Flag_shared = ctx.Shared || ctx.Dynlink
	ctxt.Flag_maymorestack = ctx.DebugFlags.MayMoreStack
	ctxt.Debugpcln = ctx.DebugFlags.PCTab
	ctxt.IsAsm = true
	ctxt.Pkgpath = ctx.Importpath
	ctxt.DwTextCount = objabi.DummyDwarfFunctionCountForAssembler()
	switch ctx.Spectre {
	default:
		fmt.Fprintf(os.Stderr, "asm: unknown setting -spectre=%s\n", ctx.Spectre)
		return 2, nil
	case "":
		// nothing
	case "index":
		// known to compiler; ignore here so people can use
		// the same list with -gcflags=-spectre=LIST and -asmflags=-spectre=LIST
	case "all", "ret":
		ctxt.Retpoline = true
	}

	ctxt.Bso = bufio.NewWriter(os.Stdout)
	defer ctxt.Bso.Flush()

	architecture.Init(ctxt)

	buf, ferr := bio.Create(ctx.OutputFile)
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "asm: %v\n", ferr)
		return 1, nil
	}
	defer buf.Close()

	if !ctx.SymABIs {
		buf.WriteString(objabi.HeaderString())
		fmt.Fprintf(buf, "!\n")
	}

	// Set macros for GOEXPERIMENTs so we can easily switch
	// runtime assembly code based on them.
	if objabi.LookupPkgSpecial(ctxt.Pkgpath).AllowAsmABI {
		for _, exp := range buildcfg.Experiment.Enabled() {
			ctx.D = append(ctx.D, "GOEXPERIMENT_"+exp)
		}
	}

	var ok, diag bool
	var failedFile string
	for _, f := range ctx.Args {
		lexer := lex.NewLexer(f, ctx.I, ctx.D, ctx.TrimPath)
		parser := asm.NewParser(ctxt, architecture, lexer)
		parser.SetFlags(ctx.Debug, ctx.AllErrors)
		ctxt.DiagFunc = func(format string, args ...any) {
			diag = true
			log.Printf(format, args...)
		}
		if ctx.SymABIs {
			ok = parser.ParseSymABIs(buf)
		} else {
			pList := new(obj.Plist)
			pList.Firstpc, ok = parser.Parse()
			// reports errors to parser.Errorf
			if ok {
				obj.Flushplist(ctxt, pList, nil)
			}
		}
		if !ok {
			failedFile = f
			break
		}
	}
	if ok && !ctx.SymABIs {
		ctxt.NumberSyms()
		obj.WriteObjFile(ctxt, buf)
	}
	if !ok || diag {
		if failedFile != "" {
			log.Printf("assembly of %s failed", failedFile)
		} else {
			log.Print("assembly failed")
		}
		buf.Close()
		os.Remove(ctx.OutputFile)
		return 1, nil
	}
	return 0, nil
}
