// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Cc translates C to Go in which C's pointers are checked against typed
// capabilities, so that C shares Go's heap, stack and collector and
// cannot break what Go relies on (doc/gd/cc.md). The Go it writes
// imports internal/c23.
//
// Usage:
//
//	go tool cc [-pkg name] [-main] -o out.go in.c
//
// Standalone binary. Forwards to cmd/cc/internal/ccmain.Run, which holds
// the logic, so that cmd/go can drive it in-process via cmd/cc/host.
package main

import (
	"os"

	"cmd/cc/internal/ccmain"
)

func main() {
	os.Exit(ccmain.Run(os.Args[1:], os.Stdout, os.Stderr))
}
