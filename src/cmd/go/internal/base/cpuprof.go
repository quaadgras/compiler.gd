// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"fmt"
	"os"
	"runtime/pprof"
)

// init starts, with GOGD_CPUPROF=file in the environment, a CPU profile
// of the go command, which is written to the file when the command
// exits: the compiler, the assembler and the linker run in it, so their
// own profiling flags see one package each and not what a build costs.
//
// gd fork only.
func init() {
	path := os.Getenv("GOGD_CPUPROF")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go: GOGD_CPUPROF: %v\n", err)
		return
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintf(os.Stderr, "go: GOGD_CPUPROF: %v\n", err)
		f.Close()
		return
	}
	AtExit(func() {
		pprof.StopCPUProfile()
		f.Close()
	})
}
