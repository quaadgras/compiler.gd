// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package resource

import "time"

var start = time.Now()

// rusage is getrusage's struct rusage, as longs: the time since the
// program started as its user time.
func rusage(who int32) ([18]int64, error) {
	var ru [18]int64
	if who != RUSAGE_CHILDREN {
		d := time.Since(start)
		ru[0], ru[1] = int64(d/time.Second), int64(d%time.Second/time.Microsecond)
	}
	return ru, nil
}
