// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// One floating-point operation each, in the rounding direction of ctl, an
// FPCR value: st is FPSR after it, cleared before, so its cumulative bits
// are the exceptions the operation raised. Go's FPCR is back on return
// (Go's ABI fixes it at calls; FPSR is scratch).

#define BEGIN \
	MOVD	ctl+0(FP), R0; \
	MRS	FPCR, R1; \
	MSR	R0, FPCR; \
	MSR	ZR, FPSR

#define END(st) \
	MRS	FPSR, R2; \
	MSR	R1, FPCR; \
	MOVD	R2, st

// func fpAdd64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·FpAdd64(SB), NOSPLIT, $0-40
	FMOVD	x+8(FP), F0
	FMOVD	y+16(FP), F1
	BEGIN
	FADDD	F1, F0, F0
	END(st+32(FP))
	FMOVD	F0, r+24(FP)
	RET

// func fpSub64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·FpSub64(SB), NOSPLIT, $0-40
	FMOVD	x+8(FP), F0
	FMOVD	y+16(FP), F1
	BEGIN
	FSUBD	F1, F0, F0
	END(st+32(FP))
	FMOVD	F0, r+24(FP)
	RET

// func fpMul64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·FpMul64(SB), NOSPLIT, $0-40
	FMOVD	x+8(FP), F0
	FMOVD	y+16(FP), F1
	BEGIN
	FMULD	F1, F0, F0
	END(st+32(FP))
	FMOVD	F0, r+24(FP)
	RET

// func fpDiv64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·FpDiv64(SB), NOSPLIT, $0-40
	FMOVD	x+8(FP), F0
	FMOVD	y+16(FP), F1
	BEGIN
	FDIVD	F1, F0, F0
	END(st+32(FP))
	FMOVD	F0, r+24(FP)
	RET

// func fpAdd32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpAdd32(SB), NOSPLIT, $0-32
	FMOVS	x+8(FP), F0
	FMOVS	y+12(FP), F1
	BEGIN
	FADDS	F1, F0, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpSub32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpSub32(SB), NOSPLIT, $0-32
	FMOVS	x+8(FP), F0
	FMOVS	y+12(FP), F1
	BEGIN
	FSUBS	F1, F0, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpMul32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpMul32(SB), NOSPLIT, $0-32
	FMOVS	x+8(FP), F0
	FMOVS	y+12(FP), F1
	BEGIN
	FMULS	F1, F0, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpDiv32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpDiv32(SB), NOSPLIT, $0-32
	FMOVS	x+8(FP), F0
	FMOVS	y+12(FP), F1
	BEGIN
	FDIVS	F1, F0, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpSqrt64(ctl uint64, x float64) (r float64, st uint64)
TEXT ·FpSqrt64(SB), NOSPLIT, $0-32
	FMOVD	x+8(FP), F1
	BEGIN
	FSQRTD	F1, F0
	END(st+24(FP))
	FMOVD	F0, r+16(FP)
	RET

// func fpSqrt32(ctl uint64, x float32) (r float32, st uint64)
TEXT ·fpSqrt32(SB), NOSPLIT, $0-32
	FMOVS	x+8(FP), F1
	BEGIN
	FSQRTS	F1, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpNarrow(ctl uint64, x float64) (r float32, st uint64)
TEXT ·FpNarrow(SB), NOSPLIT, $0-32
	FMOVD	x+8(FP), F1
	BEGIN
	FCVTDS	F1, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpIntTo64(ctl uint64, x int64) (r float64, st uint64)
TEXT ·fpIntTo64(SB), NOSPLIT, $0-32
	MOVD	x+8(FP), R3
	BEGIN
	SCVTFD	R3, F0
	END(st+24(FP))
	FMOVD	F0, r+16(FP)
	RET

// func fpIntTo32(ctl uint64, x int64) (r float32, st uint64)
TEXT ·fpIntTo32(SB), NOSPLIT, $0-32
	MOVD	x+8(FP), R3
	BEGIN
	SCVTFS	R3, F0
	END(st+24(FP))
	FMOVS	F0, r+16(FP)
	RET

// func fpFma64(ctl uint64, x, y, z float64) (r float64, st uint64)
TEXT ·FpFma64(SB), NOSPLIT, $0-48
	FMOVD	x+8(FP), F1
	FMOVD	y+16(FP), F2
	FMOVD	z+24(FP), F3
	BEGIN
	FMADDD	F2, F3, F1, F0
	END(st+40(FP))
	FMOVD	F0, r+32(FP)
	RET

// func fpFma32(ctl uint64, x, y, z float32) (r float32, st uint64)
TEXT ·FpFma32(SB), NOSPLIT, $0-40
	FMOVS	x+8(FP), F1
	FMOVS	y+12(FP), F2
	FMOVS	z+16(FP), F3
	BEGIN
	FMADDS	F2, F3, F1, F0
	END(st+32(FP))
	FMOVS	F0, r+24(FP)
	RET
