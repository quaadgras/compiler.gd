// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Standalone link binary. Forwards to cmd/link/host.Run, which holds
// all the actual logic so cmd/go can drive an in-process invocation
// without a fork/exec.
package main

import (
	"os"

	"cmd/link/host"
)

// The bulk of the linker implementation lives in cmd/link/internal/ld.
// Architecture-specific code lives in cmd/link/internal/GOARCH.
//
// Program initialization:
//
// Before any argument parsing is done, the Init function of the relevant
// architecture package is called. The only job done in Init is
// configuration of the architecture-specific variables.
//
// Then control flow passes to ld.Main, which parses flags, makes
// some configuration decisions, and then gives the architecture
// packages a second chance to modify the linker's configuration
// via the ld.Arch.Archinit function.
func main() {
	status, err := host.Run(os.Args[1:], "", os.Stdout, os.Stderr)
	if err != nil {
		os.Stderr.WriteString("link: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(status)
}
