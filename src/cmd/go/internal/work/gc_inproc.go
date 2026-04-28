// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"cmd/compile/host"
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/str"
	"fmt"
	"internal/buildcfg"
	"os"
	"path/filepath"
	"strings"
)

// useInProcessCompile reports whether cmd/go should drive
// cmd/compile in-process (default) or fork/exec it as a subprocess.
// Set GOGD_INPROC=0 to force fork/exec — useful for debugging or
// when toolexec wrapping is needed.
//
// Default-on saves the per-package fork/exec cost; the compile is
// statically linked into bin/go via cmd/compile/host.
//
// During all.bash bootstrap, dist drives toolchain2/toolchain3 install
// via the toolchain1-built go_bootstrap binary. In those phases
// pkg/tool/compile is replaced (toolchain2's, then toolchain3's) but
// go_bootstrap's embedded compile is still toolchain1's source/binary.
// cmd/go's cache keys are computed from pkg/tool/compile, so taking
// the in-process path then produces artifacts whose embedded build ID
// won't match the one cmd/go expects to read back, and dist's
// checkNotStale fails. Detect go_bootstrap by argv0 basename and
// force fork/exec for that case.
func useInProcessCompile() bool {
	return os.Getenv("GOGD_INPROC") != "0" && !isBootstrapDriver()
}

// canRunInProcess reports whether cmd/go can safely drive
// compile/asm/link in-process for an invocation whose env is `env`.
// Refuses cross-builds because internal/buildcfg.GOARCH/GOOS are
// process-globals and the in-process tool can't see env's override.
func canRunInProcess(env []string) bool {
	return !isCrossBuild(env)
}

// useInProcessLink reports whether cmd/go should drive cmd/link
// in-process. Default-on; set GOGD_INPROC_LINK=0 to force fork/exec.
// Same bootstrap-driver guard as useInProcessCompile.
func useInProcessLink() bool {
	return os.Getenv("GOGD_INPROC_LINK") != "0" && !isBootstrapDriver()
}

// isBootstrapDriver reports whether this cmd/go binary was launched
// as go_bootstrap (the toolchain1-built cmd/go used by dist for
// toolchain2/toolchain3 install phases). The in-process compile/link
// paths are unsafe in those phases — see useInProcessCompile.
func isBootstrapDriver() bool {
	return filepath.Base(os.Args[0]) == "go_bootstrap"
}

// isCrossBuild reports whether cmd/go's resolved target GOOS/GOARCH
// (cfg.Goos/Goarch — may have come from a GOENV file, env vars, or
// build defaults) differs from this process's buildcfg (which is
// process-global, set at package init from the launching env, and
// immutable thereafter). In-process compile/asm/link share buildcfg
// with cmd/go and would emit host-arch artefacts despite the
// caller's cross-target intent; fall back to fork/exec so the
// subprocess re-reads buildcfg from its own env / GOENV.
//
// Comparing cfg vs buildcfg directly (instead of scanning the
// per-call env slice cfgChangedEnv) catches GOENV-driven cross
// builds, where cfg.Getenv chases the GOENV file and matches
// cfg.Goarch — so makeCfgChangedEnv adds nothing, but the in-
// process tool still has the host arch.
func isCrossBuild(env []string) bool {
	_ = env
	return cfg.Goarch != buildcfg.GOARCH || cfg.Goos != buildcfg.GOOS
}

// inProcessCompile drives a single cmd/compile invocation in the
// calling process via cmd/compile/host.Run, mirroring the
// observable behaviour of (*Shell).runOut. cmd/go calls this in
// gc.go when no toolexec wrapper is configured and -n is off.
//
// The args slice has the shape `[toolexec...] base.Tool("compile") -o <ofile> ...flags... files...`
// (the same shape sh.runOut would have received). We strip everything
// up to and including the compile tool path so host.Run sees the
// argv slice the standalone compile binary would have received as
// os.Args[1:].
//
// stdout+stderr from the compile are captured into a single buffer
// (matching runOut's combined-output behaviour) and returned. Errors
// from the compile (status != 0) are surfaced as a non-nil error
// formatted like exec.Cmd's *ExitError, so callers in build.go can
// pattern-match on the message identically to the subprocess path.
//
// If anything in args looks "exotic" (response file, unknown shape),
// inProcessCompile bails to runOut. This keeps the in-process path
// off the hot path only when we can prove it's safe to take it.
func inProcessCompile(sh *Shell, dir string, env []string, args []any) ([]byte, error) {
	cmdline := str.StringList(args...)

	// Mirror Shell.runOut: reject @-prefixed args before we hand
	// them off. GNU binutils interpret @foo as a response file and
	// fork upstreams the rejection on the subprocess path; the
	// in-process path bypasses runOut so we re-implement the check
	// here. TestBadCommandLines depends on this rejection happening
	// even when no actual exec occurs.
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "@") {
			return nil, fmt.Errorf("invalid command-line argument %s in command: %s", arg, joinUnambiguously(cmdline))
		}
	}

	// Locate the compile tool in cmdline. cmd/go always emits it
	// at position 0 unless -toolexec was set; we already gated that
	// off in the caller. Defensive scan in case future code paths
	// prepend other tokens.
	compileTool := base.Tool("compile")
	idx := -1
	for i, a := range cmdline {
		if a == compileTool {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Should not happen on the gc.go path, but fall back to
		// the subprocess if it ever does.
		return sh.runOut(dir, env, args...)
	}

	if cfg.BuildX {
		envcmdline := ""
		for _, e := range env {
			if j := strings.IndexByte(e, '='); j != -1 {
				if strings.ContainsRune(e[j+1:], '\'') {
					envcmdline += fmt.Sprintf("%s=%q ", e[:j], e[j+1:])
				} else {
					envcmdline += fmt.Sprintf("%s='%s' ", e[:j], e[j+1:])
				}
			}
		}
		envcmdline += joinUnambiguously(cmdline)
		sh.ShowCmd(dir, "%s", envcmdline)
	}

	// Capture diagnostics into a per-Invocation buffer so cmd/go's
	// reportCmd can post-process (cgo type-name regex translation,
	// trimming \[…cgo1.go:N:M\] annotations, capturing into JSON
	// build-output for `go test -json`). cmd/compile/host.Run plumbs
	// stderr to base.Invocation.Stderr; FlushErrors / FatalfAt write
	// there. Stdout gets a buffer too in case future output is added,
	// but compile is silent on stdout today.
	var outBuf, errBuf bytes.Buffer
	status, runErr := host.Run(cmdline[idx+1:], &outBuf, &errBuf)

	out := append(outBuf.Bytes(), errBuf.Bytes()...)
	if runErr != nil {
		return out, runErr
	}
	if status != 0 {
		// Match exec.Cmd's *ExitError formatting for downstream
		// consumers in build.go that look for "exit status N".
		return out, fmt.Errorf("exit status %d", status)
	}
	return out, nil
}
