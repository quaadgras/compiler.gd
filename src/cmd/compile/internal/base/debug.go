// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Debug arguments, set by -d flag.

package base

// DebugFlags defines the debugging configuration values (see var Debug).
// Each struct field is a different value, named for the lower-case of the field name.
// Each field must be an int or string and must have a `help` struct tag.
// Don't forget to note that concurrency is "ok" when it is,
// else the compiler will serialize for that flag.
//
// The -d option takes a comma-separated list of settings.
// Each setting is name=value; for ints, name is short for name=1.
type DebugFlags struct {
	AlignHot              int    `help:"enable hot block alignment (currently requires -pgo)" concurrent:"ok"`
	Append                int    `help:"print information about append compilation"`
	AstDump               string `help:"for specified function/method, dump AST/IR at interesting points in compilation, to file pkg.func.ast. Use leading ~ for regular expression match."`
	CHeaders              int    `help:"with 1, leave the checks of C's pointers and its stores of pointers to the runtime, each a call" concurrent:"ok"`
	Checkptr              int    `help:"instrument unsafe pointer conversions\n0: instrumentation disabled\n1: conversions involving unsafe.Pointer are instrumented\n2: conversions to unsafe.Pointer force heap allocation" concurrent:"ok"`
	Closure               int    `help:"print information about closure compilation"`
	UintptrSurvey         int    `help:"report what is done with values of type uintptr that would change if a uintptr carried a pointer"`
	UnsafeWhole           int    `help:"report conversions that make a partial view of a string, slice or interface, instead of failing at run time\n1: report them\n2: also report those whose source is not known"`
	CompressInstructions  int    `help:"use compressed instructions when possible (if supported by architecture)" concurrent:"ok"`
	Converthash           string `help:"hash value for use in debugging changes to platform-dependent float-to-[u]int conversion" concurrent:"ok"`
	Defer                 int    `help:"print information about defer compilation"`
	DisableNil            int    `help:"disable nil checks" concurrent:"ok"`
	DumpInlFuncProps      string `help:"dump function properties from inl heuristics to specified file"`
	DumpInlCallSiteScores int    `help:"dump scored callsites during inlining"`
	InlScoreAdj           string `help:"set inliner score adjustments (ex: -d=inlscoreadj=panicPathAdj:10/passConstToNestedIfAdj:-90)" concurrent:"ok"`
	InlBudgetSlack        int    `help:"amount to expand the initial inline budget when new inliner enabled. Defaults to 80 if option not set." concurrent:"ok"`
	DumpPtrs              int    `help:"show Node pointers values in dump output"`
	DwarfInl              int    `help:"print information about DWARF inlined function creation"`
	EscapeMutationsCalls  int    `help:"print extra escape analysis diagnostics about mutations and calls" concurrent:"ok"`
	EscapeDebug           int    `help:"print information about escape analysis and resulting optimizations" concurrent:"ok"`
	EscapeAlias           int    `help:"print information about alias analysis" concurrent:"ok"`
	EscapeAliasCheck      int    `help:"enable additional validation for alias analysis" concurrent:"ok"`
	GdEscBitsOff          int    `help:"compile this package without gd-fork escape-bits: locals passed to dynamic calls escape, and results get and use no caller buffers" concurrent:"ok"`
	GdForwarder           int    `help:"log gd-fork Phase-F trivial-forwarder detector matches" concurrent:"ok"`
	GdForwarderDisable    int    `help:"disable gd-fork Phase-F4 trivial-forwarder install (1 to disable; default 0 = enabled)" concurrent:"ok"`
	Export                int    `help:"print export data"`
	FIPSHash              string `help:"hash value for FIPS debugging" concurrent:"ok"`
	Fmahash               string `help:"hash value for use in debugging platform-dependent multiply-add use" concurrent:"ok"`
	FreeAppend            int    `help:"insert frees of append results when proven safe (0 disabled, 1 enabled, 2 enabled + log)" concurrent:"ok"`
	GCAdjust              int    `help:"log adjustments to GOGC" concurrent:"ok"`
	GCCheck               int    `help:"check heap/gc use by compiler" concurrent:"ok"`
	GCProg                int    `help:"print dump of GC programs"`
	GCStart               int    `help:"specify \"starting\" compiler's heap size in MiB" concurrent:"ok"`
	Gossahash             string `help:"hash value for use in debugging the compiler"`
	InlFuncsWithClosures  int    `help:"allow functions with closures to be inlined" concurrent:"ok"`
	InlStaticInit         int    `help:"allow static initialization of inlined calls" concurrent:"ok"`
	InterfaceCycles       int    `help:"allow anonymous interface cycles" concurrent:"ok"`
	Libfuzzer             int    `help:"enable coverage instrumentation for libfuzzer"`
	LiteralAllocHash      string `help:"hash value for use in debugging literal allocation optimizations" concurrent:"ok"`
	LoopVar               int    `help:"shared (0), 1 (private loop variables, default), 2, private + log" concurrent:"ok"`
	LoopVarHash           string `help:"for debugging changes in loop behavior. Overrides experiment and loopvar flag." concurrent:"ok"`
	LocationLists         int    `help:"print information about DWARF location list creation"`
	MaxShapeLen           int    `help:"hash shape names longer than this threshold (default 500)" concurrent:"ok"`
	MergeLocals           int    `help:"merge together non-interfering local stack slots" concurrent:"ok"`
	MergeLocalsDumpFunc   string `help:"dump specified func in merge locals"`
	MergeLocalsHash       string `help:"hash value for debugging stack slot merging of local variables" concurrent:"ok"`
	MergeLocalsTrace      int    `help:"trace debug output for locals merging"`
	MergeLocalsHTrace     int    `help:"hash-selected trace debug output for locals merging"`
	Nil                   int    `help:"print information about nil checks"`
	NoDeadLocals          int    `help:"disable deadlocals pass" concurrent:"ok"`
	NoOpenDefer           int    `help:"disable open-coded defers" concurrent:"ok"`
	NoRefName             int    `help:"do not include referenced symbol names in object file" concurrent:"ok"`
	LocalDbg              int    `help:"decide at run time only for the allocations of these kinds (1 new, 2 append, 4 make, 8 variables; for debugging)" concurrent:"ok"`
	NoLocalMap            int    `help:"do not treat a map as a container that may be a local object (for measurements)" concurrent:"ok"`
	NoLocalRO             int    `help:"count what a string points to as shared where the string goes (for measurements)" concurrent:"ok"`
	NoLocalLink           int    `help:"count what is passed to a function that is only known when the program runs as shared, whatever the linker knows (for measurements)" concurrent:"ok"`
	NoLocalDeep           int    `help:"count what is stored deeper than a parameter as shared (for measurements)" concurrent:"ok"`
	FieldCensus           int    `help:"print where each pointer that is stored in a field comes from (a measurement)" concurrent:"ok"`
	NoLocalMethVal        int    `help:"send what a method value's results may be to the heap where the value is made (for measurements)" concurrent:"ok"`
	NoLocalRet            int    `help:"take what a caller in the batch does with a result for what the function does (for measurements)" concurrent:"ok"`
	NoLocalTyped          int    `help:"share by depth only, not by the type of what is shared (for measurements)" concurrent:"ok"`
	NoLocalRecv           int    `help:"do not keep local what is only the receiver of interface calls (for measurements)" concurrent:"ok"`
	NoLocalBits           int    `help:"do not pass to a function what its caller knows of where its results and what it stores go (for measurements)" concurrent:"ok"`
	NoLocalDyn            int    `help:"do not decide at run time whether an object is local (for measurements)" concurrent:"ok"`
	NoLocalAlloc          int    `help:"do not allocate as local the objects no other goroutine will reach (for measurements)" concurrent:"ok"`
	NoLocal               int    `help:"do not use what escape analysis knows of the memory no other goroutine can reach (for measurements)" concurrent:"ok"`
	ShareCheck            int    `help:"check at run time that the memory found unshared at compile time is" concurrent:"ok"`
	NoMove                int    `help:"share the object of a pointer sent on a channel even if the send hands it over (for measurements)" concurrent:"ok"`
	NoGroup               int    `help:"take the locks of shared memory for each store of a statement (for measurements)" concurrent:"ok"`
	NoTear                int    `help:"do not load and store slices, strings and interfaces whole (unsafe; for measurements)" concurrent:"ok"`
	PCTab                 string `help:"print named pc-value table\nOne of: pctospadj, pctofile, pctoline, pctoinline, pctopcdata"`
	Panic                 int    `help:"show all compiler panics"`
	Reshape               int    `help:"print information about expression reshaping"`
	Shapify               int    `help:"print information about shaping recursive types"`
	Simd                  int    `help:"print information about simd analysis and code transformation" concurrent:"ok"`
	SkipExtInst           int    `help:"rely on imported packages for their generic instantiations (1 skip codegen, 3 treat as imported; 2 and 4 log)" concurrent:"ok"`
	Slice                 int    `help:"print information about slice compilation"`
	SoftFloat             int    `help:"force compiler to emit soft-float code" concurrent:"ok"`
	StackVars             int    `help:"record the variables of each frame for the checks of unsafe pointers\n0: none\n1: those whose address is taken\n2: and those that have pointers (default)" concurrent:"ok"`
	StaticCopy            int    `help:"print information about missed static copies" concurrent:"ok"`
	SyncFrames            int    `help:"how many writer stack frames to include at sync points in unified export data" concurrent:"ok"`
	TailCall              int    `help:"print information about tail calls"`
	TypeAssert            int    `help:"print information about type assertion inlining"`
	WB                    int    `help:"print information about write barriers"`
	ABIWrap               int    `help:"print information about ABI wrapper generation"`
	MayMoreStack          string `help:"call named function before all stack growth checks" concurrent:"ok"`
	PGODebug              int    `help:"debug profile-guided optimizations"`
	PGOHash               string `help:"hash value for debugging profile-guided optimizations" concurrent:"ok"`
	PGOInline             int    `help:"enable profile-guided inlining" concurrent:"ok"`
	PGOInlineCDFThreshold string `help:"cumulative threshold percentage for determining call sites as hot candidates for inlining" concurrent:"ok"`
	PGOInlineBudget       int    `help:"inline budget for hot functions" concurrent:"ok"`
	PGODevirtualize       int    `help:"enable profile-guided devirtualization; 0 to disable, 1 to enable interface devirtualization, 2 to enable function devirtualization" concurrent:"ok"`
	RangeFuncCheck        int    `help:"insert code to check behavior of range iterator functions" concurrent:"ok"`
	VariableMakeHash      string `help:"hash value for debugging stack allocation of variable-sized make results" concurrent:"ok"`
	VariableMakeThreshold int    `help:"threshold in bytes for possible stack allocation of variable-sized make results" concurrent:"ok"`
	WrapGlobalMapDbg      int    `help:"debug trace output for global map init wrapping"`
	WrapGlobalMapCtl      int    `help:"global map init wrap control (0 => default, 1 => off, 2 => stress mode, no size cutoff)" concurrent:"ok"`
	ZeroCopy              int    `help:"enable zero-copy string->[]byte conversions" concurrent:"ok"`

	ConcurrentOk bool // true if only concurrentOk flags seen
}
