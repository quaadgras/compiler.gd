// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 && !arm64

package runtime

import "unsafe"

// memmove16 and memclr16 (shared_string.go) are never called where a
// 16-byte access is not atomic (stringAtomic is false).

func memmove16(dst, src unsafe.Pointer, n uintptr) {
	memmove(dst, src, n)
}

func memclr16(p unsafe.Pointer, n uintptr) {
	memclrNoHeapPointers(p, n)
}
