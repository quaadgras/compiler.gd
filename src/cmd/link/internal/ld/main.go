// Inferno utils/6l/obj.c
// https://bitbucket.org/inferno-os/inferno-os/src/master/utils/6l/obj.c
//
//	Copyright © 1994-1999 Lucent Technologies Inc.  All rights reserved.
//	Portions Copyright © 1995-1997 C H Forsyth (forsyth@terzarima.net)
//	Portions Copyright © 1997-1999 Vita Nuova Limited
//	Portions Copyright © 2000-2007 Vita Nuova Holdings Limited (www.vitanuova.com)
//	Portions Copyright © 2004,2006 Bruce Ellis
//	Portions Copyright © 2005-2007 C H Forsyth (forsyth@terzarima.net)
//	Revisions Copyright © 2000-2007 Lucent Technologies Inc. and others
//	Portions Copyright © 2009 The Go Authors. All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package ld

import (
	"bufio"
	"cmd/internal/goobj"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"cmd/internal/telemetry/counter"
	"cmd/link/internal/benchmark"
	"flag"
	"internal/buildcfg"
	"io"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
)

// writes a "GUI binary" instead of a "console binary"
// set to true if tmp dir created by linker (e.g. no -tmpdir)

// counter.Open writes process-global state. sync.Once-gate so back-to-back
// host.Run invocations don't double-initialise.
var counterOpenOnce sync.Once

// Flags used by the linker. The exported flags are used by the
// architecture-specific packages.
//
// gd fork: every flag is bound to a *Link field via the per-Link
// *flag.FlagSet (ctxt.flagSet) created in Main. Concurrent in-process
// host.Run invocations no longer race on flag.CommandLine.formal —
// each Main builds its own FlagSet and never touches process globals.

// setupFlags registers every linker flag onto ctxt.flagSet (set up by
// Main before this is called). Each in-process Main call gets a fresh
// FlagSet so concurrent invocations can't race on flag.CommandLine.
func setupFlags(ctxt *Link) {
	fs := ctxt.flagSet
	fs.Var(&ctxt.rpath, "r", "set the ELF dynamic linker search `path` to dir1:dir2:...")
	fs.Var(&ctxt.flagExtld, "extld", "use `linker` when linking in external mode")
	fs.Var(&ctxt.flagExtldflags, "extldflags", "pass `flags` to external linker")
	fs.Var(&ctxt.flagW, "w", "disable DWARF generation")

	fs.StringVar(&ctxt.flagBuildid, "buildid", "", "record `id` as Go toolchain build id")
	fs.BoolVar(&ctxt.flagBindNow, "bindnow", false, "mark a dynamically linked ELF object for immediate function binding")

	fs.StringVar(&ctxt.flagOutfile, "o", "", "write output to `file`")
	fs.StringVar(&ctxt.flagPluginPath, "pluginpath", "", "full path name for plugin")
	fs.StringVar(&ctxt.flagFipso, "fipso", "", "write fips module to `file`")

	fs.StringVar(&ctxt.flagInstallSuffix, "installsuffix", "", "set package directory `suffix`")
	fs.BoolVar(&ctxt.flagDumpDep, "dumpdep", false, "dump symbol dependency graph")
	fs.BoolVar(&ctxt.flagRace, "race", false, "enable race detector")
	fs.BoolVar(&ctxt.flagMsan, "msan", false, "enable MSan interface")
	fs.BoolVar(&ctxt.flagAsan, "asan", false, "enable ASan interface")
	fs.BoolVar(&ctxt.flagAslr, "aslr", true, "enable ASLR for buildmode=c-shared on windows")

	fs.StringVar(&ctxt.flagFieldTrack, "k", "", "set field tracking `symbol`")
	fs.StringVar(&ctxt.flagLibGCC, "libgcc", "", "compiler support lib for internal linking; use \"none\" to disable")
	fs.StringVar(&ctxt.flagTmpdir, "tmpdir", "", "use `directory` for temporary files")

	fs.StringVar(&ctxt.flagExtar, "extar", "", "archive program for buildmode=c-archive")

	fs.StringVar(&ctxt.flagCaptureHostObjs, "capturehostobjs", "", "capture host object files loaded during internal linking to specified dir")

	fs.BoolVar(&ctxt.flagA, "a", false, "no-op (deprecated)")
	fs.BoolVar(&ctxt.FlagC, "c", false, "dump call graph")
	fs.BoolVar(&ctxt.FlagD, "d", false, "disable dynamic executable")
	fs.BoolVar(&ctxt.flagF, "f", false, "ignore version mismatch")
	fs.BoolVar(&ctxt.flagG, "g", false, "disable go package data checks")
	fs.BoolVar(&ctxt.flagH, "h", false, "halt on error")
	fs.BoolVar(&ctxt.flagN, "n", false, "no-op (deprecated)")
	fs.BoolVar(&ctxt.FlagS, "s", false, "disable symbol table")
	fs.StringVar(&ctxt.flagHostBuildid, "B", "", "set ELF NT_GNU_BUILD_ID `note` or Mach-O UUID; use \"gobuildid\" to generate it from the Go build ID; \"none\" to disable")
	fs.StringVar(&ctxt.flagInterpreter, "I", "", "use `linker` as ELF dynamic linker")
	fs.BoolVar(&ctxt.flagCheckLinkname, "checklinkname", true, "check linkname symbol references")
	fs.IntVar(&ctxt.FlagDebugTramp, "debugtramp", 0, "debug trampolines")
	fs.IntVar(&ctxt.FlagDebugTextSize, "debugtextsize", 0, "debug text section max size")
	fs.BoolVar(&ctxt.flagDebugNosplit, "debugnosplit", false, "dump nosplit call graph")
	fs.IntVar(&ctxt.FlagStrictDups, "strictdups", 0, "sanity check duplicate symbol contents during object file reading (1=warn 2=err).")
	fs.Int64Var(&ctxt.FlagRound, "R", -1, "set address rounding `quantum`")
	fs.Int64Var(&ctxt.FlagTextAddr, "T", -1, "set the start address of text symbols")
	fs.Int64Var(&ctxt.FlagDataAddr, "D", -1, "set the start address of data symbols")
	fs.IntVar(&ctxt.FlagFuncAlign, "funcalign", 0, "set function align to `N` bytes")
	fs.StringVar(&ctxt.flagEntrySymbol, "E", "", "set `entry` symbol name")
	fs.BoolVar(&ctxt.flagPruneWeakMap, "pruneweakmap", true, "prune weak mapinit refs")
	fs.Int64Var(&ctxt.flagRandLayout, "randlayout", 0, "randomize function layout")
	fs.BoolVar(&ctxt.flagAllErrors, "e", false, "no limit on number of errors reported")
	fs.StringVar(&ctxt.cpuprofile, "cpuprofile", "", "write cpu profile to `file`")
	fs.StringVar(&ctxt.memprofile, "memprofile", "", "write memory profile to `file`")
	fs.Int64Var(&ctxt.memprofilerate, "memprofilerate", 0, "set runtime.MemProfileRate to `rate`")
	fs.StringVar(&ctxt.benchmarkFlag, "benchmark", "", "set to 'mem' or 'cpu' to enable phase benchmarking")
	fs.StringVar(&ctxt.benchmarkFileFlag, "benchmarkprofile", "", "emit phase profiles to `base`_phase.{cpu,mem}prof")
}

// ternaryFlag is like a boolean flag, but has a default value that is
// neither true nor false, allowing it to be set from context (e.g. from another
// flag).
// *ternaryFlag implements flag.Value.
type ternaryFlag int

const (
	ternaryFlagUnset ternaryFlag = iota
	ternaryFlagFalse
	ternaryFlagTrue
)

func (t *ternaryFlag) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	if v {
		*t = ternaryFlagTrue
	} else {
		*t = ternaryFlagFalse
	}
	return nil
}

func (t *ternaryFlag) String() string {
	switch *t {
	case ternaryFlagFalse:
		return "false"
	case ternaryFlagTrue:
		return "true"
	}
	return "unset"
}

func (t *ternaryFlag) IsBoolFlag() bool { return true } // parse like a boolean flag

// Main is the main entry point for the linker code.
//
// args is the argv that the standalone link binary would have received
// as os.Args[1:]. status, when non-nil, makes Exit write the exit code
// to *status and call runtime.Goexit instead of os.Exit — used by
// cmd/link/host.Run to drive Main on a worker goroutine without
// terminating cmd/go's process. Pass nil for the standalone binary
// path. stdout receives ctxt.Bso flushes (-v / progress chatter); pass
// nil to fall back to os.Stdout for the standalone binary.
func Main(arch *sys.Arch, theArch Arch, args []string, status *int, stdout io.Writer) {
	log.SetPrefix("link: ")
	log.SetFlags(0)
	counterOpenOnce.Do(counter.Open)
	counter.Inc("link/invocations")

	// nerrors / strictDupMsgCount are per-Link now (linkState fields
	// zero-initialised by linknew). legacyAtExitFuncs holds early-startup
	// hooks; reset before each Main so we don't drag prior invocations'
	// hooks into this one.
	legacyAtExitFuncs = nil

	ctxt := linknew(arch)
	ctxt.thearch = theArch
	if stdout == nil {
		stdout = os.Stdout
	}
	ctxt.Bso = bufio.NewWriter(stdout)
	ctxt.inProcessStatus = status

	// Per-Link *flag.FlagSet — never mutate flag.CommandLine. Concurrent
	// in-process Main calls used to race on flag.CommandLine.formal
	// inside flag.(*FlagSet).Var ("concurrent map writes" panic).
	// ContinueOnError so a malformed args slice from one in-process
	// caller doesn't os.Exit the whole cmd/go process.
	progName := "link"
	if len(os.Args) > 0 {
		progName = os.Args[0]
	}
	ctxt.flagSet = flag.NewFlagSet(progName, flag.ContinueOnError)

	// Publish ctxt as the currentLink (used outside the worker goroutine)
	// and as this goroutine's link (used by AtExit/Exit/Errorf to route
	// to the correct invocation under concurrent host.Run calls).
	currentLink = ctxt
	SetCurrentLink(ctxt)
	defer func() {
		currentLink = nil
		SetCurrentLink(nil)
	}()

	// Free every mmap'd input block bio handed out during the link.
	// Without this the long-lived bin/go (cmd/link/host runs in-process)
	// accumulates GBs of file-backed VM across hundreds of test-binary
	// links — bio's contract used to be "never unmapped" because the
	// stock linker exits per invocation. See linkState.mmaps.
	defer ctxt.releaseMmaps()

	setupFlags(ctxt)

	// For testing behavior of go command when tools crash silently.
	// Undocumented, not in standard flag parser to avoid exposing in
	// usage message.
	for _, arg := range args {
		if arg == "-crash_for_testing" {
			Exit(2)
		}
	}

	if buildcfg.GOROOT == "" {
		// cmd/go clears the GOROOT variable when -trimpath is set,
		// so omit it from the binary even if cmd/link itself has an
		// embedded GOROOT value reported by runtime.GOROOT.
	} else {
		addstrdata1(ctxt, "runtime.defaultGOROOT="+buildcfg.GOROOT)
	}

	buildVersion := buildcfg.Version
	if goexperiment := buildcfg.Experiment.String(); goexperiment != "" {
		sep := " "
		if !strings.Contains(buildVersion, "-") { // See go.dev/issue/75953.
			sep = "-"
		}
		buildVersion += sep + "X:" + goexperiment
	}
	addstrdata1(ctxt, "runtime.buildVersion="+buildVersion)

	// TODO(matloob): define these above and then check flag values here
	fs := ctxt.flagSet
	if ctxt.Arch.Family == sys.AMD64 && buildcfg.GOOS == "plan9" {
		fs.BoolVar(&ctxt.flag8, "8", false, "use 64-bit addresses in symbol table")
	}
	flagHeadType := fs.String("H", "", "set header `type`")
	fs.BoolVar(&ctxt.linkShared, "linkshared", false, "link against installed Go shared libraries")
	fs.Var(&ctxt.LinkMode, "linkmode", "set link `mode`")
	fs.Var(&ctxt.BuildMode, "buildmode", "set build `mode`")
	fs.BoolVar(&ctxt.compressDWARF, "compressdwarf", true, "compress DWARF if possible")
	objabi.Flagfn1FS(fs, "L", "add specified `directory` to library path", func(a string) { Lflag(ctxt, a) })
	objabi.AddVersionFlagFS(fs) // -V
	objabi.Flagfn1FS(fs, "X", "add string value `definition` of the form importpath.name=value", func(s string) { addstrdata1(ctxt, s) })
	objabi.FlagcountFS(fs, "v", "print link trace", &ctxt.Debugvlog)
	objabi.Flagfn1FS(fs, "importcfg", "read import configuration from `file`", ctxt.readImportCfg)

	if err := objabi.FlagparseFS(fs, args, usage); err != nil {
		usage()
		Exit(2)
	}
	counter.CountFlags("link/flag:", *fs)

	if ctxt.Debugvlog > 0 {
		// dump symbol info on crash
		defer func() { ctxt.loader.Dump() }()
	}
	if ctxt.Debugvlog > 1 {
		// dump symbol info on error
		ctxt.AtExit(func() {
			if ctxt.nerrors > 0 {
				ctxt.loader.Dump()
			}
		})
	}

	switch *flagHeadType {
	case "":
	case "windowsgui":
		ctxt.HeadType = objabi.Hwindows
		ctxt.windowsgui = true
	default:
		if err := ctxt.HeadType.Set(*flagHeadType); err != nil {
			Errorf("%v", err)
			usage()
		}
	}
	if ctxt.HeadType == objabi.Hunknown {
		ctxt.HeadType.Set(buildcfg.GOOS)
	}

	if !ctxt.flagAslr && ctxt.BuildMode != BuildModeCShared {
		Errorf("-aslr=false is only allowed for -buildmode=c-shared")
		usage()
	}

	if ctxt.FlagD && ctxt.UsesLibc() {
		Exitf("dynamic linking required on %s; -d flag cannot be used", buildcfg.GOOS)
	}

	isPowerOfTwo := func(n int64) bool {
		return n > 0 && n&(n-1) == 0
	}
	if ctxt.FlagRound != -1 && (ctxt.FlagRound < 4096 || !isPowerOfTwo(ctxt.FlagRound)) {
		Exitf("invalid -R value 0x%x", ctxt.FlagRound)
	}
	if ctxt.FlagFuncAlign != 0 && !isPowerOfTwo(int64(ctxt.FlagFuncAlign)) {
		Exitf("invalid -funcalign value %d", ctxt.FlagFuncAlign)
	}

	ctxt.checkStrictDups = ctxt.FlagStrictDups

	switch ctxt.flagW {
	case ternaryFlagFalse:
		ctxt.FlagW = false
	case ternaryFlagTrue:
		ctxt.FlagW = true
	case ternaryFlagUnset:
		ctxt.FlagW = ctxt.FlagS // -s implies -w if not explicitly set
		if ctxt.IsDarwin() && ctxt.BuildMode == BuildModeCShared {
			ctxt.FlagW = true // default to -w in c-shared mode on darwin, see #61229
		}
	}

	if !buildcfg.Experiment.RegabiWrappers {
		ctxt.abiInternalVer = 0
	}

	startProfile(ctxt)
	if ctxt.BuildMode == BuildModeUnset {
		ctxt.BuildMode.Set("exe")
	}

	if ctxt.BuildMode != BuildModeShared && fs.NArg() != 1 {
		usage()
	}

	if ctxt.flagOutfile == "" {
		ctxt.flagOutfile = "a.out"
		if ctxt.HeadType == objabi.Hwindows {
			ctxt.flagOutfile += ".exe"
		}
	}

	ctxt.interpreter = ctxt.flagInterpreter

	if ctxt.flagHostBuildid == "" && ctxt.flagBuildid != "" {
		ctxt.flagHostBuildid = "gobuildid"
	}
	addbuildinfo(ctxt)

	// enable benchmarking
	var bench *benchmark.Metrics
	if len(ctxt.benchmarkFlag) != 0 {
		if ctxt.benchmarkFlag == "mem" {
			bench = benchmark.New(benchmark.GC, ctxt.benchmarkFileFlag)
		} else if ctxt.benchmarkFlag == "cpu" {
			bench = benchmark.New(benchmark.NoGC, ctxt.benchmarkFileFlag)
		} else {
			Errorf("unknown benchmark flag: %q", ctxt.benchmarkFlag)
			usage()
		}
	}

	bench.Start("libinit")
	libinit(ctxt) // creates outfile
	bench.Start("computeTLSOffset")
	ctxt.computeTLSOffset()
	bench.Start("Archinit")
	ctxt.thearch.Archinit(ctxt)

	if ctxt.FlagDataAddr != -1 && ctxt.FlagDataAddr%ctxt.FlagRound != 0 {
		Exitf("invalid -D value 0x%x: not aligned to rounding quantum 0x%x", ctxt.FlagDataAddr, ctxt.FlagRound)
	}

	if ctxt.linkShared && !ctxt.IsELF {
		Exitf("-linkshared can only be used on elf systems")
	}

	if ctxt.Debugvlog != 0 {
		onOff := func(b bool) string {
			if b {
				return "on"
			}
			return "off"
		}
		ctxt.Logf("build mode: %s, symbol table: %s, DWARF: %s\n", ctxt.BuildMode, onOff(!ctxt.FlagS), onOff(dwarfEnabled(ctxt)))
		ctxt.Logf("HEADER = -H%d -T0x%x -R0x%x\n", ctxt.HeadType, uint64(ctxt.FlagTextAddr), uint32(ctxt.FlagRound))
	}

	zerofp := goobj.FingerprintType{}
	switch ctxt.BuildMode {
	case BuildModeShared:
		for i := 0; i < fs.NArg(); i++ {
			arg := fs.Arg(i)
			parts := strings.SplitN(arg, "=", 2)
			var pkgpath, file string
			if len(parts) == 1 {
				pkgpath, file = "main", arg
			} else {
				pkgpath, file = parts[0], parts[1]
			}
			ctxt.pkglistfornote = append(ctxt.pkglistfornote, pkgpath...)
			ctxt.pkglistfornote = append(ctxt.pkglistfornote, '\n')
			addlibpath(ctxt, "command line", "command line", file, pkgpath, "", zerofp)
		}
	case BuildModePlugin:
		addlibpath(ctxt, "command line", "command line", fs.Arg(0), ctxt.flagPluginPath, "", zerofp)
	default:
		addlibpath(ctxt, "command line", "command line", fs.Arg(0), "main", "", zerofp)
	}
	bench.Start("loadlib")
	ctxt.loadlib()

	bench.Start("inittasks")
	ctxt.inittasks()

	bench.Start("deadcode")
	deadcode(ctxt)

	bench.Start("linksetup")
	ctxt.linksetup()

	bench.Start("dostrdata")
	ctxt.dostrdata()
	if buildcfg.Experiment.FieldTrack {
		bench.Start("fieldtrack")
		fieldtrack(ctxt, ctxt.Arch, ctxt.loader)
	}

	bench.Start("dwarfGenerateDebugInfo")
	dwarfGenerateDebugInfo(ctxt)

	bench.Start("callgraph")
	ctxt.callgraph()

	bench.Start("doStackCheck")
	ctxt.doStackCheck()

	bench.Start("mangleTypeSym")
	ctxt.mangleTypeSym()

	if ctxt.IsELF {
		bench.Start("doelf")
		ctxt.doelf()
	}
	if ctxt.IsDarwin() {
		bench.Start("domacho")
		ctxt.domacho()
	}
	if ctxt.IsWindows() {
		bench.Start("dope")
		ctxt.dope()
		bench.Start("windynrelocsyms")
		ctxt.windynrelocsyms()
	}
	if ctxt.IsAIX() {
		bench.Start("doxcoff")
		ctxt.doxcoff()
	}

	bench.Start("textbuildid")
	ctxt.textbuildid()
	bench.Start("addexport")
	ctxt.setArchSyms()
	ctxt.addexport()
	bench.Start("Gentext")
	ctxt.thearch.Gentext(ctxt, ctxt.loader) // trampolines, call stubs, etc.

	bench.Start("textaddress")
	ctxt.textaddress()
	bench.Start("typelink")
	ctxt.typelink()
	bench.Start("buildinfo")
	ctxt.buildinfo()
	bench.Start("pclntab")
	containers := ctxt.findContainerSyms()
	pclnState := ctxt.pclntab(containers)
	bench.Start("findfunctab")
	ctxt.findfunctab(pclnState, containers)
	bench.Start("dwarfGenerateDebugSyms")
	dwarfGenerateDebugSyms(ctxt)
	bench.Start("symtab")
	symGroupType := ctxt.symtab(pclnState)
	bench.Start("dodata")
	ctxt.dodata(symGroupType)
	bench.Start("address")
	order := ctxt.address()
	bench.Start("dwarfcompress")
	dwarfcompress(ctxt)
	bench.Start("layout")
	filesize := ctxt.layout(order)

	// Write out the output file.
	// It is split into two parts (Asmb and Asmb2). The first
	// part writes most of the content (sections and segments),
	// for which we have computed the size and offset, in a
	// mmap'd region. The second part writes more content, for
	// which we don't know the size.
	if ctxt.Arch.Family != sys.Wasm {
		// Don't mmap if we're building for Wasm. Wasm file
		// layout is very different so filesize is meaningless.
		if err := ctxt.Out.Mmap(filesize); err != nil {
			Exitf("mapping output file failed: %v", err)
		}
	}
	// asmb will redirect symbols to the output file mmap, and relocations
	// will be applied directly there.
	bench.Start("Asmb")
	asmb(ctxt)
	ctxt.exitIfErrors()

	// Generate additional symbols for the native symbol table just prior
	// to code generation.
	bench.Start("GenSymsLate")
	if ctxt.thearch.GenSymsLate != nil {
		ctxt.thearch.GenSymsLate(ctxt, ctxt.loader)
	}

	asmbfips(ctxt, ctxt.flagFipso)

	bench.Start("Asmb2")
	asmb2(ctxt)

	bench.Start("Munmap")
	ctxt.Out.Close() // Close handles Munmapping if necessary.

	bench.Start("hostlink")
	ctxt.hostlink()
	if ctxt.Debugvlog != 0 {
		ctxt.Logf("%s", ctxt.loader.Stat())
		ctxt.Logf("%d liveness data\n", ctxt.liveness)
	}
	bench.Start("Flush")
	ctxt.Bso.Flush()
	bench.Start("archive")
	ctxt.archive()
	bench.Report(os.Stdout)

	ctxt.errorexit()
}

type Rpath struct {
	set bool
	val string
}

func (r *Rpath) Set(val string) error {
	r.set = true
	r.val = val
	return nil
}

func (r *Rpath) String() string {
	return r.val
}

func startProfile(ctxt *Link) {
	if ctxt.cpuprofile != "" {
		f, err := os.Create(ctxt.cpuprofile)
		if err != nil {
			log.Fatalf("%v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("%v", err)
		}
		ctxt.AtExit(func() {
			pprof.StopCPUProfile()
			if err = f.Close(); err != nil {
				log.Fatalf("error closing cpu profile: %v", err)
			}
		})
	}
	if ctxt.memprofile != "" {
		if ctxt.memprofilerate != 0 {
			runtime.MemProfileRate = int(ctxt.memprofilerate)
		}
		memprofile := ctxt.memprofile
		f, err := os.Create(memprofile)
		if err != nil {
			log.Fatalf("%v", err)
		}
		ctxt.AtExit(func() {
			// Profile all outstanding allocations.
			runtime.GC()
			const writeLegacyFormat = 1
			if err := pprof.Lookup("heap").WriteTo(f, writeLegacyFormat); err != nil {
				log.Fatalf("%v", err)
			}
			if err := f.Close(); err != nil {
				log.Fatalf("could not close %v: %v", memprofile, err)
			}
		})
	}
}
