// Copyright 2026 The compiler.gd Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gd

package main

// toolchainName is the identity of the toolchain dist is producing. It
// gates the compiler-identity build constraint (matchtag) when filtering
// source files for the stdlib build.
const toolchainName = "gd"
