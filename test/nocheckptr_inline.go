// run

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A function marked go:nocheckptr is not checked where it is inlined
// either: neither its conversions, nor the slice that it makes of a
// converted pointer to an array.

package main

import "unsafe"

type attr struct {
	p *byte
	n uint32
}

type groups struct {
	count uint32
	g     [1]attr
}

//go:nocheckptr
func (g *groups) all() []attr {
	return (*[(1 << 28) - 1]attr)(unsafe.Pointer(&g.g[0]))[:g.count:g.count]
}

//go:nocheckptr
func get(b []byte) *groups { return (*groups)(unsafe.Pointer(&b[0])) }

func main() {
	b := make([]byte, 256)
	b[0] = 2
	s := get(b).all()
	if len(s) != 2 || s[1].n != 0 {
		panic("bad slice")
	}
}
