// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(linux || darwin || dragonfly || freebsd || netbsd || openbsd)

package signal

import "os"

// getpgrp is the program's process group: where the system has none,
// its process and children are one, its process id's.
func getpgrp() int32 { return int32(os.Getpid()) }

// pgidOf is process pid's group: the program's, for its children.
func pgidOf(pid int32) int32 { return getpgrp() }
