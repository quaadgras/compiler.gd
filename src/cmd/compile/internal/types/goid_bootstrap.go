// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build compiler_bootstrap

package types

import (
	"bytes"
	"runtime"
	"strconv"
)

// calcSizeGoroutineID returns the id of the goroutine. The compiler
// that the bootstrap toolchain builds runs on that toolchain's runtime,
// which has no function for it: the id is read from runtime.Stack's
// output. That is slow (a traceback, under the lock of the runtime's
// prints), and no more than that compiler needs.
func calcSizeGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := buf[:n]
	const prefix = "goroutine "
	if !bytes.HasPrefix(b, []byte(prefix)) {
		return 0
	}
	b = b[len(prefix):]
	end := bytes.IndexByte(b, ' ')
	if end < 0 {
		return 0
	}
	id, err := strconv.ParseInt(string(b[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
