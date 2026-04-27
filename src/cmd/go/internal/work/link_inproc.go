// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/str"
	"cmd/link/host"
	"fmt"
	"os"
	"strings"
)

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

	status, runErr := host.Run(cmdline[idx+1:], os.Stdout, os.Stderr)
	if runErr != nil {
		return runErr
	}
	if status != 0 {
		return fmt.Errorf("exit status %d", status)
	}
	return nil
}
