// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Standalone cgo binary. Forwards to cmd/cgo/internal/cgomain.Run,
// which holds all the actual logic so cmd/go can drive an
// in-process invocation via cmd/cgo/host.
package main

import (
	"os"

	"cmd/cgo/internal/cgomain"
)

func main() {
	cgomain.Run(os.Args[1:])
}
