// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"os"
	"time"
)

// Utimes is utimes: tv is two struct timevals, the access and
// modification times, or null for now.
func Utimes(path, tv *c23.Void) int32 {
	a, m := time.Now(), time.Now()
	if c23.Addr(tv) != 0 {
		var ts [2]time.Time
		for i := range ts {
			sec, usec := int64(c23.Load64(c23.Add(tv, 16*i))), int64(c23.Load64(c23.Add(tv, 16*i+8)))
			if usec < 0 || usec >= 1e6 {
				c23.SetErrno(c23.EINVAL)
				return -1
			}
			ts[i] = time.Unix(sec, usec*1000)
		}
		a, m = ts[0], ts[1]
	}
	return c23.ErrRet(os.Chtimes(c23.GoString(path), a, m))
}
