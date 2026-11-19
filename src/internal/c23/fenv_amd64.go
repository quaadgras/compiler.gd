// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "internal/cpu"

// fpHasFMA: fpFma64 and fpFma32 are FMA3's.
var FpHasFMA = cpu.X86.HasFMA

// fpControl is MXCSR for rounding direction r: Go's (every exception
// masked) with its RC field, which is the x87 field fenv.h's values are.
func FpControl(r int32) uint64 { return 0x1f80 | uint64(r)<<3 }

// fpExcepts are the exceptions MXCSR's status bits st hold: fenv.h's
// values are those bits (the denormal bit, 0x02, is none of C's).
func FpExcepts(st uint64) int32 { return int32(st) & FeAllExcept }
