// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux || !(amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x)

package cgotest

import (
	"testing"
	"unsafe"
)

func hugeBytesForC(t *testing.T) (unsafe.Pointer, int) { return nil, 0 }
