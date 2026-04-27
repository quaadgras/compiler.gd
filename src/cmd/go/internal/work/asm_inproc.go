// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"cmd/asm/host"
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/str"
	"fmt"
	"os"
	"strings"
)

// inProcessAssemble drives a single cmd/asm invocation in the calling
// process via cmd/asm/host.Run, mirroring (*Shell).run for a single
// `asm <args>` command line. cmd/go calls this in gc.go's asm path
// when no toolexec wrapper is configured and -n is off and
// GOGD_INPROC is not set to 0.
//
// args has the shape `[toolexec...] base.Tool("asm") <flags...> <files...>`
// (the same shape sh.run would have received). We strip everything
// up to and including the asm tool path so host.Run sees the argv
// slice the standalone asm binary would have received as os.Args[1:].
//
// Falls back to (*Shell).run on any unexpected shape (response files,
// missing tool token).
func inProcessAssemble(sh *Shell, dir string, env []string, args []any) error {
	cmdline := str.StringList(args...)

	asmTool := base.Tool("asm")
	idx := -1
	for i, a := range cmdline {
		if a == asmTool {
			idx = i
			break
		}
	}
	if idx < 0 {
		return sh.run(dir, "", env, args...)
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
