// Copyright 2026 The compiler.gd Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// toolchainName is the identity of the toolchain dist is producing.
// The fork has only one toolchain — gd — so this is unconditional.
// Used by matchtag to gate the compiler-identity build constraint
// (//go:build gd vs //go:build !gd) when filtering source files for
// the stdlib build, and by goCmd/checkNotStale to pass -compiler=gd
// to subcommands.
const toolchainName = "gd"
