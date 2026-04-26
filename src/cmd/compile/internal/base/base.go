// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"runtime"
)

func (gd *Invocation) AtExit(f func()) {
	gd.atExitFuncs = append(gd.atExitFuncs, f)
}

// RunAtExitFuncs runs and drains the registered AtExit callbacks in
// LIFO order. The outermost cmd/compile entry calls this directly
// before os.Exit (since Exit's runtime.Goexit isn't appropriate at the
// process boundary).
func (gd *Invocation) RunAtExitFuncs() {
	for i := len(gd.atExitFuncs) - 1; i >= 0; i-- {
		f := gd.atExitFuncs[i]
		gd.atExitFuncs = gd.atExitFuncs[:i]
		f()
	}
}

func (gd *Invocation) Exit(code int) {
	gd.RunAtExitFuncs()
	gd.Status = code
	runtime.Goexit()
}

// To enable tracing support (-t flag), set EnableTrace to true.
const EnableTrace = false
