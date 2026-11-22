// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// func memmove16(dst, src unsafe.Pointer, n uintptr)
// R0 = dst, R1 = src, R2 = n (a multiple of 16).
TEXT runtime·memmove16<ABIInternal>(SB),NOSPLIT|NOFRAME,$0-24
	CBZ	R2, done
	CMP	R0, R1
	BEQ	done
	SUB	R1, R0, R3
	CMP	R2, R3
	BLO	back	// dst - src < n: dst is in (src, src+n)
fwd:
	LDP.P	16(R1), (R4, R5)
	STP.P	(R4, R5), 16(R0)
	SUBS	$16, R2, R2
	BNE	fwd
	RET
back:
	ADD	R2, R0, R0
	ADD	R2, R1, R1
backloop:
	LDP.W	-16(R1), (R4, R5)
	STP.W	(R4, R5), -16(R0)
	SUBS	$16, R2, R2
	BNE	backloop
done:
	RET

// func memclr16(p unsafe.Pointer, n uintptr)
// R0 = p, R1 = n (a multiple of 16).
TEXT runtime·memclr16<ABIInternal>(SB),NOSPLIT|NOFRAME,$0-16
	CBZ	R1, done
loop:
	STP.P	(ZR, ZR), 16(R0)
	SUBS	$16, R1, R1
	BNE	loop
done:
	RET
