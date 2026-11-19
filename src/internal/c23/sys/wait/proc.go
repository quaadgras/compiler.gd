// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wait

import (
	c23 "internal/c23"
)

const WNOHANG = 1

const WUNTRACED = 2

const WCONTINUED = 8

// Waitpid is waitpid: for pid -1 (or 0, or a process group: any child
// posix_spawn started) or one child. It reports only children that
// ended, with Linux's status: the exit status times 256, or the signal
// that ended it. WUNTRACED and WCONTINUED are accepted, and report
// nothing. It is not interrupted by signals.
func Waitpid(pid int32, status c23.Ptr, options int32) int32 {
	if options&^(WNOHANG|WUNTRACED|WCONTINUED) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	c23.ChildMu.Lock()
	defer c23.ChildMu.Unlock()
	for {
		var found *c23.Child
		any := false
		for _, c := range c23.Children {
			if pid > 0 && c.Pid != pid {
				continue
			}
			any = true
			if c.Done && (found == nil || c.Seq < found.Seq) {
				found = c
			}
		}
		if found != nil {
			delete(c23.Children, found.Pid)
			if c23.Addr(status) != 0 {
				c23.Store32(status, uint32(found.Status))
			}
			return found.Pid
		}
		if !any {
			c23.SetErrno(c23.ECHILD)
			return -1
		}
		if options&WNOHANG != 0 {
			return 0
		}
		c23.ChildCond.Wait()
	}
}

// Wait is wait.
func Wait(status c23.Ptr) int32 { return Waitpid(-1, status, 0) }
