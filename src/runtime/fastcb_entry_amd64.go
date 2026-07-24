// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64

package runtime

import "unsafe" // also for go:linkname

// fastcbEntrySupported gates fastcbArmEntry: the direct C-ABI virtual-call
// entry thunk is implemented for amd64 and validated on Linux. Other systems
// (notably Windows, which needs the preemptExtLock discipline the thunk does
// not carry) keep the stock cgocallback entry.
const fastcbEntrySupported = GOOS == "linux"

// fastcbentry is the direct C-ABI virtual-call entry thunk
// (fastcb_amd64.s). Never called from Go — the engine calls its PC with C
// ABI arguments.
func fastcbentry()

// fastcbCallCFast is the fused resident outbound crossing (fastcb_amd64.s):
// fastcbCallC + asmcgocall specialized for the resident goroutine calling a
// single-pointer-argument C function with the errno ignored. Callers must
// hold the same preconditions as fastcbCallC and be on a platform where
// osPreemptExtEnter is a no-op — graphics.gd selects it only where the entry
// thunk armed (linux/amd64).
//
//go:linkname fastcbCallCFast
//go:noescape
func fastcbCallCFast(fn, arg unsafe.Pointer)
