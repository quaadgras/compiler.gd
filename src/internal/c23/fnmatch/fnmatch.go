// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fnmatch

import (
	c23 "internal/c23"
)

const FNM_NOMATCH = 1

// Fnmatch is fnmatch: 0 if string matches pattern, else FNM_NOMATCH.
func Fnmatch(pattern, str c23.Ptr, flags int32) int32 {
	if c23.Fnmatch(c23.GoString(pattern), c23.GoString(str), flags) {
		return 0
	}
	return FNM_NOMATCH
}
