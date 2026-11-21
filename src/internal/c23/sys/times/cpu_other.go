// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package times

import "time"

// cpuTimes returns the time since the program started as its user time,
// where the system's are not known.
func cpuTimes() (u, s, cu, cs time.Duration) {
	return time.Since(start), 0, 0, 0
}
