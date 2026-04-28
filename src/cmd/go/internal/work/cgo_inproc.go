// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"cmd/cgo/host"
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/str"
	"fmt"
	"os"
	"strings"
)

// useInProcessCgo reports whether cmd/go should drive cmd/cgo
// in-process (default) or fork/exec it as a subprocess. Set
// GOGD_INPROC_CGO=0 to force fork/exec.
//
// Same bootstrap-driver guard as useInProcessCompile/Link.
func useInProcessCgo() bool {
	return os.Getenv("GOGD_INPROC_CGO") != "0" && !isBootstrapDriver()
}

// inProcessCgo drives a single cmd/cgo invocation in the calling
// process via cmd/cgo/host.Run, mirroring sh.run's behaviour for
// the cgo command line. cmd/go calls this from buildCgo when
// no -toolexec wrapper is configured, -n is off, and we're not
// cross-building.
//
// args has the shape `[toolexec...] base.Tool("cgo") <flags...>`
// (the same shape sh.run would have received). We strip everything
// up to and including the cgo tool path so host.Run sees the argv
// slice the standalone cgo binary would have received as
// os.Args[1:].
//
// host.Run serialises invocations under runMu while cmd/cgo's
// package-level state (flag.* pointers, fset, typedef map, ...)
// is migrated piecemeal to per-Invocation. The reset hook in
// cgomain.resetState clears state between calls.
func inProcessCgo(sh *Shell, dir, desc string, env []string, args []any) error {
	cmdline := str.StringList(args...)

	// Mirror Shell.runOut: reject @-prefixed args before we hand
	// them off. The subprocess path checks this in runOut; the
	// in-process path bypasses runOut so re-implement it here.
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "@") {
			return fmt.Errorf("invalid command-line argument %s in command: %s", arg, joinUnambiguously(cmdline))
		}
	}

	cgoTool := base.Tool("cgo")
	idx := -1
	for i, a := range cmdline {
		if a == cgoTool {
			idx = i
			break
		}
	}
	if idx < 0 {
		return sh.run(dir, desc, env, args...)
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

	// Restore CWD around the call: cgomain reads -srcdir relative
	// to the working directory and may chdir internally.
	savedCwd, _ := os.Getwd()
	if dir != "" && dir != "." {
		if err := os.Chdir(dir); err != nil {
			return err
		}
		defer os.Chdir(savedCwd)
	}

	// Capture link diagnostics into a single buffer and route
	// through sh.reportCmd, mirroring sh.run for subprocess cgo.
	var buf bytes.Buffer
	lw := &lockedWriter{w: &buf}
	status, runErr := host.Run(cmdline[idx+1:], lw, lw)
	out := buf.Bytes()

	if desc == "" {
		desc = sh.fmtCmd(dir, "%s", strings.Join(cmdline, " "))
	}
	var cmdErr error
	switch {
	case runErr != nil:
		cmdErr = fmt.Errorf("%s: %v", cgoTool, runErr)
	case status != 0:
		cmdErr = fmt.Errorf("%s: exit status %d", cgoTool, status)
	}
	return sh.reportCmd(desc, dir, out, cmdErr)
}

