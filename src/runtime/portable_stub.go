// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !portable

package runtime

// portableinit sets what differs between the hosts of a GOOS=portable program.
//
//go:nosplit
func portableinit(c int32, v **byte) {}
