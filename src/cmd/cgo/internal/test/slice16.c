// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include <stdint.h>
#include "_cgo_export.h"

// sliceRoundTrip passes the bytes at p, n of them, to Go as a GoSlice,
// and checks the GoSlice and the byte at i that Go returns
// (cgoSliceRoundTrip).
int sliceRoundTrip(void *p, int64_t n, int64_t i) {
	GoSlice s = {p, n, n};
	struct cgoSliceRoundTrip_return r = cgoSliceRoundTrip(s, i);
	if (r.r0.data != (char*)p + 1) {
		return 1;
	}
	if (r.r0.len != n - 1 || r.r0.cap != n - 1) {
		return 2;
	}
	if (r.r1 != ((unsigned char*)p)[i]) {
		return 3;
	}
	return 0;
}
