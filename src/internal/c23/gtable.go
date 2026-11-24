// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "unsafe"

//go:linkname gTableSlot runtime.c23GTable
func gTableSlot() *unsafe.Pointer

// A gTable is what C's library keeps for one goroutine, which is a C
// thread. Only its goroutine uses it.
type gTable struct {
	tls    map[*TLS]*Void   // the goroutine's _Thread_local objects (thread.go)
	Keys   map[uint32]*Void // its pthread_setspecific values (thread.go)
	fe     fenvState        // its floating-point environment (fenv.go)
	Sig    *SigThread       // its signal state (sigaction.go)
	errno  *Void            // its errno, once it has used it (libc.go)
	errnoP *int32           // where errno is: nil until then

	th      *Thread             // the thread it is, if it is one (cancel.go)
	cleanup []cleanupHandler    // pthread_cleanup_push's handlers (cancel.go)
	robust  map[*Mutex]struct{} // the robust mutexes it holds (cancel.go)
}
