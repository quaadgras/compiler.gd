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
	"runtime"
	"runtime/debug"
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
// workDir is the directory relative source/#include paths resolve
// against. The standalone binary passes "" (use the process working
// directory); cmd/go passes the package source directory when driving
// the assembler in-process, because the in-process assembler shares
// cmd/go's working directory rather than cd'ing into the package.
//
// Concurrent invocations: per-invocation flag state lives on a fresh
// flags.Context, the obj.Link is allocated fresh per call, and the
// Parser carries Debug/AllErrors/etc. as fields rather than reading
// process-globals. Architecture tables (x86.Anames, obj.Anames, etc.)
// are init-time-immutable and safe to share.
func Run(args []string, workDir string, stdout, stderr io.Writer) (status int, err error) {
	// Mirror compile/link/cgo: run the assembler on a worker
	// goroutine so any os.Exit-replacement (lex.ExitFunc) that
	// bottoms out in runtime.Goexit terminates only this worker, not
	// the calling cmd/go process. Without this, an os.Exit anywhere
	// in the asm internals (e.g. lex.Input.Error, asm.parser.errorf
	// after >10 errors) kills cmd/go before closeBuilders runs and
	// leaks WorkDir under TMPDIR.
	var st int
	prevLexExit := lex.ExitFunc
	lex.ExitFunc = func(code int) {
		st = code
		runtime.Goexit()
	}
	defer func() { lex.ExitFunc = prevLexExit }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "asm: panic: %v\n", r)
				os.Stderr.Write(debug.Stack())
				st = 2
			}
		}()
		st = runMain(args, workDir, stdout, stderr)
	}()
	<-done

	return st, nil
}

func runMain(args []string, workDir string, stdout, stderr io.Writer) int {
	_ = stdout
	_ = stderr

	log.SetFlags(0)
	log.SetPrefix("asm: ")

	counterOpenOnce.Do(counter.Open)

	if buildcfg.Error != nil {
		fmt.Fprintf(os.Stderr, "asm: %v\n", buildcfg.Error)
		return 2
	}
	GOARCH := buildcfg.GOARCH

	ctx, perr := flags.Parse(args, os.Stderr)
	if perr != nil {
		// flags.Parse already printed usage / error to os.Stderr.
		return 2
	}
	counter.Inc("asm/invocations")

	architecture := arch.Set(GOARCH, ctx.Shared || ctx.Dynlink)
	if architecture == nil {
		fmt.Fprintf(os.Stderr, "asm: unrecognized architecture %s\n", GOARCH)
		return 1
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
	ctxt.Std = ctx.Std
	ctxt.DwTextCount = objabi.DummyDwarfFunctionCountForAssembler()
	switch ctx.Spectre {
	default:
		fmt.Fprintf(os.Stderr, "asm: unknown setting -spectre=%s\n", ctx.Spectre)
		return 2
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
		return 1
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
		lexer := lex.NewLexer(f, ctx.I, ctx.D, ctx.TrimPath, workDir)
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
		return 1
	}
	return 0
}
