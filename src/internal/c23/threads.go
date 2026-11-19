// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import ()

// <threads.h>: C's threads are pthread.h's (goroutines), with its own
// results.

// threads.h's results (glibc's and musl's values).
const ()

// mtx_init's types.
const (
	mtxPlain = 0
)

// Thread-specific storage: tss_t is a pthread_key_t.
