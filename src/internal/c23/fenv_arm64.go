// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

const FpHasFMA = true

// FpControl is FPCR for rounding direction r: Go's (zero) with its RMode
// field (bits 22-23: RN, RP, RM, RZ).
func FpControl(r int32) uint64 {
	switch r {
	case FeUpward:
		return 1 << 22
	case FeDownward:
		return 2 << 22
	case FeTowardZero:
		return 3 << 22
	}
	return 0
}

// FpExcepts are the exceptions FPSR's cumulative bits st hold: IOC, DZC,
// OFC, UFC and IXC (bits 0-4), as fenv.h numbers them.
func FpExcepts(st uint64) int32 { return int32(st&1) | int32(st&0x1e)<<1 }
