// errorcheck

// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// gd: fork's CalcSize bails via fatal.Error from the function-decl
// position, not the make() expression position, so the error lands on
// line 9 instead of line 10.
func main() { // ERROR "channel element type too large"
	ch := make(chan struct{ v [65536]byte })
	close(ch)
}
