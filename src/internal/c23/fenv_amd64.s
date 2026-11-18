// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// One floating-point operation each, in the rounding direction of ctl, an
// MXCSR value with the status bits clear: st is MXCSR after it, so its
// status bits are the exceptions the operation raised. Go's MXCSR, status
// bits and all, is back on return (Go's ABI: its control bits are fixed at
// calls, its status bits callee-save).

// func fpAdd64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·fpAdd64(SB), NOSPLIT, $16-40
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X0
	LDMXCSR	0(SP)
	ADDSD	y+16(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+24(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+32(FP)
	RET

// func fpSub64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·fpSub64(SB), NOSPLIT, $16-40
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X0
	LDMXCSR	0(SP)
	SUBSD	y+16(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+24(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+32(FP)
	RET

// func fpMul64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·fpMul64(SB), NOSPLIT, $16-40
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X0
	LDMXCSR	0(SP)
	MULSD	y+16(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+24(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+32(FP)
	RET

// func fpDiv64(ctl uint64, x, y float64) (r float64, st uint64)
TEXT ·fpDiv64(SB), NOSPLIT, $16-40
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X0
	LDMXCSR	0(SP)
	DIVSD	y+16(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+24(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+32(FP)
	RET

// func fpAdd32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpAdd32(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSS	x+8(FP), X0
	LDMXCSR	0(SP)
	ADDSS	y+12(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpSub32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpSub32(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSS	x+8(FP), X0
	LDMXCSR	0(SP)
	SUBSS	y+12(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpMul32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpMul32(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSS	x+8(FP), X0
	LDMXCSR	0(SP)
	MULSS	y+12(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpDiv32(ctl uint64, x, y float32) (r float32, st uint64)
TEXT ·fpDiv32(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSS	x+8(FP), X0
	LDMXCSR	0(SP)
	DIVSS	y+12(FP), X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpSqrt64(ctl uint64, x float64) (r float64, st uint64)
TEXT ·fpSqrt64(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X1
	LDMXCSR	0(SP)
	SQRTSD	X1, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpSqrt32(ctl uint64, x float32) (r float32, st uint64)
TEXT ·fpSqrt32(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSS	x+8(FP), X1
	LDMXCSR	0(SP)
	SQRTSS	X1, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpNarrow(ctl uint64, x float64) (r float32, st uint64)
TEXT ·fpNarrow(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X1
	LDMXCSR	0(SP)
	CVTSD2SS	X1, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpIntTo64(ctl uint64, x int64) (r float64, st uint64)
TEXT ·fpIntTo64(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVQ	x+8(FP), BX
	LDMXCSR	0(SP)
	CVTSQ2SD	BX, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// func fpIntTo32(ctl uint64, x int64) (r float32, st uint64)
TEXT ·fpIntTo32(SB), NOSPLIT, $16-32
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVQ	x+8(FP), BX
	LDMXCSR	0(SP)
	CVTSQ2SS	BX, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+16(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+24(FP)
	RET

// fpFma64 and fpFma32 need FMA3 (fpHasFMA).

// func fpFma64(ctl uint64, x, y, z float64) (r float64, st uint64)
TEXT ·fpFma64(SB), NOSPLIT, $16-48
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSD	x+8(FP), X1
	MOVSD	y+16(FP), X2
	MOVSD	z+24(FP), X0
	LDMXCSR	0(SP)
	VFMADD231SD	X2, X1, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSD	X0, r+32(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+40(FP)
	RET

// func fpFma32(ctl uint64, x, y, z float32) (r float32, st uint64)
TEXT ·fpFma32(SB), NOSPLIT, $16-40
	STMXCSR	8(SP)
	MOVQ	ctl+0(FP), AX
	MOVL	AX, 0(SP)
	MOVSS	x+8(FP), X1
	MOVSS	y+12(FP), X2
	MOVSS	z+16(FP), X0
	LDMXCSR	0(SP)
	VFMADD231SS	X2, X1, X0
	STMXCSR	0(SP)
	LDMXCSR	8(SP)
	MOVSS	X0, r+24(FP)
	MOVL	0(SP), AX
	MOVQ	AX, st+32(FP)
	RET
