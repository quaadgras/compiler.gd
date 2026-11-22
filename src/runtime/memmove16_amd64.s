// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// func memmove16(dst, src unsafe.Pointer, n uintptr)
// AX = dst, BX = src, CX = n (a multiple of 16).
TEXT runtime·memmove16<ABIInternal>(SB),NOSPLIT,$0-24
	TESTQ	CX, CX
	JEQ	done
	CMPQ	AX, BX
	JEQ	done
	MOVQ	AX, DX
	SUBQ	BX, DX
	CMPQ	DX, CX
	JB	back	// dst - src < n: dst is in (src, src+n)
fwd:
	MOVUPS	(BX), X0
	MOVUPS	X0, (AX)
	ADDQ	$16, AX
	ADDQ	$16, BX
	SUBQ	$16, CX
	JNE	fwd
	RET
back:
	ADDQ	CX, AX
	ADDQ	CX, BX
backloop:
	SUBQ	$16, AX
	SUBQ	$16, BX
	MOVUPS	(BX), X0
	MOVUPS	X0, (AX)
	SUBQ	$16, CX
	JNE	backloop
done:
	RET

// func memclr16(p unsafe.Pointer, n uintptr)
// AX = p, BX = n (a multiple of 16); X15 is zero.
TEXT runtime·memclr16<ABIInternal>(SB),NOSPLIT,$0-16
	TESTQ	BX, BX
	JEQ	done
loop:
	MOVUPS	X15, (AX)
	ADDQ	$16, AX
	SUBQ	$16, BX
	JNE	loop
done:
	RET
