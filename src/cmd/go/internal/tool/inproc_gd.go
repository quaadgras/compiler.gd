// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tool

import (
	"io"

	cchost "cmd/cc/host"
)

// inProcessTools are the tools that are part of the go binary itself (gd
// fork): go tool runs them in-process instead of from the tool directory.
var inProcessTools = map[string]func(args []string, stdout, stderr io.Writer) (int, error){
	"cc": cchost.Run,
}
