// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"cmd/internal/cov/covcmd"
	"cmd/internal/telemetry/counter"
	"encoding/json"
	"flag"
	"fmt"
	"internal/buildcfg"
	"internal/platform"
	"log"
	"os"
	"reflect"
	"runtime"
	"strings"

	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/sys"
)

func (gd *Invocation) usage() {
	fmt.Fprintf(os.Stderr, "usage: compile [options] file.go...\n")
	gd.flagprint(os.Stderr)
	gd.Exit(2)
}

// A CountFlag is a counting integer flag.
// It accepts -name=value to set the value directly,
// but it also accepts -name with no =value to increment the count.
type CountFlag int

// CmdFlags defines the command-line flags (see var Flag).
// Each struct field is a different flag, by default named for the lower-case of the field name.
// If the flag name is a single letter, the default flag name is left upper-case.
// If the flag name is "Lower" followed by a single letter, the default flag name is the lower-case of the last letter.
//
// If this default flag name can't be made right, the `flag` struct tag can be used to replace it,
// but this should be done only in exceptional circumstances: it helps everyone if the flag name
// is obvious from the field name when the flag is used elsewhere in the compiler sources.
// The `flag:"-"` struct tag makes a field invisible to the flag logic and should also be used sparingly.
//
// Each field must have a `help` struct tag giving the flag help message.
//
// The allowed field types are bool, int, string, pointers to those (for values stored elsewhere),
// CountFlag (for a counting flag), and func(string) (for a flag that uses special code for parsing).
type CmdFlags struct {
	// Single letters
	B CountFlag    "help:\"disable bounds checking\""
	C CountFlag    "help:\"disable printing of columns in error messages\""
	D string       "help:\"set relative `path` for local imports\""
	E CountFlag    "help:\"debug symbol export\""
	I func(string) "help:\"add `directory` to import search path\""
	K CountFlag    "help:\"debug missing line numbers\""
	L CountFlag    "help:\"also show actual source file names in error messages for positions affected by //line directives\""
	N CountFlag    "help:\"disable optimizations\""
	S CountFlag    "help:\"print assembly listing\""
	// V is added by objabi.AddVersionFlag
	W CountFlag "help:\"debug parse tree after type checking\""

	LowerC int        "help:\"concurrency during compilation (1 means no concurrency)\""
	LowerD flag.Value "help:\"enable debugging settings; try -d help\""
	LowerE CountFlag  "help:\"no limit on number of errors reported\""
	LowerH CountFlag  "help:\"halt on error\""
	LowerJ CountFlag  "help:\"debug runtime-initialized variables\""
	LowerL CountFlag  "help:\"disable inlining\""
	LowerM CountFlag  "help:\"print optimization decisions\""
	LowerO string     "help:\"write output to `file`\""
	LowerP *string    "help:\"set expected package import `path`\"" // &Ctxt.Pkgpath, set below
	LowerR CountFlag  "help:\"debug generated wrappers\""
	LowerT bool       "help:\"enable tracing for debugging the compiler\""
	LowerW CountFlag  "help:\"debug type checking\""
	LowerV *bool      "help:\"increase debug verbosity\""

	// Special characters
	Percent          CountFlag "flag:\"%\" help:\"debug non-static initializers\""
	CompilingRuntime bool      "flag:\"+\" help:\"compiling runtime\""

	// Longer names
	AsmHdr             string       "help:\"write assembly header to `file`\""
	ASan               bool         "help:\"build code compatible with C/C++ address sanitizer\""
	Bench              string       "help:\"append benchmark times to `file`\""
	BlockProfile       string       "help:\"write block profile to `file`\""
	BuildID            string       "help:\"record `id` as the build id in the export metadata\""
	CPUProfile         string       "help:\"write cpu profile to `file`\""
	Complete           bool         "help:\"compiling complete package (no C or assembly)\""
	ClobberDead        bool         "help:\"clobber dead stack slots (for debugging)\""
	ClobberDeadReg     bool         "help:\"clobber dead registers (for debugging)\""
	Dwarf              bool         "help:\"generate DWARF symbols\""
	DwarfBASEntries    *bool        "help:\"use base address selection entries in DWARF\""                        // &Ctxt.UseBASEntries, set below
	DwarfLocationLists *bool        "help:\"add location lists to DWARF in optimized mode\""                      // &Ctxt.Flag_locationlists, set below
	Dynlink            *bool        "help:\"support references to Go symbols defined in other shared libraries\"" // &Ctxt.Flag_dynlink, set below
	EmbedCfg           func(string) "help:\"read go:embed configuration from `file`\""
	Env                func(string) "help:\"add `definition` of the form key=value to environment\""
	GenDwarfInl        int          "help:\"generate DWARF inline info records\"" // 0=disabled, 1=funcs, 2=funcs+formals/locals
	GoVersion          string       "help:\"required version of the runtime\""
	ImportCfg          func(string) "help:\"read import configuration from `file`\""
	InstallSuffix      string       "help:\"set pkg directory `suffix`\""
	JSON               string       "help:\"version,file for JSON compiler/optimizer detail output\""
	Lang               string       "help:\"Go language version source code expects\""
	LinkObj            string       "help:\"write linker-specific object to `file`\""
	LinkShared         *bool        "help:\"generate code that will be linked against Go shared libraries\"" // &Ctxt.Flag_linkshared, set below
	Live               CountFlag    "help:\"debug liveness analysis\""
	MSan               bool         "help:\"build code compatible with C/C++ memory sanitizer\""
	MemProfile         string       "help:\"write memory profile to `file`\""
	MemProfileRate     int          "help:\"set runtime.MemProfileRate to `rate`\""
	MutexProfile       string       "help:\"write mutex profile to `file`\""
	NoLocalImports     bool         "help:\"reject local (relative) imports\""
	CoverageCfg        func(string) "help:\"read coverage configuration from `file`\""
	Pack               bool         "help:\"write to file.a instead of file.o\""
	Race               bool         "help:\"enable race detector\""
	Shared             *bool        "help:\"generate code that can be linked into a shared library\"" // &Ctxt.Flag_shared, set below
	SmallFrames        bool         "help:\"reduce the size limit for stack allocated objects\""      // small stacks, to diagnose GC latency; see golang.org/issue/27732
	Spectre            string       "help:\"enable spectre mitigations in `list` (all, index, ret)\""
	Std                bool         "help:\"compiling standard library\""
	SymABIs            string       "help:\"read symbol ABIs from `file`\""
	TraceProfile       string       "help:\"write an execution trace to `file`\""
	TrimPath           string       "help:\"remove `prefix` from recorded source file paths\""
	WB                 bool         "help:\"enable write barrier\"" // TODO: remove
	PgoProfile         string       "help:\"read profile or pre-process profile from `file`\""
	ErrorURL           bool         "help:\"print explanatory URL with error message if applicable\""

	// Configuration derived from flags; not a flag itself.
	Cfg struct {
		Embed struct { // set by -embedcfg
			Patterns map[string][]string
			Files    map[string]string
		}
		ImportDirs   []string                 // appended to by -I
		ImportMap    map[string]string        // set by -importcfg
		PackageFile  map[string]string        // set by -importcfg; nil means not in use
		CoverageInfo *covcmd.CoverFixupConfig // set by -coveragecfg
		SpectreIndex bool                     // set by -spectre=index or -spectre=all
		// Whether we are adding any sort of code instrumentation, such as
		// when the race detector is enabled.
		Instrumenting bool
	}
}

func addEnv(s string) {
	i := strings.Index(s, "=")
	if i < 0 {
		log.Fatal("-env argument must be of the form key=value")
	}
	os.Setenv(s[:i], s[i+1:])
}

// ParseFlags parses args into gd.Flag. args is typically os.Args[1:]
// from cmd/compile/main.go but an in-process embedder can pass any
// slice. A fresh *flag.FlagSet is created on first call so each
// Invocation has its own (no flag.CommandLine sharing across compiles
// in the same process).
func (gd *Invocation) ParseFlags(args []string) {
	if gd.Flagset == nil {
		// ContinueOnError so -h/-help and parse errors come back
		// as flag.ErrHelp / err and we route them through gd.Exit
		// (runtime.Goexit + Status) rather than the flag package's
		// built-in os.Exit. flagparse below maps the errors to
		// status codes.
		gd.Flagset = flag.NewFlagSet("compile", flag.ContinueOnError)
	}
	gd.Flag.I = gd.addImportDir

	gd.Flag.LowerC = runtime.GOMAXPROCS(0)
	gd.Flag.LowerD = objabi.NewDebugFlag(&gd.Debug, gd.DebugSSA)
	gd.Flag.LowerP = &gd.Ctxt.Pkgpath
	gd.Flag.LowerV = &gd.Ctxt.Debugvlog

	gd.Flag.Dwarf = buildcfg.GOARCH != "wasm"
	gd.Flag.DwarfBASEntries = &gd.Ctxt.UseBASEntries
	gd.Flag.DwarfLocationLists = &gd.Ctxt.Flag_locationlists
	*gd.Flag.DwarfLocationLists = true
	gd.Flag.Dynlink = &gd.Ctxt.Flag_dynlink
	gd.Flag.EmbedCfg = gd.readEmbedCfg
	gd.Flag.Env = addEnv
	gd.Flag.GenDwarfInl = 2
	gd.Flag.ImportCfg = gd.readImportCfg
	gd.Flag.CoverageCfg = gd.readCoverageCfg
	gd.Flag.LinkShared = &gd.Ctxt.Flag_linkshared
	gd.Flag.Shared = &gd.Ctxt.Flag_shared
	gd.Flag.WB = true

	gd.Debug.ConcurrentOk = true
	gd.Debug.CompressInstructions = 1
	gd.Debug.MaxShapeLen = 500
	gd.Debug.AlignHot = 1
	gd.Debug.InlFuncsWithClosures = 1
	gd.Debug.InlStaticInit = 1
	gd.Debug.FreeAppend = 1
	gd.Debug.PGOInline = 1
	gd.Debug.PGODevirtualize = 2
	gd.Debug.SyncFrames = -1            // disable sync markers by default
	gd.Debug.VariableMakeThreshold = 32 // 32 byte default for stack allocated make results
	gd.Debug.ZeroCopy = 1
	gd.Debug.RangeFuncCheck = 1
	gd.Debug.MergeLocals = 1

	gd.Debug.Checkptr = -1 // so we can tell whether it is set explicitly

	gd.Flag.Cfg.ImportMap = make(map[string]string)

	gd.addVersionFlag("compile") // -V on gd.Flagset, exits via gd.Exit
	gd.registerFlags()
	gd.flagparse(args, gd.usage)
	counter.CountFlags("compile/flag:", *gd.Flagset)

	if gcd := os.Getenv("GOCOMPILEDEBUG"); gcd != "" {
		// This will only override the flags set in gcd;
		// any others set on the command line remain set.
		gd.Flag.LowerD.Set(gcd)
	}

	if gd.Debug.Gossahash != "" {
		hashDebug = gd.NewHashDebug("gossahash", gd.Debug.Gossahash, nil)
	}
	obj.SetFIPSDebugHash(gd.Debug.FIPSHash)

	// Compute whether we're compiling the runtime from the package path. Test
	// code can also use the flag to set this explicitly.
	if gd.Flag.Std && objabi.LookupPkgSpecial(gd.Ctxt.Pkgpath).Runtime {
		gd.Flag.CompilingRuntime = true
	}

	gd.Ctxt.Std = gd.Flag.Std

	// Three inputs govern loop iteration variable rewriting, hash, experiment, flag.
	// The loop variable rewriting is:
	// IF non-empty hash, then hash determines behavior (function+line match) (*)
	// ELSE IF experiment and flag==0, then experiment (set flag=1)
	// ELSE flag (note that build sets flag per-package), with behaviors:
	//  -1 => no change to behavior.
	//   0 => no change to behavior (unless non-empty hash, see above)
	//   1 => apply change to likely-iteration-variable-escaping loops
	//   2 => apply change, log results
	//   11 => apply change EVERYWHERE, do not log results (for debugging/benchmarking)
	//   12 => apply change EVERYWHERE, log results (for debugging/benchmarking)
	//
	// The expected uses of the these inputs are, in believed most-likely to least likely:
	//  GOEXPERIMENT=loopvar -- apply change to entire application
	//  -gcflags=some_package=-d=loopvar=1 -- apply change to some_package (**)
	//  -gcflags=some_package=-d=loopvar=2 -- apply change to some_package, log it
	//  GOEXPERIMENT=loopvar -gcflags=some_package=-d=loopvar=-1 -- apply change to all but one package
	//  GOCOMPILEDEBUG=loopvarhash=... -- search for failure cause
	//
	//  (*) For debugging purposes, providing loopvar flag >= 11 will expand the hash-eligible set of loops to all.
	// (**) Loop semantics, changed or not, follow code from a package when it is inlined; that is, the behavior
	//      of an application compiled with partially modified loop semantics does not depend on inlining.

	if gd.Debug.LoopVarHash != "" {
		// This first little bit controls the inputs for debug-hash-matching.
		mostInlineOnly := true
		if strings.HasPrefix(gd.Debug.LoopVarHash, "IL") {
			// When hash-searching on a position that is an inline site, default is to use the
			// most-inlined position only.  This makes the hash faster, plus there's no point
			// reporting a problem with all the inlining; there's only one copy of the source.
			// However, if for some reason you wanted it per-site, you can get this.  (The default
			// hash-search behavior for compiler debugging is at an inline site.)
			gd.Debug.LoopVarHash = gd.Debug.LoopVarHash[2:]
			mostInlineOnly = false
		}
		// end of testing trickiness
		LoopVarHash = gd.NewHashDebug("loopvarhash", gd.Debug.LoopVarHash, nil)
		if gd.Debug.LoopVar < 11 { // >= 11 means all loops are rewrite-eligible
			gd.Debug.LoopVar = 1 // 1 means those loops that syntactically escape their dcl vars are eligible.
		}
		LoopVarHash.SetInlineSuffixOnly(mostInlineOnly)
	} else if buildcfg.Experiment.LoopVar && gd.Debug.LoopVar == 0 {
		gd.Debug.LoopVar = 1
	}

	hashGlobalsOnce.Do(func() {
		if gd.Debug.Converthash != "" {
			ConvertHash = gd.NewHashDebug("converthash", gd.Debug.Converthash, nil)
		} else {
			// quietly disable the convert hash changes
			ConvertHash = gd.NewHashDebug("converthash", "qn", nil)
		}
		if gd.Debug.Fmahash != "" {
			FmaHash = gd.NewHashDebug("fmahash", gd.Debug.Fmahash, nil)
		}
		if gd.Debug.PGOHash != "" {
			PGOHash = gd.NewHashDebug("pgohash", gd.Debug.PGOHash, nil)
		}
		if gd.Debug.LiteralAllocHash != "" {
			LiteralAllocHash = gd.NewHashDebug("literalalloc", gd.Debug.LiteralAllocHash, nil)
		}

		if gd.Debug.MergeLocalsHash != "" {
			MergeLocalsHash = gd.NewHashDebug("mergelocals", gd.Debug.MergeLocalsHash, nil)
		}
		if gd.Debug.VariableMakeHash != "" {
			VariableMakeHash = gd.NewHashDebug("variablemake", gd.Debug.VariableMakeHash, nil)
		}
	})

	if gd.Flag.MSan && !platform.MSanSupported(buildcfg.GOOS, buildcfg.GOARCH) {
		log.Fatalf("%s/%s does not support -msan", buildcfg.GOOS, buildcfg.GOARCH)
	}
	if gd.Flag.ASan && !platform.ASanSupported(buildcfg.GOOS, buildcfg.GOARCH) {
		log.Fatalf("%s/%s does not support -asan", buildcfg.GOOS, buildcfg.GOARCH)
	}
	if gd.Flag.Race && !platform.RaceDetectorSupported(buildcfg.GOOS, buildcfg.GOARCH) {
		log.Fatalf("%s/%s does not support -race", buildcfg.GOOS, buildcfg.GOARCH)
	}
	if (*gd.Flag.Shared || *gd.Flag.Dynlink || *gd.Flag.LinkShared) && !gd.Ctxt.Arch.InFamily(sys.AMD64, sys.ARM, sys.ARM64, sys.I386, sys.Loong64, sys.MIPS64, sys.PPC64, sys.RISCV64, sys.S390X) {
		log.Fatalf("%s/%s does not support -shared", buildcfg.GOOS, buildcfg.GOARCH)
	}
	gd.parseSpectre(gd.Flag.Spectre) // left as string for RecordFlags

	gd.Ctxt.CompressInstructions = gd.Debug.CompressInstructions != 0
	gd.Ctxt.Flag_shared = gd.Ctxt.Flag_dynlink || gd.Ctxt.Flag_shared
	gd.Ctxt.Flag_optimize = gd.Flag.N == 0
	gd.Ctxt.Debugasm = int(gd.Flag.S)
	gd.Ctxt.Flag_maymorestack = gd.Debug.MayMoreStack
	gd.Ctxt.Flag_noRefName = gd.Debug.NoRefName != 0

	if gd.Flagset.NArg() < 1 {
		gd.usage()
	}

	if gd.Flag.GoVersion != "" && !versionsCompatible(runtime.Version(), gd.Flag.GoVersion) {
		gd.UsageError("version %q does not match go tool version %q", runtime.Version(), gd.Flag.GoVersion)
	}

	if *gd.Flag.LowerP == "" {
		*gd.Flag.LowerP = obj.UnlinkablePkg
	}

	if gd.Flag.LowerO == "" {
		p := gd.Flagset.Arg(0)
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[i+1:]
		}
		if runtime.GOOS == "windows" {
			if i := strings.LastIndex(p, `\`); i >= 0 {
				p = p[i+1:]
			}
		}
		if i := strings.LastIndex(p, "."); i >= 0 {
			p = p[:i]
		}
		suffix := ".o"
		if gd.Flag.Pack {
			suffix = ".a"
		}
		gd.Flag.LowerO = p + suffix
	}
	switch {
	case gd.Flag.Race && gd.Flag.MSan:
		log.Fatal("cannot use both -race and -msan")
	case gd.Flag.Race && gd.Flag.ASan:
		log.Fatal("cannot use both -race and -asan")
	case gd.Flag.MSan && gd.Flag.ASan:
		log.Fatal("cannot use both -msan and -asan")
	}
	if gd.Flag.Race || gd.Flag.MSan || gd.Flag.ASan {
		// -race, -msan and -asan imply -d=checkptr for now.
		if gd.Debug.Checkptr == -1 { // if not set explicitly
			gd.Debug.Checkptr = 1
		}
	}

	if gd.Flag.LowerC < 1 {
		gd.UsageError("-c must be at least 1, got %d", gd.Flag.LowerC)
	}
	if !gd.concurrentBackendAllowed() {
		gd.Flag.LowerC = 1
	}

	if gd.Flag.CompilingRuntime {
		// It is not possible to build the runtime with no optimizations,
		// because the compiler cannot eliminate enough write barriers.
		gd.Flag.N = 0
		gd.Ctxt.Flag_optimize = true

		// Runtime can't use -d=checkptr, at least not yet.
		gd.Debug.Checkptr = 0

		// Fuzzing the runtime isn't interesting either.
		gd.Debug.Libfuzzer = 0
	}

	if gd.Debug.Checkptr == -1 { // if not set explicitly
		gd.Debug.Checkptr = 0
	}

	// set via a -d flag
	gd.Ctxt.Debugpcln = gd.Debug.PCTab

	// https://golang.org/issue/67502
	if buildcfg.GOOS == "plan9" && buildcfg.GOARCH == "386" {
		gd.Debug.AlignHot = 0
	}
}

// versionsCompatible reports whether two Go version strings refer to
// the same underlying Go release for purposes of the compile -goversion
// check. It tolerates the gd fork's "gd" prefix (e.g. "gd1.26.2") being
// passed alongside a stock prefix from the host go (e.g. "go1.26.1") so
// that running the fork toolchain via `GOROOT=$fork host-go test ...`
// doesn't reject every package with a version mismatch.
//
// The check is intentionally loose: we only require the major.minor
// release line to match. Patch-level differences (e.g. fork's gd1.26.2
// vs host's go1.26.1) are accepted — both share the gd1.26 ABI.
func versionsCompatible(have, want string) bool {
	if have == want {
		return true
	}
	// Strip "go" or "gd" prefix.
	stripPrefix := func(v string) string {
		if strings.HasPrefix(v, "go") || strings.HasPrefix(v, "gd") {
			return v[2:]
		}
		return v
	}
	h := stripPrefix(have)
	w := stripPrefix(want)
	// Match on major.minor only (everything up to the second '.').
	majorMinor := func(v string) string {
		dots := 0
		for i, c := range v {
			if c == '.' {
				dots++
				if dots == 2 {
					return v[:i]
				}
			}
		}
		return v // no second dot → already major.minor
	}
	return majorMinor(h) == majorMinor(w)
}

// registerFlags adds flag registrations for all the fields in Flag.
// See the comment on type CmdFlags for the rules.
func (gd *Invocation) registerFlags() {
	var (
		boolType      = reflect.TypeFor[bool]()
		intType       = reflect.TypeFor[int]()
		stringType    = reflect.TypeFor[string]()
		ptrBoolType   = reflect.TypeFor[*bool]()
		ptrIntType    = reflect.TypeFor[*int]()
		ptrStringType = reflect.TypeFor[*string]()
		countType     = reflect.TypeFor[CountFlag]()
		funcType      = reflect.TypeFor[func(string)]()
	)

	v := reflect.ValueOf(&gd.Flag).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Name == "Cfg" {
			continue
		}

		var name string
		if len(f.Name) == 1 {
			name = f.Name
		} else if len(f.Name) == 6 && f.Name[:5] == "Lower" && 'A' <= f.Name[5] && f.Name[5] <= 'Z' {
			name = string(rune(f.Name[5] + 'a' - 'A'))
		} else {
			name = strings.ToLower(f.Name)
		}
		if tag := f.Tag.Get("flag"); tag != "" {
			name = tag
		}

		help := f.Tag.Get("help")
		if help == "" {
			panic(fmt.Sprintf("base.Flag.%s is missing help text", f.Name))
		}

		if k := f.Type.Kind(); (k == reflect.Ptr || k == reflect.Func) && v.Field(i).IsNil() {
			panic(fmt.Sprintf("base.Flag.%s is uninitialized %v", f.Name, f.Type))
		}

		switch f.Type {
		case boolType:
			p := v.Field(i).Addr().Interface().(*bool)
			gd.Flagset.BoolVar(p, name, *p, help)
		case intType:
			p := v.Field(i).Addr().Interface().(*int)
			gd.Flagset.IntVar(p, name, *p, help)
		case stringType:
			p := v.Field(i).Addr().Interface().(*string)
			gd.Flagset.StringVar(p, name, *p, help)
		case ptrBoolType:
			p := v.Field(i).Interface().(*bool)
			gd.Flagset.BoolVar(p, name, *p, help)
		case ptrIntType:
			p := v.Field(i).Interface().(*int)
			gd.Flagset.IntVar(p, name, *p, help)
		case ptrStringType:
			p := v.Field(i).Interface().(*string)
			gd.Flagset.StringVar(p, name, *p, help)
		case countType:
			p := (*int)(v.Field(i).Addr().Interface().(*CountFlag))
			gd.flagcount(name, help, p)
		case funcType:
			f := v.Field(i).Interface().(func(string))
			gd.flagfn1(name, help, f)
		default:
			if val, ok := v.Field(i).Interface().(flag.Value); ok {
				gd.Flagset.Var(val, name, help)
			} else {
				panic(fmt.Sprintf("base.Flag.%s has unexpected type %s", f.Name, f.Type))
			}
		}
	}
}

// concurrentFlagOk reports whether the current compiler flags
// are compatible with concurrent compilation.
func (gd *Invocation) concurrentFlagOk() bool {
	// TODO(rsc): Many of these are fine. Remove them.
	return gd.Flag.Percent == 0 &&
		gd.Flag.E == 0 &&
		gd.Flag.K == 0 &&
		gd.Flag.L == 0 &&
		gd.Flag.LowerH == 0 &&
		gd.Flag.LowerJ == 0 &&
		gd.Flag.LowerM == 0 &&
		gd.Flag.LowerR == 0
}

func (gd *Invocation) concurrentBackendAllowed() bool {
	if !gd.concurrentFlagOk() {
		return false
	}

	// Debug.S by itself is ok, because all printing occurs
	// while writing the object file, and that is non-concurrent.
	// Adding Debug_vlog, however, causes Debug.S to also print
	// while flushing the plist, which happens concurrently.
	if gd.Ctxt.Debugvlog || !gd.Debug.ConcurrentOk || gd.Flag.Live > 0 {
		return false
	}
	// TODO: Test and delete this condition.
	if buildcfg.Experiment.FieldTrack {
		return false
	}
	// TODO: fix races and enable the following flags
	if gd.Ctxt.Flag_dynlink || gd.Flag.Race {
		return false
	}
	return true
}

func (gd *Invocation) addImportDir(dir string) {
	if dir != "" {
		gd.Flag.Cfg.ImportDirs = append(gd.Flag.Cfg.ImportDirs, dir)
	}
}

func (gd *Invocation) readImportCfg(file string) {
	if gd.Flag.Cfg.ImportMap == nil {
		gd.Flag.Cfg.ImportMap = make(map[string]string)
	}
	gd.Flag.Cfg.PackageFile = map[string]string{}
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("-importcfg: %v", err)
	}

	for lineNum, line := range strings.Split(string(data), "\n") {
		lineNum++ // 1-based
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		verb, args, found := strings.Cut(line, " ")
		if found {
			args = strings.TrimSpace(args)
		}
		before, after, hasEq := strings.Cut(args, "=")

		switch verb {
		default:
			log.Fatalf("%s:%d: unknown directive %q", file, lineNum, verb)
		case "importmap":
			if !hasEq || before == "" || after == "" {
				log.Fatalf(`%s:%d: invalid importmap: syntax is "importmap old=new"`, file, lineNum)
			}
			gd.Flag.Cfg.ImportMap[before] = after
		case "packagefile":
			if !hasEq || before == "" || after == "" {
				log.Fatalf(`%s:%d: invalid packagefile: syntax is "packagefile path=filename"`, file, lineNum)
			}
			gd.Flag.Cfg.PackageFile[before] = after
		}
	}
}

func (gd *Invocation) readCoverageCfg(file string) {
	var cfg covcmd.CoverFixupConfig
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("-coveragecfg: %v", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("error reading -coveragecfg file %q: %v", file, err)
	}
	gd.Flag.Cfg.CoverageInfo = &cfg
}

func (gd *Invocation) readEmbedCfg(file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("-embedcfg: %v", err)
	}
	if err := json.Unmarshal(data, &gd.Flag.Cfg.Embed); err != nil {
		log.Fatalf("%s: %v", file, err)
	}
	if gd.Flag.Cfg.Embed.Patterns == nil {
		log.Fatalf("%s: invalid embedcfg: missing Patterns", file)
	}
	if gd.Flag.Cfg.Embed.Files == nil {
		log.Fatalf("%s: invalid embedcfg: missing Files", file)
	}
}

// parseSpectre parses the spectre configuration from the string s.
func (gd *Invocation) parseSpectre(s string) {
	for f := range strings.SplitSeq(s, ",") {
		f = strings.TrimSpace(f)
		switch f {
		default:
			log.Fatalf("unknown setting -spectre=%s", f)
		case "":
			// nothing
		case "all":
			gd.Flag.Cfg.SpectreIndex = true
			gd.Ctxt.Retpoline = true
		case "index":
			gd.Flag.Cfg.SpectreIndex = true
		case "ret":
			gd.Ctxt.Retpoline = true
		}
	}

	if gd.Flag.Cfg.SpectreIndex {
		switch buildcfg.GOARCH {
		case "amd64":
			// ok
		default:
			log.Fatalf("GOARCH=%s does not support -spectre=index", buildcfg.GOARCH)
		}
	}
}
