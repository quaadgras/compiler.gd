// run

// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"unsafe"
)

func main() {
	// gd SSO: "abc" is inline-rep (len <= 15), so word 0 of the header
	// is nil — reflect.StringHeader.Data would return 0 here, which is
	// why this test no longer compares against it. unsafe.StringData
	// still returns a valid pointer to the first byte (materializing a
	// heap copy on demand for inline inputs).
	var s = "abc"
	ptr := unsafe.StringData(s)
	if ptr == nil {
		panic(fmt.Errorf("unsafe.StringData(%q) returned nil", s))
	}
	got := unsafe.String(ptr, len(s))
	if got != s {
		panic(fmt.Errorf("unsafe.String(unsafe.StringData(%q), %d) = %q, want %q", s, len(s), got, s))
	}
}
