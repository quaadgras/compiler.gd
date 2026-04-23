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

// gd: under the fork's 24 B string layout, args come in as
//   R0=a.ptr R1=a.hash R2=a.word2  R3=b.ptr R4=b.hash R5=b.word2
// whereas cmpbody expects (R0=a_base, R1=a_len, R2=b_base, R3=b_len).
// Decode each string: heap rep → len from word 2 low 60 bits; inline
// rep → spill words 1+2 to a local 16 B buffer, point at the buffer,
// take length from word 2's top nibble. 32 B local frame for both
// strings; BL + RET so the epilogue restores SP.
TEXT runtime·cmpstring<ABIInternal>(SB),NOSPLIT,$32-56
	// Decode a: R0=ptr, R1=len on exit.
	CBZ	R0, cs_a_inline
	AND	$0x0fffffffffffffff, R2, R1
	B	cs_a_done
cs_a_inline:
	MOVD	R1, 0(RSP)
	MOVD	R2, 8(RSP)
	MOVD	RSP, R0
	LSR	$60, R2, R1
cs_a_done:

	// Decode b: R2=ptr, R3=len on exit.
	CBZ	R3, cs_b_inline
	MOVD	R3, R2
	AND	$0x0fffffffffffffff, R5, R3
	B	cs_b_done
cs_b_inline:
	MOVD	R4, 16(RSP)
	MOVD	R5, 24(RSP)
	ADD	$16, RSP, R2
	LSR	$60, R5, R3
cs_b_done:

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
