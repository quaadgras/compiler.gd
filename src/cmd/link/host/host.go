// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package host exposes a public entry point for invoking cmd/link
// in-process. cmd/go imports this package to skip the per-binary
// fork/exec overhead when linking.
//
// gd fork specific: cmd/link's package-level state (~100 flag
// pointers, format-output globals, nerrors counter, atExitFuncs)
// has not yet been migrated onto a per-Link Context. host.Run
// serialises invocations under linkRunMu so the still-shared globals
// don't race; once the migration finishes, the mutex can be lifted.
package host

import (
	"fmt"
	"io"
	"os"
	"sync"

	"cmd/internal/sys"
	"cmd/link/internal/amd64"
	"cmd/link/internal/arm"
	"cmd/link/internal/arm64"
	"cmd/link/internal/ld"
	"cmd/link/internal/loong64"
	"cmd/link/internal/mips"
	"cmd/link/internal/mips64"
	"cmd/link/internal/ppc64"
	"cmd/link/internal/riscv64"
	"cmd/link/internal/s390x"
	"cmd/link/internal/wasm"
	"cmd/link/internal/x86"

	"internal/buildcfg"
)

// archInits mirrors the table in cmd/link/main.go.
var archInits = map[string]func() (*sys.Arch, ld.Arch){
	"386":      x86.Init,
	"amd64":    amd64.Init,
	"arm":      arm.Init,
	"arm64":    arm64.Init,
	"loong64":  loong64.Init,
	"mips":     mips.Init,
	"mipsle":   mips.Init,
	"mips64":   mips64.Init,
	"mips64le": mips64.Init,
	"ppc64":    ppc64.Init,
	"ppc64le":  ppc64.Init,
	"riscv64":  riscv64.Init,
	"s390x":    s390x.Init,
	"wasm":     wasm.Init,
}

// linkRunMu serialises Run calls. cmd/link's package-level state
// (flags, nerrors, format-output globals, atExitFuncs) is not yet
// per-Invocation, so concurrent Runs would race. Lifted later once
// the migration is complete.
var linkRunMu sync.Mutex

// Run drives one cmd/link invocation in the calling process. args is
// the argv that the standalone link binary would have received as
// os.Args[1:]. stdout/stderr are reserved (link internals write to
// os.Stdout/os.Stderr; ctxt.Bso flushes to os.Stdout).
//
// Run uses a worker goroutine + ld.Exit's runtime.Goexit hook so
// link's many Exitf call sites can terminate cleanly without taking
// down the calling cmd/go process.
func Run(args []string, stdout, stderr io.Writer) (status int, err error) {
	_ = stdout
	_ = stderr

	if buildcfg.Error != nil {
		fmt.Fprintf(os.Stderr, "link: %v\n", buildcfg.Error)
		return 2, nil
	}
	archInit, ok := archInits[buildcfg.GOARCH]
	if !ok {
		fmt.Fprintf(os.Stderr, "link: unknown architecture %q\n", buildcfg.GOARCH)
		return 2, nil
	}

	linkRunMu.Lock()
	defer linkRunMu.Unlock()

	arch, theArch := archInit()

	// inProcessStatus + runtime.Goexit dance: ld.Exit writes the
	// status to *st and Goexits, the worker goroutine unwinds (running
	// AtExit funcs along the way), the main goroutine sees done close
	// and reads st. The status pointer is per-Link (passed into Main,
	// stored on ctxt) so concurrent host.Run invocations don't race.
	var st int

	done := make(chan struct{})
	go func() {
		defer close(done)
		ld.Main(arch, theArch, args, &st)
	}()
	<-done

	return st, nil
}
