// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package utime

import (
	c23 "internal/c23"
	"os"
	"time"
)

// Utime is utime: times is a struct utimbuf {time_t actime; time_t
// modtime;}, or null for now.
func Utime(path, times c23.Ptr) int32 {
	a, m := time.Now(), time.Now()
	if c23.Addr(times) != 0 {
		a = time.Unix(int64(c23.Load64(times)), 0)
		m = time.Unix(int64(c23.Load64(c23.Add(times, 8))), 0)
	}
	return c23.ErrRet(os.Chtimes(c23.GoString(path), a, m))
}
