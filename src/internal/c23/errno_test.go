// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "testing"

func BenchmarkSetErrno(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		SetErrno(int32(i))
	}
}

func BenchmarkErrno(b *testing.B) {
	SetErrno(1)
	var s int32
	for b.Loop() {
		s += Errno()
	}
	_ = s
}
