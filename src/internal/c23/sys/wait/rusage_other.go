// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package wait

import "os"

// sysRusage is zero but for the times, which storeRusage takes from
// os.ProcessState.
func sysRusage(ps *os.ProcessState) [18]int64 { return [18]int64{} }
