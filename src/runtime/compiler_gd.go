// Copyright 2026 The compiler.gd Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gd

package runtime

// Compiler is the name of the compiler toolchain that built the
// running binary. Known toolchains are:
//
//	gc      Also known as cmd/compile.
//	gccgo   The gccgo front end, part of the GCC compiler suite.
//	gd      The compiler.gd fork of cmd/compile.
const Compiler = "gd"
