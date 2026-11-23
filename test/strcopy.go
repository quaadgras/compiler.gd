// run

// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that string([]byte(string)) makes a copy and doesn't reduce to
// nothing. (Issue 25834)

package main

import (
	"unsafe"
)

func main() {
	var (
		buf      = make([]byte, 2<<10)
		large    = string(buf)
		sub      = large[10:12]
		subcopy  = string([]byte(sub))
	)
	if unsafe.StringData(sub) == unsafe.StringData(subcopy) {
		panic("sub and subcopy have the same underlying array")
	}
}
