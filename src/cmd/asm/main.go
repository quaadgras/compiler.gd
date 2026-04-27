// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Standalone asm binary. Forwards to cmd/asm/host.Run, which holds
// all the actual logic so cmd/go can drive an in-process invocation
// without a fork/exec.
package main

import (
	"os"

	"cmd/asm/host"
)

func main() {
	status, err := host.Run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		// Genuinely unexpected error from host.Run itself (not an
		// assembler diagnostic). Print and exit non-zero.
		os.Stderr.WriteString("asm: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(status)
}
