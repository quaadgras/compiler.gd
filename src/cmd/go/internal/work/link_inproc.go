// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/str"
	"cmd/link/host"
	"fmt"
	"strings"
	"sync"
)

// lockedWriter serialises writes from ctxt.Bso and util.Errorf so
// the shared capture buffer in inProcessLink doesn't get corrupted
// when an error path interleaves with -v progress output.
type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

// inProcessLink drives a single cmd/link invocation in the calling
// process via cmd/link/host.Run, mirroring (*Shell).run for a single
// `link <args>` command line. cmd/go calls this in gc.go's ld and
// ldShared paths when no toolexec wrapper is configured, -n is off,
// and GOGD_INPROC is not set to 0.
//
// args has the shape `[toolexec...] base.Tool("link") <flags...>`
// (the same shape sh.run would have received). We strip everything
// up to and including the link tool path so host.Run sees the argv
// slice the standalone link binary would have received as os.Args[1:].
//
// host.Run still serialises link invocations under linkRunMu while
// cmd/link's package-level state is migrated; the per-link mutex
// can be lifted once the migration completes.
func inProcessLink(sh *Shell, dir, desc string, env []string, args []any) error {
	cmdline := str.StringList(args...)

	// Mirror Shell.runOut: reject @-prefixed args before we hand
	// them off. The subprocess path checks this in runOut; the
	// in-process path bypasses runOut so re-implement the check here.
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "@") {
			return fmt.Errorf("invalid command-line argument %s in command: %s", arg, joinUnambiguously(cmdline))
		}
	}

	linkTool := base.Tool("link")
	idx := -1
	for i, a := range cmdline {
		if a == linkTool {
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

	// Capture link diagnostics into a single buffer and route
	// through sh.reportCmd, mirroring sh.run / sh.runOut for
	// subprocess link. ld.Main's stdout (ctxt.Bso, -v / progress
	// chatter) and stderr (util.Errorf / loader.ErrorReporter
	// diagnostics like "nosplit stack over N byte limit") share
	// the buffer so write-order interleaving — which the
	// testStackCheckOutput stanza parser depends on — survives.
	// Stock cmd/go gets the same effect for free via
	// exec.Cmd.CombinedOutput on the link subprocess.
	// dir is the directory the subprocess path would have run the
	// linker in — "." for ordinary links, the output directory for
	// c-shared/plugin links, whose -o is a bare filename precisely so
	// the temp path stays out of the artifact's recorded name (see
	// gcToolchain.ld). Pass it through so the in-process linker can
	// resolve the relative -o without a subprocess cwd; without this
	// the output landed in cmd/go's own cwd and the install step
	// failed with "open $WORK/b001/exe/a.out: no such file".
	workDir := dir
	if workDir == "." {
		workDir = ""
	}
	var buf bytes.Buffer
	lw := &lockedWriter{w: &buf}
	status, runErr := host.Run(cmdline[idx+1:], workDir, lw, lw)
	out := buf.Bytes()

	if desc == "" {
		desc = sh.fmtCmd(dir, "%s", strings.Join(cmdline, " "))
	}
	var cmdErr error
	switch {
	case runErr != nil:
		cmdErr = fmt.Errorf("%s: %v", linkTool, runErr)
	case status != 0:
		// Format mirrors os/exec.(*Cmd).Run for a non-zero exit so
		// reportCmd, vet, and tests grepping for the tool path
		// (e.g. testdata/script/linkname.txt expects
		// `tool/.../link` in stderr) see the same shape under
		// in-process linking as under subprocess execution.
		cmdErr = fmt.Errorf("%s: exit status %d", linkTool, status)
	}
	return sh.reportCmd(desc, dir, out, cmdErr)
}
