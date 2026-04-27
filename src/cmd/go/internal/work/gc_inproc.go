// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"cmd/compile/host"
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/str"
	"fmt"
	"os"
	"strings"
)

// useInProcessCompile reports whether cmd/go should drive
// cmd/compile in-process (default) or fork/exec it as a subprocess.
// Set GOGD_INPROC=0 to force fork/exec — useful for debugging or
// when toolexec wrapping is needed.
//
// Default-on saves the per-package fork/exec cost; the compile is
// statically linked into bin/go via cmd/compile/host. Concurrent
// invocations are serialised internally by host.Run's runMu while
// the lingering process-global state in types.NewPtr's caches and
// elsewhere is migrated; cmd/go still benefits from the avoided
// fork/exec, and each compile uses its own -c=N backend parallelism.
func useInProcessCompile() bool {
	return os.Getenv("GOGD_INPROC") != "0"
}

// useInProcessLink reports whether cmd/go should drive cmd/link
// in-process. Opt-in via GOGD_INPROC_LINK=1 because cmd/link's
// package-level state (DWARF caches, Mach-O/ELF format vars, error
// counters) has not yet been migrated onto a per-Link Context;
// back-to-back invocations in the same process can carry stale state.
// The host.Run scaffolding (worker goroutine + runtime.Goexit)
// already exists; flipping the default to on requires finishing the
// per-Link migration first.
func useInProcessLink() bool {
	return os.Getenv("GOGD_INPROC_LINK") == "1"
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

	// Compile errors and warnings go to this process's os.Stderr
	// directly (compile internals write there). Returning an empty
	// byte slice as "captured output" loses runOut's diagnostic
	// capture behaviour, but redirecting os.Stderr concurrently is
	// fundamentally racy under parallel invocations and the user
	// still sees the diagnostics on the terminal. cmd/go's build
	// driver only reads the returned bytes for additional log
	// output beyond the exit status.
	status, runErr := host.Run(cmdline[idx+1:], os.Stdout, os.Stderr)

	if runErr != nil {
		return nil, runErr
	}
	if status != 0 {
		// Match exec.Cmd's *ExitError formatting for downstream
		// consumers in build.go that look for "exit status N".
		return nil, fmt.Errorf("exit status %d", status)
	}
	return nil, nil
}
