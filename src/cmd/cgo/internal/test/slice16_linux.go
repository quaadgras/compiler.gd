// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package cgotest

import (
	"syscall"
	"testing"
	"unsafe"
)

// hugeBytesForC returns 5 GiB of memory, which the kernel commits as it
// is touched, unmapped when t ends.
func hugeBytesForC(t *testing.T) (unsafe.Pointer, int) {
	const n = 5 << 30
	b, err := syscall.Mmap(-1, 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE|syscall.MAP_NORESERVE)
	if err != nil {
		t.Logf("mmap of %d bytes: %v", n, err)
		return nil, 0
	}
	t.Cleanup(func() { syscall.Munmap(b) })
	return unsafe.Pointer(unsafe.SliceData(b)), n
}
