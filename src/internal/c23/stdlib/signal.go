// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	"os"
	"os/exec"
)

// System is system: it runs command with /bin/sh -c and returns its
// status as wait does (its exit status times 256, or the number of the
// signal that ended it), -1 if it cannot run, or, for a null command,
// whether there is a shell. It does not flush C's streams first.
func System(command *c23.Void) int32 {
	if c23.Addr(command) == 0 {
		_, err := os.Stat("/bin/sh")
		return c23.B2I(err == nil)
	}
	cmd := exec.Command("/bin/sh", "-c", c23.GoString(command))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c23.WaitStatus(cmd, cmd.Run())
}
