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
	"cmd/compile/internal/loopheapify"
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
	"fmt"
	"internal/buildcfg"
	"log"
	"os"
	"runtime"
	"sync"
)

// processInitOnce / processInitOnce2 / processInitOnce3 serialise
// process-global init writes that the gd.Main flow performs at
// various points across concurrent in-process invocations. The
// values written are identical across invocations targeting the
// same arch (cmd/go passes the same flags every time); a once gate
// is sufficient. Three separate Onces let us preserve the existing
// in-flow ordering (counter.Open and archInit happen before
// ParseFlags, ParseLangFlag happens after dwarf setup, the
// LinkArch / intrinsic / types.PtrSize block happens last before
// LoadPackage).
var (
	processInitOnce  sync.Once
	processInitOnce2 sync.Once
	processInitOnce3 sync.Once
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

// Main parses args (typically os.Args[1:]) and Go source files
// specified in those args, type-checks the parsed Go package, compiles
// functions to machine code, and finally writes the compiled package
// definition to disk. Status is reported via gd.Status; gd.Exit and
// the various error paths use runtime.Goexit rather than os.Exit, so
// an embedder calling Main on a worker goroutine survives a single
// invocation's exit.
func Main(archInit func(*ssagen.ArchInfo), gd *base.Invocation, args []string) {
	// gd in-process: BuiltinPkg / UnsafePkg are process-global
	// (shared across invocations so types match by pointer); their
	// Syms accumulate per-invocation flags like Siggen that need to
	// reset between invocations.
	types.ResetSharedPkgPerInvocationFlags()
	gd.Timer.Start("fe", "init")

	// counter.Open + archInit set process-global state; sync.Once
	// them so concurrent in-process invocations don't race on the
	// init writes. The values they write are identical across
	// invocations targeting the same arch.
	processInitOnce.Do(func() {
		counter.Open()
		archInit(&ssagen.Arch)
	})
	counter.Inc("compile/invocations")

	defer handlePanic(gd)

	gd.Ctxt = obj.Linknew(ssagen.Arch.LinkArch)
	// Publish this invocation's Ctxt so the captured-once HashDebug
	// globals (ConvertHash, FmaHash, etc.) use the right PosTable
	// for hash-position lookups. host.Run's runMu serialises
	// invocations, so the published value is stable for the
	// duration of this compile.
	base.SetCurrentCtxt(gd.Ctxt)
	gd.Ctxt.DiagFunc = gd.Errorf
	gd.Ctxt.DiagFlush = gd.FlushErrors
	gd.Ctxt.Bso = bufio.NewWriter(os.Stdout)

	// UseBASEntries is preferred because it shaves about 2% off build time, but LLDB, dsymutil, and dwarfdump
	// on Darwin don't support it properly, especially since macOS 10.14 (Mojave).  This is exposed as a flag
	// to allow testing with LLVM tools on Linux, and to help with reporting this bug to the LLVM project.
	// See bugs 31188 and 21945 (CLs 170638, 98075, 72371).
	gd.Ctxt.UseBASEntries = gd.Ctxt.Headtype != objabi.Hdarwin

	gd.DebugSSA = ssa.PhaseOption
	gd.ParseFlags(args)

	// Skip AdjustStartingHeap when running in-process (cmd/compile/host.Run
	// from cmd/go). It calls debug.SetGCPercent process-wide to delay GC
	// until heap reaches ~128 MB; in a fork/exec compile that's reclaimed
	// when the process exits, but in-process the cranked-up GOGC sticks
	// across every concurrent invocation in cmd/go's process and RSS grows
	// until the kernel SIGKILLs it. Explicit -d=gcstart still wins.
	if flagGCStart := gd.Debug.GCStart; flagGCStart > 0 || // explicit flags overrides environment variable disable of GC boost
		(!gd.InProcess && os.Getenv("GOGC") == "" && os.Getenv("GOMEMLIMIT") == "" && gd.Flag.LowerC != 1) { // explicit GC knobs or no concurrency implies default heap
		startHeapMB := int64(128)
		if flagGCStart > 0 {
			startHeapMB = int64(flagGCStart)
		}
		gd.AdjustStartingHeap(uint64(startHeapMB)<<20, 0, 0, 0, gd.Debug.GCAdjust == 1)
	}

	localPkg := types.NewPkg(gd, gd.Ctxt.Pkgpath, "")
	localPkg.Local = true
	gd.LocalPkg = localPkg

	pkgs := ir.Pkgs(gd)

	// pseudo-package, for scoping (BuiltinPkg lazy-inits with the
	// "go.builtin" path and "go:builtin" prefix on first call)
	_ = types.BuiltinPkg(gd)

	// pseudo-package, accessed by import "unsafe" (UnsafePkg lazy-inits)
	_ = types.UnsafePkg(gd)

	// Pseudo-package that contains the compiler's builtin
	// declarations for package runtime. These are declared in a
	// separate package to avoid conflicts with package runtime's
	// actual declarations, which may differ intentionally but
	// insignificantly.
	pkgs.Runtime = types.NewPkg(gd, "go.runtime", "runtime")
	pkgs.Runtime.Prefix = "runtime"

	// Pseudo-package that contains the compiler's builtin
	// declarations for maps.
	pkgs.InternalMaps = types.NewPkg(gd, "go.internal/runtime/maps", "internal/runtime/maps")
	pkgs.InternalMaps.Prefix = "internal/runtime/maps"

	// pseudo-packages used in symbol tables
	pkgs.Itab = types.NewPkg(gd, "go.itab", "go.itab")
	pkgs.Itab.Prefix = "go:itab"

	// pseudo-package used for methods with anonymous receivers
	pkgs.Go = types.NewPkg(gd, "go", "")

	// pseudo-package for use with code coverage instrumentation.
	pkgs.Coverage = types.NewPkg(gd, "go.coverage", "runtime/coverage")
	pkgs.Coverage.Prefix = "runtime/coverage"

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

	processInitOnce2.Do(func() {
		types.ParseLangFlag(gd)
	})

	symABIs := ssagen.NewSymABIs(gd)
	if gd.Flag.SymABIs != "" {
		symABIs.ReadSymABIs(gd.Flag.SymABIs)
	}

	if objabi.LookupPkgSpecial(gd.Ctxt.Pkgpath).NoInstrument {
		gd.Flag.Race = false
		gd.Flag.MSan = false
		gd.Flag.ASan = false
	}

	processInitOnce3.Do(func() {
		ssagen.Arch.LinkArch.Init(gd.Ctxt)
		if gd.Flag.Dwarf {
			dwarf.EnableLogging(gd.Debug.DwarfInl != 0)
		}
		if gd.Debug.SoftFloat != 0 {
			ssagen.Arch.SoftFloat = true
		}
		ir.EscFmt = escape.Fmt
		// ir.IsIntrinsicCall / ir.IsIntrinsicSym now take gd at the
		// call site rather than capturing it once: findIntrinsic
		// filters by per-Invocation flags (e.g. gd.Flag.Race
		// excludes sync/atomic), so capturing the first invocation's
		// gd would mismatch a later race-enabled call and crash with
		// a nil intrinsicBuilder dereference in intrinsicCall.
		ir.IsIntrinsicCall = ssagen.IsIntrinsicCall
		ir.IsIntrinsicSym = ssagen.IsIntrinsicSym
		inline.SSADumpInline = ssagen.DumpInline
		ssagen.InitEnv()
		types.PtrSize = ssagen.Arch.LinkArch.PtrSize
		types.RegSize = ssagen.Arch.LinkArch.RegSize
		types.MaxWidth = ssagen.Arch.MAXWIDTH
	})
	startProfile(gd)
	if gd.Flag.Race || gd.Flag.MSan || gd.Flag.ASan {
		gd.Flag.Cfg.Instrumenting = true
	}

	if gd.Flag.JSON != "" { // parse version,destination from json logging optimization.
		logopt.LogJsonOption(gd.Flag.JSON)
	}

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
	noder.LoadPackage(gd, gd.Flagset.Args())

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

	// Loop-heapify pass: hoist SSO inline-vs-heap dispatch out of
	// for-loop bodies that byte-index a string. Must run before
	// escape analysis so the synthesized OUNSAFESTRINGDATA spills
	// participate in the escape solver's flow graph.
	loopheapify.Funcs(gd, typecheck.Target(gd).Funcs)

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
		if len(compilequeue(gd)) != 0 {
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
		ssagen.NoWriteBarrierRecCheck(gd)
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

	if cq := compilequeue(gd); len(cq) != 0 {
		gd.Fatalf("%d uncompiled functions", len(cq))
	}

	logopt.FlushLoggedOpts(gd, gd.Ctxt, gd.Ctxt.Pkgpath)
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
