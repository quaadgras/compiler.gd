// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1 && wasm.split

package syscall

import "unsafe"

// hostBytes stops the program unless the n bytes at p are in what p
// points into: the host is given an address and a length, and reads or
// writes there with no pointer to ask. (In the runtime. A slice of the
// program's is what its words say, which with package unsafe or C in
// the program may be more than is there.)
func hostBytes(p unsafe.Pointer, n int)
