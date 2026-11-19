// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "testing"

func TestNewAligned(t *testing.T) {
	for _, align := range []uintptr{16, 64, 512, 1024, 4096} {
		for i := 0; i < 10; i++ {
			p := NewAligned[int32](align)
			if a := Addr(p); a%align != 0 {
				t.Errorf("NewAligned[int32](%d) = %#x", align, a)
			}
			q := NewAligned[[3]*int](align)
			if a := Addr(q); a%align != 0 {
				t.Errorf("NewAligned[[3]*int](%d) = %#x", align, a)
			}
			if c, ok := cobject(q.obj); !ok || c.obj != q.obj {
				t.Errorf("cobject of an aligned object: %v %v", c, ok)
			}
		}
	}
}
