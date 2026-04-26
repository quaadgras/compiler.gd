// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

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
	"log"
	"os"
)

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

func main() {
	// disable timestamps for reproducible output
	log.SetFlags(0)
	log.SetPrefix("compile: ")

	buildcfg.Check()
	archInit, ok := archInits[buildcfg.GOARCH]
	if !ok {
		fmt.Fprintf(os.Stderr, "compile: unknown architecture %q\n", buildcfg.GOARCH)
		os.Exit(2)
	}
	compiler := new(base.Invocation)
	gd.Main(archInit, compiler)
	// gd.Main may have set compiler.Status via gd.Exit (which only
	// runtime.Goexit's the calling goroutine, so an in-process harness
	// can keep running). At the outermost cmd/compile entry we must
	// terminate the process — runtime.Goexit on the main goroutine
	// triggers the deadlock detector once the worker pool drains.
	compiler.RunAtExitFuncs()
	os.Exit(compiler.Status)
}
