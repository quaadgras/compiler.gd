// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gd

import (
	"bufio"
	"bytes"
	"cmd/compile/internal/base"
	"cmd/compile/internal/bloop"
	"cmd/compile/internal/coverage"
	"cmd/compile/internal/deadlocals"
	"cmd/compile/internal/dwarfgen"
	"cmd/compile/internal/escape"
	"cmd/compile/internal/inline"
	"cmd/compile/internal/inline/interleaved"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/logopt"
	"cmd/compile/internal/loopvar"
	"cmd/compile/internal/noder"
	"cmd/compile/internal/pgoir"
	"cmd/compile/internal/pkginit"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/rttype"
	"cmd/compile/internal/slice"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/staticinit"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/dwarf"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/src"
	"cmd/internal/telemetry/counter"
	"flag"
	"fmt"
	"internal/buildcfg"
	"log"
	"os"
	"runtime"
)

// handlePanic ensures that we print out an "internal compiler error" for any panic
// or runtime exception during front-end compiler processing (unless there have
// already been some compiler errors). It may also be invoked from the explicit panic in
// hcrash(), in which case, we pass the panic on through.
func handlePanic(gd *base.Invocation) {
	if err := recover(); err != nil {
		if err == "-h" {
			// Force real panic now with -h option (hcrash) - the error
			// information will have already been printed.
			panic(err)
		}
		gd.Fatalf("panic: %v", err)
	}
}

// Main parses flags and Go source files specified in the command-line
// arguments, type-checks the parsed Go package, compiles functions to machine
// code, and finally writes the compiled package definition to disk.
func Main(archInit func(*ssagen.ArchInfo), gd *base.Invocation) {
	gd.Timer.Start("fe", "init")
	counter.Open()
	counter.Inc("compile/invocations")

	defer handlePanic(gd)

	archInit(&ssagen.Arch)

	gd.Ctxt = obj.Linknew(ssagen.Arch.LinkArch)
	gd.Ctxt.DiagFunc = gd.Errorf
	gd.Ctxt.DiagFlush = gd.FlushErrors
	gd.Ctxt.Bso = bufio.NewWriter(os.Stdout)

	// UseBASEntries is preferred because it shaves about 2% off build time, but LLDB, dsymutil, and dwarfdump
	// on Darwin don't support it properly, especially since macOS 10.14 (Mojave).  This is exposed as a flag
	// to allow testing with LLVM tools on Linux, and to help with reporting this bug to the LLVM project.
	// See bugs 31188 and 21945 (CLs 170638, 98075, 72371).
	gd.Ctxt.UseBASEntries = gd.Ctxt.Headtype != objabi.Hdarwin

	gd.DebugSSA = ssa.PhaseOption
	gd.ParseFlags()

	if flagGCStart := gd.Debug.GCStart; flagGCStart > 0 || // explicit flags overrides environment variable disable of GC boost
		os.Getenv("GOGC") == "" && os.Getenv("GOMEMLIMIT") == "" && gd.Flag.LowerC != 1 { // explicit GC knobs or no concurrency implies default heap
		startHeapMB := int64(128)
		if flagGCStart > 0 {
			startHeapMB = int64(flagGCStart)
		}
		gd.AdjustStartingHeap(uint64(startHeapMB)<<20, 0, 0, 0, gd.Debug.GCAdjust == 1)
	}

	localPkg := types.NewPkg(gd.Ctxt.Pkgpath, "")
	localPkg.Local = true
	gd.LocalPkg = localPkg

	// pseudo-package, for scoping
	types.BuiltinPkg = types.NewPkg("go.builtin", "") // TODO(gri) name this package go.builtin?
	types.BuiltinPkg.Prefix = "go:builtin"

	// pseudo-package, accessed by import "unsafe"
	types.UnsafePkg = types.NewPkg("unsafe", "unsafe")

	// Pseudo-package that contains the compiler's builtin
	// declarations for package runtime. These are declared in a
	// separate package to avoid conflicts with package runtime's
	// actual declarations, which may differ intentionally but
	// insignificantly.
	ir.Pkgs.Runtime = types.NewPkg("go.runtime", "runtime")
	ir.Pkgs.Runtime.Prefix = "runtime"

	// Pseudo-package that contains the compiler's builtin
	// declarations for maps.
	ir.Pkgs.InternalMaps = types.NewPkg("go.internal/runtime/maps", "internal/runtime/maps")
	ir.Pkgs.InternalMaps.Prefix = "internal/runtime/maps"

	// pseudo-packages used in symbol tables
	ir.Pkgs.Itab = types.NewPkg("go.itab", "go.itab")
	ir.Pkgs.Itab.Prefix = "go:itab"

	// pseudo-package used for methods with anonymous receivers
	ir.Pkgs.Go = types.NewPkg("go", "")

	// pseudo-package for use with code coverage instrumentation.
	ir.Pkgs.Coverage = types.NewPkg("go.coverage", "runtime/coverage")
	ir.Pkgs.Coverage.Prefix = "runtime/coverage"

	// Record flags that affect the build result. (And don't
	// record flags that don't, since that would cause spurious
	// changes in the binary.)
	dwarfgen.RecordFlags(gd, "B", "N", "l", "msan", "race", "asan", "shared", "dynlink", "dwarf", "dwarflocationlists", "dwarfbasentries", "smallframes", "spectre")

	if !base.EnableTrace && gd.Flag.LowerT {
		log.Fatalf("compiler not built with support for -t")
	}

	// Enable inlining (after RecordFlags, to avoid recording the rewritten -l).  For now:
	//	default: inlining on.  (Flag.LowerL == 1)
	//	-l: inlining off  (Flag.LowerL == 0)
	//	-l=2, -l=3: inlining on again, with extra debugging (Flag.LowerL > 1)
	if gd.Flag.LowerL <= 1 {
		gd.Flag.LowerL = 1 - gd.Flag.LowerL
	}

	if gd.Flag.SmallFrames {
		ir.MaxStackVarSize = 64 * 1024
		ir.MaxImplicitStackVarSize = 16 * 1024
	}

	if gd.Flag.Dwarf {
		gd.Ctxt.DebugInfo = func(ctxt *obj.Link, fn, info *obj.LSym, curfn obj.Func) ([]dwarf.Scope, dwarf.InlCalls) {
			return dwarfgen.Info(gd, ctxt, fn, info, curfn)
		}
		gd.Ctxt.GenAbstractFunc = func(fn *obj.LSym) {
			dwarfgen.AbstractFunc(gd, fn)
		}
		gd.Ctxt.DwFixups = obj.NewDwarfFixupTable(gd.Ctxt)
	} else {
		// turn off inline generation if no dwarf at all
		gd.Flag.GenDwarfInl = 0
		gd.Ctxt.Flag_locationlists = false
	}
	if gd.Ctxt.Flag_locationlists && len(gd.Ctxt.Arch.DWARFRegisters) == 0 {
		log.Fatalf("location lists requested but register mapping not available on %v", gd.Ctxt.Arch.Name)
	}

	types.ParseLangFlag(gd)

	symABIs := ssagen.NewSymABIs(gd)
	if gd.Flag.SymABIs != "" {
		symABIs.ReadSymABIs(gd.Flag.SymABIs)
	}

	if objabi.LookupPkgSpecial(gd.Ctxt.Pkgpath).NoInstrument {
		gd.Flag.Race = false
		gd.Flag.MSan = false
		gd.Flag.ASan = false
	}

	ssagen.Arch.LinkArch.Init(gd.Ctxt)
	startProfile(gd)
	if gd.Flag.Race || gd.Flag.MSan || gd.Flag.ASan {
		gd.Flag.Cfg.Instrumenting = true
	}
	if gd.Flag.Dwarf {
		dwarf.EnableLogging(gd.Debug.DwarfInl != 0)
	}
	if gd.Debug.SoftFloat != 0 {
		ssagen.Arch.SoftFloat = true
	}

	if gd.Flag.JSON != "" { // parse version,destination from json logging optimization.
		logopt.LogJsonOption(gd.Flag.JSON)
	}

	ir.EscFmt = escape.Fmt
	ir.IsIntrinsicCall = func(ce *ir.CallExpr) bool {
		return ssagen.IsIntrinsicCall(gd, ce)
	}
	ir.IsIntrinsicSym = func(s *types.Sym) bool {
		return ssagen.IsIntrinsicSym(gd, s)
	}
	inline.SSADumpInline = ssagen.DumpInline
	ssagen.InitEnv()

	types.PtrSize = ssagen.Arch.LinkArch.PtrSize
	types.RegSize = ssagen.Arch.LinkArch.RegSize
	types.MaxWidth = ssagen.Arch.MAXWIDTH

	gd.Package = new(ir.Package)

	gd.AutogeneratedPos = makePos(gd, src.NewFileBase("<autogenerated>", "<autogenerated>"), 1, 0)

	typecheck.InitUniverse(gd)
	typecheck.InitRuntime(gd)
	rttype.Init(gd)

	// Some intrinsics (notably, the simd intrinsics) mention
	// types "eagerly", thus ssagen must be initialized AFTER
	// the type system is ready.
	ssagen.InitTables(gd)

	// Parse and typecheck input.
	noder.LoadPackage(gd, flag.Args())

	// As a convenience to users (toolchain maintainers, in particular),
	// when compiling a package named "main", we default the package
	// path to "main" if the -p flag was not specified.
	if gd.Ctxt.Pkgpath == obj.UnlinkablePkg && types.LocalPkg(gd).Name == "main" {
		gd.Ctxt.Pkgpath = "main"
		types.LocalPkg(gd).Path = "main"
		types.LocalPkg(gd).Prefix = "main"
	}

	dwarfgen.RecordPackageName(gd)

	// Prepare for backend processing.
	ssagen.InitConfig(gd)

	// Apply coverage fixups, if applicable.
	coverage.Fixup(gd)

	// Read profile file and build profile-graph and weighted-call-graph.
	gd.Timer.Start("fe", "pgo-load-profile")
	var profile *pgoir.Profile
	if gd.Flag.PgoProfile != "" {
		var err error
		profile, err = pgoir.New(gd, gd.Flag.PgoProfile)
		if err != nil {
			log.Fatalf("%s: PGO error: %v", gd.Flag.PgoProfile, err)
		}
	}

	// Apply bloop markings.
	bloop.BloopWalk(gd, typecheck.Target(gd))

	// Interleaved devirtualization and inlining.
	gd.Timer.Start("fe", "devirtualize-and-inline")
	interleaved.DevirtualizeAndInlinePackage(gd, typecheck.Target(gd), profile)

	noder.MakeWrappers(gd, typecheck.Target(gd)) // must happen after inlining

	// Get variable capture right in for loops.
	var transformed []loopvar.VarAndLoop
	for _, fn := range typecheck.Target(gd).Funcs {
		transformed = append(transformed, loopvar.ForCapture(gd, fn)...)
	}
	gd.CurFunc = nil

	// Build init task, if needed.
	pkginit.MakeTask(gd)

	// Generate ABI wrappers. Must happen before escape analysis
	// and doesn't benefit from dead-coding or inlining.
	symABIs.GenABIWrappers()

	deadlocals.Funcs(gd, typecheck.Target(gd).Funcs)

	// Escape analysis.
	// Required for moving heap allocations onto stack,
	// which in turn is required by the closure implementation,
	// which stores the addresses of stack variables into the closure.
	// If the closure does not escape, it needs to be on the stack
	// or else the stack copier will not update it.
	// Large values are also moved off stack in escape analysis;
	// because large values may contain pointers, it must happen early.
	gd.Timer.Start("fe", "escapes")
	escape.Funcs(gd, typecheck.Target(gd).Funcs)

	// gd Phase F4: synthesise compute fns for every detected
	// trivial forwarder. Must run after escape (so GdForwarder
	// fields are populated by DetectForwarders inside
	// escape.Batch.finish) and before FinalizeItabMasks (so the
	// install path can reference the synthesized funcsym). See
	// doc/gd/escape-bits.md §F4.
	if gd.Debug.GdForwarderDisable == 0 {
		escape.SynthesizeForwarderComputeFns(gd, typecheck.Target(gd).Funcs)
	}

	// gd escape-bits: write the per-method EscMask into every itab
	// whose slot was reserved during noder-time writeITab calls. Must
	// run after escape so ir.Func.EscMask is populated. See
	// doc/gd/escape-bits.md.
	reflectdata.FinalizeItabMasks(gd)

	slice.Funcs(gd, typecheck.Target(gd).Funcs)

	loopvar.LogTransformations(gd, transformed)

	// Collect information for go:nowritebarrierrec
	// checking. This must happen before transforming closures during Walk
	// We'll do the final check after write barriers are
	// inserted.
	if gd.Flag.CompilingRuntime {
		ssagen.EnableNoWriteBarrierRecCheck(gd)
	}

	gd.CurFunc = nil

	reflectdata.WriteBasicTypes(gd)

	// Compile top-level declarations.
	//
	// There are cyclic dependencies between all of these phases, so we
	// need to iterate all of them until we reach a fixed point.
	gd.Timer.Start("be", "compilefuncs")
	for nextFunc, nextExtern := 0, 0; ; {
		reflectdata.WriteRuntimeTypes(gd)

		if nextExtern < len(typecheck.Target(gd).Externs) {
			switch n := typecheck.Target(gd).Externs[nextExtern]; n.Op() {
			case ir.ONAME:
				dumpGlobal(gd, n)
			case ir.OLITERAL:
				dumpGlobalConst(gd, n)
			case ir.OTYPE:
				reflectdata.NeedRuntimeType(gd, n.Type())
			}
			nextExtern++
			continue
		}

		if nextFunc < len(typecheck.Target(gd).Funcs) {
			enqueueFunc(gd, typecheck.Target(gd).Funcs[nextFunc], symABIs)
			nextFunc++
			continue
		}

		// The SSA backend supports using multiple goroutines, so keep it
		// as late as possible to maximize how much work we can batch and
		// process concurrently.
		if len(compilequeue) != 0 {
			compileFunctions(gd, profile)
			continue
		}

		// Finalize DWARF inline routine DIEs, then explicitly turn off
		// further DWARF inlining generation to avoid problems with
		// generated method wrappers.
		//
		// Note: The DWARF fixup code for inlined calls currently doesn't
		// allow multiple invocations, so we intentionally run it just
		// once after everything else. Worst case, some generated
		// functions have slightly larger DWARF DIEs.
		if gd.Ctxt.DwFixups != nil {
			gd.Ctxt.DwFixups.Finalize(gd.Ctxt.Pkgpath, gd.Debug.DwarfInl != 0)
			gd.Ctxt.DwFixups = nil
			gd.Flag.GenDwarfInl = 0
			continue // may have called reflectdata.TypeLinksym (#62156)
		}

		break
	}

	gd.Timer.AddEvent(int64(len(typecheck.Target(gd).Funcs)), "funcs")

	if gd.Flag.CompilingRuntime {
		// Write barriers are now known. Check the call graph.
		ssagen.NoWriteBarrierRecCheck()
	}

	// Add keep relocations for global maps.
	if gd.Debug.WrapGlobalMapCtl != 1 {
		staticinit.AddKeepRelocations(gd)
	}

	// Write object data to disk.
	gd.Timer.Start("be", "dumpobj")
	dumpdata(gd)
	gd.Ctxt.NumberSyms()
	dumpobj(gd)
	if gd.Flag.AsmHdr != "" {
		dumpasmhdr(gd)
	}

	ssagen.CheckLargeStacks(gd)
	typecheck.CheckFuncStack(gd)

	if len(compilequeue) != 0 {
		gd.Fatalf("%d uncompiled functions", len(compilequeue))
	}

	logopt.FlushLoggedOpts(gd.Ctxt, gd.Ctxt.Pkgpath)
	gd.ExitIfErrors()

	gd.FlushErrors()
	gd.Timer.Stop()

	if gd.Flag.Bench != "" {
		if err := writebench(gd, gd.Flag.Bench); err != nil {
			log.Fatalf("cannot write benchmark data: %v", err)
		}
	}
}

func writebench(gd *base.Invocation, filename string) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	fmt.Fprintln(&buf, "commit:", buildcfg.Version)
	fmt.Fprintln(&buf, "goos:", runtime.GOOS)
	fmt.Fprintln(&buf, "goarch:", runtime.GOARCH)
	gd.Timer.Write(&buf, "BenchmarkCompile:"+gd.Ctxt.Pkgpath+":")

	n, err := f.Write(buf.Bytes())
	if err != nil {
		return err
	}
	if n != buf.Len() {
		panic("bad writer")
	}

	return f.Close()
}

func makePos(gd *base.Invocation, b *src.PosBase, line, col uint) src.XPos {
	return gd.Ctxt.PosTable.XPos(src.MakePos(b, line, col))
}
