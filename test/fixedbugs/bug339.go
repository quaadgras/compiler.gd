// run

// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Issue 1608.
// Size used to be -1000000000.

package main

import "unsafe"

func main() {
	var a interface{} = 0
	size := unsafe.Sizeof(a)
	// gd fat-interface: header carries a 16-byte inline payload
	// alongside the two words, so size == 2*PtrSize + 16.
	if size != 2*unsafe.Sizeof((*int)(nil))+16 {
		println("wrong size: ", size)
	}
}
