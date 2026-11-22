// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "go_asm.h"
#include "textflag.h"

TEXT ·Compare<ABIInternal>(SB),NOSPLIT|NOFRAME,$0-56
	// R0 = a_base (want in R0)
	// R1 = a_len  (want in R1)
	// R2 = a_cap  (unused)
	// R3 = b_base (want in R2)
	// R4 = b_len  (want in R3)
	// R5 = b_cap  (unused)
	MOVD	R3, R2
	MOVD	R4, R3
	B	cmpbody<>(SB)

TEXT runtime·cmpstring<ABIInternal>(SB),NOSPLIT|NOFRAME,$0-40
	// R0 = a_base
	// R1 = a_len
	// R2 = b_base
	// R3 = b_len
	// gd: either inline (w0's bit 63, doc/gd/sso-string.md)?
	ORR	R0, R2, R4
	TBZ	$63, R4, notinline
	B	cmpstrinline<>(SB)
notinline:
	B	cmpbody<>(SB)

// cmpstrinline is cmpstring with at least one inline string. Two inline
// strings are compared in registers: byte-reversed, w1 (bytes 0..7) and
// then w0 (bytes 8..14 and, last, the tag, whose low bits are the
// length) compare as their bytes do, the shorter of two whose bytes
// agree less. Otherwise the inline one's bytes are put in order in the
// frame (w1, w0) for cmpbody.
TEXT cmpstrinline<>(SB),NOSPLIT,$40-0
	AND	R0, R2, R4
	TBZ	$63, R4, mixed
	REV	R1, R1
	REV	R3, R3
	CMP	R3, R1
	BNE	differ
	REV	R0, R0
	REV	R2, R2
	CMP	R2, R0
differ:
	CSET	HI, R4
	CSINV	HS, R4, ZR, R0
	RET
mixed:
	TBZ	$63, R0, aheap
	MOVD	R1, 8(RSP)
	MOVD	R0, 16(RSP)
	LSR	$56, R0, R1
	AND	$15, R1
	MOVD	RSP, R0
	ADD	$8, R0
aheap:
	TBZ	$63, R2, bheap
	MOVD	R3, 24(RSP)
	MOVD	R2, 32(RSP)
	LSR	$56, R2, R3
	AND	$15, R3
	MOVD	RSP, R2
	ADD	$24, R2
bheap:
	BL	cmpbody<>(SB)
	RET

// On entry:
// R0 points to the start of a
// R1 is the length of a
// R2 points to the start of b
// R3 is the length of b
//
// On exit:
// R0 is the result
// R4, R5, R6, R8, R9 and R10 are clobbered
TEXT cmpbody<>(SB),NOSPLIT|NOFRAME,$0-0
	CMP	R0, R2
	BEQ	samebytes         // same starting pointers; compare lengths
	CMP	R1, R3
	CSEL	LT, R3, R1, R6    // R6 is min(R1, R3)

	CBZ	R6, samebytes
	BIC	$0xf, R6, R10
	CBZ	R10, small        // length < 16
	ADD	R0, R10           // end of chunk16
	// length >= 16
chunk16_loop:
	LDP.P	16(R0), (R4, R8)
	LDP.P	16(R2), (R5, R9)
	CMP	R4, R5
	BNE	cmp
	CMP	R8, R9
	BNE	cmpnext
	CMP	R10, R0
	BNE	chunk16_loop
	AND	$0xf, R6, R6
	CBZ	R6, samebytes
	SUBS	$8, R6
	BLT	tail
	// the length of tail > 8 bytes
	MOVD.P	8(R0), R4
	MOVD.P	8(R2), R5
	CMP	R4, R5
	BNE	cmp
	SUB	$8, R6
	// compare last 8 bytes
tail:
	MOVD	(R0)(R6), R4
	MOVD	(R2)(R6), R5
	CMP	R4, R5
	BEQ	samebytes
cmp:
	REV	R4, R4
	REV	R5, R5
	CMP	R4, R5
ret:
	MOVD	$1, R0
	CNEG	HI, R0, R0
	RET
small:
	TBZ	$3, R6, lt_8
	MOVD	(R0), R4
	MOVD	(R2), R5
	CMP	R4, R5
	BNE	cmp
	SUBS	$8, R6
	BEQ	samebytes
	ADD	$8, R0
	ADD	$8, R2
	SUB	$8, R6
	B	tail
lt_8:
	TBZ	$2, R6, lt_4
	MOVWU	(R0), R4
	MOVWU	(R2), R5
	CMPW	R4, R5
	BNE	cmp
	SUBS	$4, R6
	BEQ	samebytes
	ADD	$4, R0
	ADD	$4, R2
lt_4:
	TBZ	$1, R6, lt_2
	MOVHU	(R0), R4
	MOVHU	(R2), R5
	CMPW	R4, R5
	BNE	cmp
	ADD	$2, R0
	ADD	$2, R2
lt_2:
	TBZ	$0, R6, samebytes
one:
	MOVBU	(R0), R4
	MOVBU	(R2), R5
	CMPW	R4, R5
	BNE	ret
samebytes:
	CMP	R3, R1
	CSET	NE, R0
	CNEG	LO, R0, R0
	RET
cmpnext:
	REV	R8, R4
	REV	R9, R5
	CMP	R4, R5
	B	ret
