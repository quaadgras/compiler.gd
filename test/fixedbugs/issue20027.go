// errorcheck

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package p

// gd: fork's CalcSize bails via fatal.Error on the first too-large
// chan element, so only the make() on line 10 is reported and the
// other three decls never reach the size check.
var _ chan [0x2FFFF]byte
var _ = make(chan [0x2FFFF]byte) // ERROR "channel element type too large"

var c1 chan [0x2FFFF]byte
var c2 = make(chan [0x2FFFF]byte)
