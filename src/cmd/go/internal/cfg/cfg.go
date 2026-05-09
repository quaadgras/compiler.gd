// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cfg holds configuration shared by multiple parts
// of the go command.
package cfg

import (
	"bytes"
	"context"
	"fmt"
	"go/build"
	"internal/buildcfg"
	"internal/cfg"
	"internal/platform"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"cmd/go/internal/fsys"
	"cmd/internal/pathcache"
	"cmd/internal/stdembed"
)

// Global build parameters (used during package load)
var (
	Goos   = envOr("GOOS", build.Default.GOOS)
	Goarch = envOr("GOARCH", build.Default.GOARCH)

	ExeSuffix = exeSuffix()

	// ModulesEnabled specifies whether the go command is running
	// in module-aware mode (as opposed to GOPATH mode).
	// It is equal to modload.Enabled, but not all packages can import modload.
	ModulesEnabled bool
)

func exeSuffix() string {
	if Goos == "windows" {
		return ".exe"
	}
	return ""
}

// Configuration for tools installed to GOROOT/bin.
// Normally these match runtime.GOOS and runtime.GOARCH,
// but when testing a cross-compiled cmd/go they will
// indicate the GOOS and GOARCH of the installed cmd/go
// rather than the test binary.
var (
	installedGOOS   string
	installedGOARCH string
)

// ToolExeSuffix returns the suffix for executables installed
// in build.ToolDir.
func ToolExeSuffix() string {
	if installedGOOS == "windows" {
		return ".exe"
	}
	return ""
}

// These are general "build flags" used by build and other commands.
var (
	BuildA                 bool     // -a flag
	BuildBuildmode         string   // -buildmode flag
	BuildBuildvcs          = "auto" // -buildvcs flag: "true", "false", or "auto"
	BuildContext           = defaultContext()
	BuildMod               string                  // -mod flag
	BuildModExplicit       bool                    // whether -mod was set explicitly
	BuildModReason         string                  // reason -mod was set, if set by default
	BuildLinkshared        bool                    // -linkshared flag
	BuildMSan              bool                    // -msan flag
	BuildASan              bool                    // -asan flag
	BuildCover             bool                    // -cover flag
	BuildCoverMode         string                  // -covermode flag
	BuildCoverPkg          []string                // -coverpkg flag
	BuildJSON              bool                    // -json flag
	BuildN                 bool                    // -n flag
	BuildO                 string                  // -o flag
	BuildP                 = runtime.GOMAXPROCS(0) // -p flag
	BuildPGO               string                  // -pgo flag
	BuildPkgdir            string                  // -pkgdir flag
	BuildRace              bool                    // -race flag
	BuildToolexec          []string                // -toolexec flag
	BuildToolchainName     string
	BuildToolchainCompiler func() string
	BuildToolchainLinker   func() string
	BuildTrimpath          bool // -trimpath flag
	BuildV                 bool // -v flag
	BuildWork              bool // -work flag
	BuildX                 bool // -x flag

	ModCacheRW bool   // -modcacherw flag
	ModFile    string // -modfile flag

	CmdName string // "build", "install", "list", "mod tidy", etc.

	DebugActiongraph  string // -debug-actiongraph flag (undocumented, unstable)
	DebugTrace        string // -debug-trace flag
	DebugRuntimeTrace string // -debug-runtime-trace flag (undocumented, unstable)

	// GoPathError is set when GOPATH is not set. it contains an
	// explanation why GOPATH is unset.
	GoPathError   string
	GOPATHChanged bool
	CGOChanged    bool
)

func defaultContext() build.Context {
	ctxt := build.Default

	ctxt.JoinPath = filepath.Join // back door to say "do not use go command"

	// Override defaults computed in go/build with defaults
	// from go environment configuration file, if known.
	ctxt.GOPATH, GOPATHChanged = EnvOrAndChanged("GOPATH", gopath(ctxt))
	ctxt.GOOS = Goos
	ctxt.GOARCH = Goarch

	// Clear the GOEXPERIMENT-based tool tags, which we will recompute later.
	var save []string
	for _, tag := range ctxt.ToolTags {
		if !strings.HasPrefix(tag, "goexperiment.") {
			save = append(save, tag)
		}
	}
	ctxt.ToolTags = save

	// The go/build rule for whether cgo is enabled is:
	//  1. If $CGO_ENABLED is set, respect it.
	//  2. Otherwise, if this is a cross-compile, disable cgo.
	//  3. Otherwise, use built-in default for GOOS/GOARCH.
	//
	// Recreate that logic here with the new GOOS/GOARCH setting.
	// We need to run steps 2 and 3 to determine what the default value
	// of CgoEnabled would be for computing CGOChanged.
	defaultCgoEnabled := false
	if buildcfg.DefaultCGO_ENABLED == "1" {
		defaultCgoEnabled = true
	} else if buildcfg.DefaultCGO_ENABLED == "0" {
	} else if runtime.GOARCH == ctxt.GOARCH && runtime.GOOS == ctxt.GOOS {
		defaultCgoEnabled = platform.CgoSupported(ctxt.GOOS, ctxt.GOARCH)
		// Use built-in default cgo setting for GOOS/GOARCH.
		// Note that ctxt.GOOS/GOARCH are derived from the preference list
		// (1) environment, (2) go/env file, (3) runtime constants,
		// while go/build.Default.GOOS/GOARCH are derived from the preference list
		// (1) environment, (2) runtime constants.
		//
		// We know ctxt.GOOS/GOARCH == runtime.GOOS/GOARCH;
		// no matter how that happened, go/build.Default will make the
		// same decision (either the environment variables are set explicitly
		// to match the runtime constants, or else they are unset, in which
		// case go/build falls back to the runtime constants), so
		// go/build.Default.GOOS/GOARCH == runtime.GOOS/GOARCH.
		// So ctxt.CgoEnabled (== go/build.Default.CgoEnabled) is correct
		// as is and can be left unmodified.
		//
		// All that said, starting in Go 1.20 we layer one more rule
		// on top of the go/build decision: if CC is unset and
		// the default C compiler we'd look for is not in the PATH,
		// we automatically default cgo to off.
		// This makes go builds work automatically on systems
		// without a C compiler installed.
		if ctxt.CgoEnabled {
			if os.Getenv("CC") == "" {
				cc := DefaultCC(ctxt.GOOS, ctxt.GOARCH)
				if _, err := pathcache.LookPath(cc); err != nil {
					defaultCgoEnabled = false
				}
			}
		}
	}
	ctxt.CgoEnabled = defaultCgoEnabled
	if v := Getenv("CGO_ENABLED"); v == "0" || v == "1" {
		ctxt.CgoEnabled = v[0] == '1'
	}
	CGOChanged = ctxt.CgoEnabled != defaultCgoEnabled

	ctxt.OpenFile = func(path string) (io.ReadCloser, error) {
		return fsys.Open(path)
	}
	ctxt.ReadDir = func(path string) ([]fs.FileInfo, error) {
		// Convert []fs.DirEntry to []fs.FileInfo using dirInfo.
		dirs, err := fsys.ReadDir(path)
		infos := make([]fs.FileInfo, len(dirs))
		for i, dir := range dirs {
			infos[i] = &dirInfo{dir}
		}
		return infos, err
	}
	ctxt.IsDir = func(path string) bool {
		isDir, err := fsys.IsDir(path)
		return err == nil && isDir
	}

	return ctxt
}

// IsGdRuntime reports whether bin/go was built by the gd fork
// toolchain. We check runtime.Version() rather than runtime.Compiler
// because the fork reuses the gc backend (Compiler stays "gc") but
// stamps a "gd"-prefixed version string into the binary at link
// time. Compiler != "gd" was leaving the gd-only init paths dormant
// in fork-built binaries.
func IsGdRuntime() bool {
	return strings.HasPrefix(runtime.Version(), "gd")
}

func init() {
	SetGOROOT(Getenv("GOROOT"), false)
	// initGDPATH first so initGDROOT can fall back to $GDPATH/std,
	// the embedded-stdlib materialisation path. The materialisation
	// itself happens here too (idempotent — only extracts when the
	// embedded archive's hash differs from $GDPATH/std/.embed-hash).
	initGDPATH()
	if GDPATH != "" {
		// Best-effort. If the user has no writable $GDPATH, leave
		// GDROOT to fall through to GOROOT below; we only succeed
		// at materialise when bin/go was built with an actual
		// archive (cmd/dist's mkstdembed step) and $GDPATH is
		// writable.
		_ = stdembed.Materialize(filepath.Join(GDPATH, "std"))
	}
	initGDROOT()
}

// initGDROOT resolves GDROOT for the gd toolchain. It checks $GDROOT,
// then the install path of the running binary, then $GDPATH/std (the
// embedded-stdlib materialisation path), and finally GOROOT (used
// during make.bash bootstrap where both roots are the same tree).
func initGDROOT() {
	if !IsGdRuntime() {
		return
	}
	if env := os.Getenv("GDROOT"); env != "" {
		GDROOT = filepath.Clean(env)
		return
	}
	if exe, err := os.Executable(); err == nil {
		// Binary is at $GDROOT/bin/go; install is two dirs up.
		candidate := filepath.Clean(filepath.Dir(filepath.Dir(exe)))
		if fi, err := os.Stat(filepath.Join(candidate, "src", "runtime")); err == nil && fi.IsDir() {
			GDROOT = candidate
			return
		}
	}
	// Try $GDPATH/std — the embedded-stdlib materialisation path.
	// initGDPATH runs before initGDROOT (see Go's package init order;
	// initGDPATH is called from the same Init function below), so
	// GDPATH is already resolved here.
	if GDPATH != "" {
		std := filepath.Join(GDPATH, "std")
		if fi, err := os.Stat(filepath.Join(std, "src", "runtime")); err == nil && fi.IsDir() {
			GDROOT = std
			return
		}
	}
	GDROOT = GOROOT
}

// initGDPATH resolves GDPATH for the gd toolchain: $GDPATH, otherwise
// $HOME/gd (the gd analogue of GOPATH's ~/go default). Activated by
// -compiler=gd, which swaps BuildContext.GOPATH to this value.
func initGDPATH() {
	if !IsGdRuntime() {
		return
	}
	if env := os.Getenv("GDPATH"); env != "" {
		GDPATH = filepath.Clean(env)
		return
	}
	envVar := "HOME"
	if runtime.GOOS == "windows" {
		envVar = "USERPROFILE"
	} else if runtime.GOOS == "plan9" {
		envVar = "home"
	}
	if home := os.Getenv(envVar); home != "" {
		GDPATH = filepath.Join(home, "gd")
	}
}

// ForceHost forces GOOS and GOARCH to runtime.GOOS and runtime.GOARCH.
// This is used by go tool to build tools for the go command's own
// GOOS and GOARCH.
func ForceHost() {
	Goos = runtime.GOOS
	Goarch = runtime.GOARCH
	ExeSuffix = exeSuffix()
	GO386 = buildcfg.DefaultGO386
	GOAMD64 = buildcfg.DefaultGOAMD64
	GOARM = buildcfg.DefaultGOARM
	GOARM64 = buildcfg.DefaultGOARM64
	GOMIPS = buildcfg.DefaultGOMIPS
	GOMIPS64 = buildcfg.DefaultGOMIPS64
	GOPPC64 = buildcfg.DefaultGOPPC64
	GORISCV64 = buildcfg.DefaultGORISCV64
	GOWASM = ""

	// Recompute the build context using Goos and Goarch to
	// set the correct value for ctx.CgoEnabled.
	BuildContext = defaultContext()
	// Call SetGOROOT to properly set the GOROOT on the new context.
	SetGOROOT(Getenv("GOROOT"), false)
	// Recompute experiments: the settings determined depend on GOOS and GOARCH.
	// This will also update the BuildContext's tool tags to include the new
	// experiment tags.
	computeExperiment()
}

// SetGOROOT sets GOROOT and associated variables to the given values.
//
// If isTestGo is true, build.ToolDir is set based on the TESTGO_GOHOSTOS and
// TESTGO_GOHOSTARCH environment variables instead of runtime.GOOS and
// runtime.GOARCH.
func SetGOROOT(goroot string, isTestGo bool) {
	BuildContext.GOROOT = goroot

	GOROOT = goroot
	if goroot == "" {
		GOROOTbin = ""
		GOROOTpkg = ""
		GOROOTsrc = ""
	} else {
		GOROOTbin = filepath.Join(goroot, "bin")
		GOROOTpkg = filepath.Join(goroot, "pkg")
		GOROOTsrc = filepath.Join(goroot, "src")
	}

	installedGOOS = runtime.GOOS
	installedGOARCH = runtime.GOARCH
	if isTestGo {
		if testOS := os.Getenv("TESTGO_GOHOSTOS"); testOS != "" {
			installedGOOS = testOS
		}
		if testArch := os.Getenv("TESTGO_GOHOSTARCH"); testArch != "" {
			installedGOARCH = testArch
		}
	}

	if runtime.Compiler != "gccgo" {
		if goroot == "" {
			build.ToolDir = ""
		} else {
			// Note that we must use the installed OS and arch here: the tool
			// directory does not move based on environment variables, and even if we
			// are testing a cross-compiled cmd/go all of the installed packages and
			// tools would have been built using the native compiler and linker (and
			// would spuriously appear stale if we used a cross-compiled compiler and
			// linker).
			//
			// This matches the initialization of ToolDir in go/build, except for
			// using ctxt.GOROOT and the installed GOOS and GOARCH rather than the
			// GOROOT, GOOS, and GOARCH reported by the runtime package.
			build.ToolDir = filepath.Join(GOROOTpkg, "tool", installedGOOS+"_"+installedGOARCH)
		}
	}
}

// Experiment configuration.
var (
	// RawGOEXPERIMENT is the GOEXPERIMENT value set by the user.
	RawGOEXPERIMENT = envOr("GOEXPERIMENT", buildcfg.DefaultGOEXPERIMENT)
	// CleanGOEXPERIMENT is the minimal GOEXPERIMENT value needed to reproduce the
	// experiments enabled by RawGOEXPERIMENT.
	CleanGOEXPERIMENT = RawGOEXPERIMENT

	Experiment    *buildcfg.ExperimentFlags
	ExperimentErr error
)

func init() {
	computeExperiment()
}

func computeExperiment() {
	Experiment, ExperimentErr = buildcfg.ParseGOEXPERIMENT(Goos, Goarch, RawGOEXPERIMENT)
	if ExperimentErr != nil {
		return
	}

	// GOEXPERIMENT is valid, so convert it to canonical form.
	CleanGOEXPERIMENT = Experiment.String()

	// Add build tags based on the experiments in effect.
	exps := Experiment.Enabled()
	expTags := make([]string, 0, len(exps)+len(BuildContext.ToolTags))
	for _, exp := range exps {
		expTags = append(expTags, "goexperiment."+exp)
	}
	BuildContext.ToolTags = append(expTags, BuildContext.ToolTags...)
}

// An EnvVar is an environment variable Name=Value.
type EnvVar struct {
	Name    string
	Value   string
	Changed bool // effective Value differs from default
}

// OrigEnv is the original environment of the program at startup.
var OrigEnv []string

// CmdEnv is the new environment for running go tool commands.
// User binaries (during go test or go run) are run with OrigEnv,
// not CmdEnv.
var CmdEnv []EnvVar

var envCache struct {
	once   sync.Once
	m      map[string]string
	goroot map[string]string
}

// EnvFile returns the name of the Go environment configuration file,
// and reports whether the effective value differs from the default.
func EnvFile() (string, bool, error) {
	if file := os.Getenv("GOENV"); file != "" {
		if file == "off" {
			return "", false, fmt.Errorf("GOENV=off")
		}
		return file, true, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", false, err
	}
	if dir == "" {
		return "", false, fmt.Errorf("missing user-config dir")
	}
	return filepath.Join(dir, "go/env"), false, nil
}

func initEnvCache() {
	envCache.m = make(map[string]string)
	envCache.goroot = make(map[string]string)
	if file, _, _ := EnvFile(); file != "" {
		readEnvFile(file, "user")
	}
	goroot := findGOROOT(envCache.m["GOROOT"])
	if goroot != "" {
		readEnvFile(filepath.Join(goroot, "go.env"), "GOROOT")
	}

	// Save the goroot for func init calling SetGOROOT,
	// and also overwrite anything that might have been in go.env.
	// It makes no sense for GOROOT/go.env to specify
	// a different GOROOT.
	envCache.m["GOROOT"] = goroot
}

func readEnvFile(file string, source string) {
	if file == "" {
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}

	for len(data) > 0 {
		// Get next line.
		line := data
		i := bytes.IndexByte(data, '\n')
		if i >= 0 {
			line, data = line[:i], data[i+1:]
		} else {
			data = nil
		}

		i = bytes.IndexByte(line, '=')
		if i < 0 || line[0] < 'A' || 'Z' < line[0] {
			// Line is missing = (or empty) or a comment or not a valid env name. Ignore.
			// This should not happen in the user file, since the file should be maintained almost
			// exclusively by "go env -w", but better to silently ignore than to make
			// the go command unusable just because somehow the env file has
			// gotten corrupted.
			// In the GOROOT/go.env file, we expect comments.
			continue
		}
		key, val := line[:i], line[i+1:]

		if source == "GOROOT" {
			envCache.goroot[string(key)] = string(val)
			// In the GOROOT/go.env file, do not overwrite fields loaded from the user's go/env file.
			if _, ok := envCache.m[string(key)]; ok {
				continue
			}
		}
		envCache.m[string(key)] = string(val)
	}
}

// Getenv gets the value for the configuration key.
// It consults the operating system environment
// and then the go/env file.
// If Getenv is called for a key that cannot be set
// in the go/env file (for example GODEBUG), it panics.
// This ensures that CanGetenv is accurate, so that
// 'go env -w' stays in sync with what Getenv can retrieve.
func Getenv(key string) string {
	if !CanGetenv(key) {
		switch key {
		case "CGO_TEST_ALLOW", "CGO_TEST_DISALLOW", "CGO_test_ALLOW", "CGO_test_DISALLOW":
			// used by internal/work/security_test.go; allow
		default:
			panic("internal error: invalid Getenv " + key)
		}
	}
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	envCache.once.Do(initEnvCache)
	return envCache.m[key]
}

// CanGetenv reports whether key is a valid go/env configuration key.
func CanGetenv(key string) bool {
	envCache.once.Do(initEnvCache)
	if _, ok := envCache.m[key]; ok {
		// Assume anything in the user file or go.env file is valid.
		return true
	}
	return strings.Contains(cfg.KnownEnv, "\t"+key+"\n")
}

var (
	GOROOT string

	// GDROOT is the install prefix of the gd toolchain, used when
	// -compiler=gd swaps GOROOT to the gd install. Only meaningful when
	// runtime.Compiler == "gd"; empty otherwise. Resolved from $GDROOT,
	// the running binary's install path, or GOROOT as a last resort.
	GDROOT string

	// GDPATH is the user workspace for the gd toolchain, swapped into
	// BuildContext.GOPATH by -compiler=gd. Only meaningful when
	// runtime.Compiler == "gd"; empty otherwise. Defaults to $GDPATH,
	// otherwise $HOME/gd.
	GDPATH string

	// Either empty or produced by filepath.Join(GOROOT, …).
	GOROOTbin string
	GOROOTpkg string
	GOROOTsrc string

	GOBIN                           = Getenv("GOBIN")
	GOCACHEPROG, GOCACHEPROGChanged = EnvOrAndChanged("GOCACHEPROG", "")
	GOMODCACHE, GOMODCACHEChanged   = EnvOrAndChanged("GOMODCACHE", gopathDir("pkg/mod"))

	// Used in envcmd.MkEnv and build ID computations.
	GOARM64, goARM64Changed     = EnvOrAndChanged("GOARM64", buildcfg.DefaultGOARM64)
	GOARM, goARMChanged         = EnvOrAndChanged("GOARM", buildcfg.DefaultGOARM)
	GO386, go386Changed         = EnvOrAndChanged("GO386", buildcfg.DefaultGO386)
	GOAMD64, goAMD64Changed     = EnvOrAndChanged("GOAMD64", buildcfg.DefaultGOAMD64)
	GOMIPS, goMIPSChanged       = EnvOrAndChanged("GOMIPS", buildcfg.DefaultGOMIPS)
	GOMIPS64, goMIPS64Changed   = EnvOrAndChanged("GOMIPS64", buildcfg.DefaultGOMIPS64)
	GOPPC64, goPPC64Changed     = EnvOrAndChanged("GOPPC64", buildcfg.DefaultGOPPC64)
	GORISCV64, goRISCV64Changed = EnvOrAndChanged("GORISCV64", buildcfg.DefaultGORISCV64)
	GOWASM, goWASMChanged       = EnvOrAndChanged("GOWASM", fmt.Sprint(buildcfg.GOWASM))

	GOFIPS140, GOFIPS140Changed = EnvOrAndChanged("GOFIPS140", buildcfg.DefaultGOFIPS140)
	GOPROXY, GOPROXYChanged     = EnvOrAndChanged("GOPROXY", "")
	GOSUMDB, GOSUMDBChanged     = EnvOrAndChanged("GOSUMDB", "")
	GOPRIVATE                   = Getenv("GOPRIVATE")
	GONOPROXY, GONOPROXYChanged = EnvOrAndChanged("GONOPROXY", GOPRIVATE)
	GONOSUMDB, GONOSUMDBChanged = EnvOrAndChanged("GONOSUMDB", GOPRIVATE)
	GOINSECURE                  = Getenv("GOINSECURE")
	GOVCS                       = Getenv("GOVCS")
	GOAUTH, GOAUTHChanged       = EnvOrAndChanged("GOAUTH", "netrc")
)

// EnvOrAndChanged returns the environment variable value
// and reports whether it differs from the default value.
func EnvOrAndChanged(name, def string) (v string, changed bool) {
	val := Getenv(name)
	if val != "" {
		v = val
		if g, ok := envCache.goroot[name]; ok {
			changed = val != g
		} else {
			changed = val != def
		}
		return v, changed
	}
	return def, false
}

var SumdbDir = gopathDir("pkg/sumdb")

// GetArchEnv returns the name and setting of the
// GOARCH-specific architecture environment variable.
// If the current architecture has no GOARCH-specific variable,
// GetArchEnv returns empty key and value.
func GetArchEnv() (key, val string, changed bool) {
	switch Goarch {
	case "arm":
		return "GOARM", GOARM, goARMChanged
	case "arm64":
		return "GOARM64", GOARM64, goARM64Changed
	case "386":
		return "GO386", GO386, go386Changed
	case "amd64":
		return "GOAMD64", GOAMD64, goAMD64Changed
	case "mips", "mipsle":
		return "GOMIPS", GOMIPS, goMIPSChanged
	case "mips64", "mips64le":
		return "GOMIPS64", GOMIPS64, goMIPS64Changed
	case "ppc64", "ppc64le":
		return "GOPPC64", GOPPC64, goPPC64Changed
	case "riscv64":
		return "GORISCV64", GORISCV64, goRISCV64Changed
	case "wasm":
		return "GOWASM", GOWASM, goWASMChanged
	}
	return "", "", false
}

// envOr returns Getenv(key) if set, or else def.
func envOr(key, def string) string {
	val := Getenv(key)
	if val == "" {
		val = def
	}
	return val
}

// There is a copy of findGOROOT, isSameDir, and isGOROOT in
// x/tools/cmd/godoc/goroot.go.
// Try to keep them in sync for now.

// findGOROOT returns the GOROOT value, using either an explicitly
// provided environment variable, a GOROOT that contains the current
// os.Executable value, or else the GOROOT that the binary was built
// with from runtime.GOROOT().
//
// There is a copy of this code in x/tools/cmd/godoc/goroot.go.
func findGOROOT(env string) string {
	if env == "" {
		// Not using Getenv because findGOROOT is called
		// to find the GOROOT/go.env file. initEnvCache
		// has passed in the setting from the user go/env file.
		env = os.Getenv("GOROOT")
	}
	if env != "" {
		return filepath.Clean(env)
	}
	def := ""
	if r := runtime.GOROOT(); r != "" {
		def = filepath.Clean(r)
	}
	if runtime.Compiler == "gccgo" {
		// gccgo has no real GOROOT, and it certainly doesn't
		// depend on the executable's location.
		return def
	}

	// canonical returns a directory path that represents
	// the same directory as dir,
	// preferring the spelling in def if the two are the same.
	canonical := func(dir string) string {
		if isSameDir(def, dir) {
			return def
		}
		return dir
	}

	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.Abs(exe)
		if err == nil {
			// cmd/go may be installed in GOROOT/bin or GOROOT/bin/GOOS_GOARCH,
			// depending on whether it was cross-compiled with a different
			// GOHOSTOS (see https://go.dev/issue/62119). Try both.
			if dir := filepath.Join(exe, "../.."); isGOROOT(dir) {
				return canonical(dir)
			}
			if dir := filepath.Join(exe, "../../.."); isGOROOT(dir) {
				return canonical(dir)
			}

			// Depending on what was passed on the command line, it is possible
			// that os.Executable is a symlink (like /usr/local/bin/go) referring
			// to a binary installed in a real GOROOT elsewhere
			// (like /usr/lib/go/bin/go).
			// Try to find that GOROOT by resolving the symlinks.
			exe, err = filepath.EvalSymlinks(exe)
			if err == nil {
				if dir := filepath.Join(exe, "../.."); isGOROOT(dir) {
					return canonical(dir)
				}
				if dir := filepath.Join(exe, "../../.."); isGOROOT(dir) {
					return canonical(dir)
				}
			}
		}
	}
	return def
}

// isSameDir reports whether dir1 and dir2 are the same directory.
func isSameDir(dir1, dir2 string) bool {
	if dir1 == dir2 {
		return true
	}
	info1, err1 := os.Stat(dir1)
	info2, err2 := os.Stat(dir2)
	return err1 == nil && err2 == nil && os.SameFile(info1, info2)
}

// isGOROOT reports whether path looks like a GOROOT.
//
// It does this by looking for the path/pkg/tool directory,
// which is necessary for useful operation of the cmd/go tool,
// and is not typically present in a GOPATH.
//
// There is a copy of this code in x/tools/cmd/godoc/goroot.go.
func isGOROOT(path string) bool {
	stat, err := os.Stat(filepath.Join(path, "pkg", "tool"))
	if err != nil {
		return false
	}
	return stat.IsDir()
}

func gopathDir(rel string) string {
	list := filepath.SplitList(BuildContext.GOPATH)
	if len(list) == 0 || list[0] == "" {
		return ""
	}
	return filepath.Join(list[0], rel)
}

// Keep consistent with go/build.defaultGOPATH.
func gopath(ctxt build.Context) string {
	if len(ctxt.GOPATH) > 0 {
		return ctxt.GOPATH
	}
	env := "HOME"
	if runtime.GOOS == "windows" {
		env = "USERPROFILE"
	} else if runtime.GOOS == "plan9" {
		env = "home"
	}
	if home := os.Getenv(env); home != "" {
		def := filepath.Join(home, "go")
		if filepath.Clean(def) == filepath.Clean(runtime.GOROOT()) {
			GoPathError = "cannot set GOROOT as GOPATH"
		}
		return ""
	}
	GoPathError = fmt.Sprintf("%s is not set", env)
	return ""
}

// WithBuildXWriter returns a Context in which BuildX output is written
// to given io.Writer.
func WithBuildXWriter(ctx context.Context, xLog io.Writer) context.Context {
	return context.WithValue(ctx, buildXContextKey{}, xLog)
}

type buildXContextKey struct{}

// BuildXWriter returns nil if BuildX is false, or
// the writer to which BuildX output should be written otherwise.
func BuildXWriter(ctx context.Context) (io.Writer, bool) {
	if !BuildX {
		return nil, false
	}
	if v := ctx.Value(buildXContextKey{}); v != nil {
		return v.(io.Writer), true
	}
	return os.Stderr, true
}

// A dirInfo implements fs.FileInfo from fs.DirEntry.
// We know that go/build doesn't use the non-DirEntry parts,
// so we can panic instead of doing difficult work.
type dirInfo struct {
	dir fs.DirEntry
}

func (d *dirInfo) Name() string      { return d.dir.Name() }
func (d *dirInfo) IsDir() bool       { return d.dir.IsDir() }
func (d *dirInfo) Mode() fs.FileMode { return d.dir.Type() }

func (d *dirInfo) Size() int64        { panic("dirInfo.Size") }
func (d *dirInfo) ModTime() time.Time { panic("dirInfo.ModTime") }
func (d *dirInfo) Sys() any           { panic("dirInfo.Sys") }
