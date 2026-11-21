// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package stat

import "sync/atomic"

var umaskVal atomic.Uint32

func init() { umaskVal.Store(0o022) }

// umask records the mask, which the system does not have.
func umask(mask uint32) uint32 { return umaskVal.Swap(mask) }
