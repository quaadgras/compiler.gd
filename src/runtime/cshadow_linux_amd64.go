// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

const cShadowOn = true

// cShadowInit maps the shadow: nothing of it is memory until it is
// written, and the system does not count it as memory that the program
// may come to ask for.
func cShadowInit() {
	const noReserve = 0x4000
	p, err := mmap(unsafe.Pointer(uintptr(cShadowBase)), cShadowBytes, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_FIXED|_MAP_PRIVATE|noReserve, -1, 0)
	if err != 0 || uintptr(p) != cShadowBase {
		print("runtime: mmap of the shadow of C's memory: errno ", err, "\n")
		throw("runtime: cannot map the shadow of C's memory")
	}
	sysNoHugePageOS(p, cShadowBytes)
}
