// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

const cShadowOn = true

// cShadowInit maps the shadow: nothing of it is memory until it is
// written, and the system does not count it as memory that the program
// may come to ask for.
//
// The system is asked itself, not C's mmap as for the other memory of a
// program with C in it (cgo_mmap.go): a sanitizer of C has its own
// mmap, which refuses an address that is not where it has a program's
// memory be, and the shadow is at none of those. No C code reads or
// writes the shadow, so a sanitizer need not know of it.
func cShadowInit() {
	const noReserve = 0x4000
	p, err := sysMmap(unsafe.Pointer(uintptr(cShadowBase)), cShadowBytes, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_FIXED|_MAP_PRIVATE|noReserve, -1, 0)
	if err != 0 || uintptr(p) != cShadowBase {
		print("runtime: mmap of the shadow of C's memory: errno ", err, "\n")
		throw("runtime: cannot map the shadow of C's memory")
	}
	sysNoHugePageOS(p, cShadowBytes)
}
