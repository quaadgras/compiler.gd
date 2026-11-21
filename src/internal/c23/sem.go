// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import ()

// semaphore.h: semaphores kept by the address of the sem_t, as mutexes
// are. A named semaphore (sem_open) is the program's own: other processes
// do not share it. Waits are not interrupted by signals.

// SEM_VALUE_MAX

// uintptr -> *sem

// Named semaphores.

var ()
