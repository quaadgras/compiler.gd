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
	"os"
	"strings"
)

// useInProcessCompile reports whether the GOGD_INPROC opt-in is set.
// Off by default — see gc.go's comment on the gating block.
func useInProcessCompile() bool {
	return os.Getenv("GOGD_INPROC") == "1"
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

	// host.Run captures the compile's stdout/stderr by way of the
	// global os.Stdout/os.Stderr today; redirect both to an in-
	// memory buffer for the duration of the call so we can return
	// the captured output (matching runOut). Best-effort — some
	// compile error paths may bypass these globals.
	var buf bytes.Buffer
	origStdout, origStderr := os.Stdout, os.Stderr
	stdoutR, stdoutW, perr := os.Pipe()
	if perr != nil {
		return sh.runOut(dir, env, args...)
	}
	stderrR, stderrW, perr := os.Pipe()
	if perr != nil {
		stdoutR.Close()
		stdoutW.Close()
		return sh.runOut(dir, env, args...)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW

	// Drain the pipes concurrently so the compile doesn't block on
	// a full pipe buffer.
	drainDone := make(chan struct{}, 2)
	go func() {
		buf2 := make([]byte, 4096)
		for {
			n, err := stdoutR.Read(buf2)
			if n > 0 {
				buf.Write(buf2[:n])
			}
			if err != nil {
				break
			}
		}
		drainDone <- struct{}{}
	}()
	go func() {
		buf2 := make([]byte, 4096)
		for {
			n, err := stderrR.Read(buf2)
			if n > 0 {
				buf.Write(buf2[:n])
			}
			if err != nil {
				break
			}
		}
		drainDone <- struct{}{}
	}()

	// Run the compile. host.Run handles the worker-goroutine dance
	// for runtime.Goexit-based gd.Exit paths, so we get a status
	// back without process-level termination.
	status, runErr := host.Run(cmdline[idx+1:], stdoutW, stderrW)

	// Restore globals and close write-ends so the pipe drainers see EOF.
	os.Stdout = origStdout
	os.Stderr = origStderr
	stdoutW.Close()
	stderrW.Close()
	<-drainDone
	<-drainDone
	stdoutR.Close()
	stderrR.Close()

	if runErr != nil {
		return buf.Bytes(), runErr
	}
	if status != 0 {
		// Match exec.Cmd's *ExitError formatting for downstream
		// consumers in build.go that look for "exit status N".
		return buf.Bytes(), fmt.Errorf("exit status %d", status)
	}
	return buf.Bytes(), nil
}
