// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build arm64

package runtime

import (
	"internal/abi"
	"unsafe" // also for go:linkname
)

// fastcbEntrySupported gates fastcbArmEntry. The arm64 thunk and the
// fused outbound crossing are enabled on darwin and ios, where graphics.gd's
// stock-toolchain overlay has device-verified the same code; linux/arm64
// (Android) keeps the stock cgocallback entry until it has been exercised
// on a device. Windows needs the preemptExtLock discipline neither carries.
const fastcbEntrySupported = GOOS == "darwin" || GOOS == "ios"

// fastcbentryfast is the direct C-ABI virtual-call entry thunk
// (fastcb_arm64.s). Never called from Go — the engine calls its PC with C
// ABI arguments. Unlike amd64's fastcbentry it does not splice a frame
// into the goroutine stack by hand: it enters fastcbentrycall, a real
// frame whose saved LR is the resume anchor, so it carries no special
// FuncID.
func fastcbentryfast()

// fastcbentrycall is the dispatcher's caller frame (fastcb_arm64.s),
// entered from fastcbentryfast on the resident goroutine's stack. Never
// called from Go.
func fastcbentrycall()

// fastcbentryexit unwinds from fastcbentrycall back to the engine's C
// frame (fastcb_arm64.s). Never returns.
func fastcbentryexit()

// fastcbEntryPC is the C-callable PC of the direct entry thunk.
func fastcbEntryPC() uintptr { return abi.FuncPCABI0(fastcbentryfast) }

// fastcbEntryDepth and fastcbEntrySaved hold the engine's C context for
// each nested thunk entry (fastcb_arm64.s): [0] the C SP at the thunk's
// callee-saved block, [1] the g0.sched.sp the entry replaced. Only the
// resident m touches them, so plain globals suffice; a ninth nested
// entry falls back to the stock path.
var (
	fastcbEntryDepth uintptr
	fastcbEntrySaved [8][2]uintptr
)

// fastcbCallCFast is the fused resident outbound crossing (fastcb_arm64.s):
// fastcbCallC + asmcgocall specialized for the resident goroutine calling a
// single-pointer-argument C function with the errno ignored. Callers must
// hold the same preconditions as fastcbCallC and be on a platform where
// osPreemptExtEnter is a no-op — graphics.gd selects it only where the
// entry thunk arms.
//
//go:linkname fastcbCallCFast
//go:noescape
func fastcbCallCFast(fn, arg unsafe.Pointer)
