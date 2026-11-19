// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import "testing"

// TestRandomGlibc checks random's numbers with glibc's (musl's random
// has a table and seeding of its own, so cmd/cc's tests cannot compare
// them with the host's).
func TestRandomGlibc(t *testing.T) {
	check := func(name string, want []int64) {
		t.Helper()
		for i, w := range want {
			if got := Random(); got != w {
				t.Fatalf("%s: random #%d = %d, want %d", name, i, got, w)
			}
		}
	}
	check("unseeded", []int64{1804289383, 846930886, 1681692777, 1714636915, 1957747793})
	Srandom(1)
	check("srandom(1)", []int64{1804289383, 846930886, 1681692777})
	Srandom(42)
	check("srandom(42)", []int64{71876166, 708592740, 1483128881})
}
