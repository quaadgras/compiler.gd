// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package host exposes a public entry point for invoking cmd/compile
// in-process — the same gd.Main the standalone `compile` binary
// drives, but with args supplied as a slice and the exit status
// returned to the caller instead of going through os.Exit. cmd/go
// imports this package to skip the per-package fork/exec overhead
// when building.
//
// gd fork specific: package-level Invocation state has been migrated
// onto *base.Invocation so two compiles in the same process don't
// alias state. See cmd/compile/internal/test/inproc_test.go for the
// canonical call shape.
package host

import (
	"cmd/compile/internal/amd64"
	"cmd/compile/internal/arm"
	"cmd/compile/internal/arm64"
	"cmd/compile/internal/base"
	"cmd/compile/internal/gd"
	"cmd/compile/internal/loong64"
	"cmd/compile/internal/mips"
	"cmd/compile/internal/mips64"
	"cmd/compile/internal/ppc64"
	"cmd/compile/internal/riscv64"
	"cmd/compile/internal/s390x"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/wasm"
	"cmd/compile/internal/x86"
	"fmt"
	"internal/buildcfg"
	"io"
)

// archInits mirrors the table in cmd/compile/main.go.
var archInits = map[string]func(*ssagen.ArchInfo){
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

// Run drives one cmd/compile invocation in the calling process.
// args is the argv that would be passed to the standalone `compile`
// binary, NOT including argv[0]. stdout and stderr are reserved for
// future use; today the compile internals write directly to
// os.Stdout/os.Stderr.
//
// Each call uses a fresh worker goroutine because gd.Exit (used on
// error and -V paths) calls runtime.Goexit. Run handles this
// internally — the caller's goroutine survives.
//
// Concurrent invocations: per-Invocation state lives on
// *base.Invocation (ssaConfig/ssaCaches/Pathsyms/SiggenSet/
// Defercalc/DeferredTypeStack/CalcSizeDisabled). Shared types from
// types.Types[] / rttype.* / abi.synth* are computed via sync.Once
// during process init; their Type.cache.{ptr,slice} are
// atomic.Pointer; subsequent NewPtr/NewSlice calls on shared elem
// types CAS-then-reload to preserve pointer-identity.
func Run(args []string, stdout, stderr io.Writer) (status int, err error) {
	archInit, ok := archInits[buildcfg.GOARCH]
	if !ok {
		return 2, fmt.Errorf("compile/host.Run: unknown architecture %q", buildcfg.GOARCH)
	}

	gd_ := &base.Invocation{InProcess: true, Stdout: stdout, Stderr: stderr}

	// Run gd.Main on a worker goroutine. gd.Exit (used by error
	// paths, -V, usage) calls runtime.Goexit; calling it directly
	// would terminate the caller's goroutine. Mirror the dance in
	// cmd/compile/main.go.
	done := make(chan struct{})
	go func() {
		defer close(done)
		gd.Main(archInit, gd_, args)
	}()
	<-done

	gd_.RunAtExitFuncs()

	_ = stdout // reserved: see comment above; compile internals write
	_ = stderr // directly to os.Stdout/os.Stderr today.

	return gd_.Status, nil
}
