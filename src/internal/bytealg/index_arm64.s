// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "go_asm.h"
#include "textflag.h"

// gd small-string optimization: IndexString takes two 24 B string
// headers. Under ABIInternal the fork lays them out as
//   R0=a.ptr R1=a.hash R2=a.word2  R3=b.ptr R4=b.hash R5=b.word2
// whereas the stock body expects (R0=a.ptr, R1=a.len, R2=b.ptr,
// R3=b.len). The $32 local frame holds two 16 B spill buffers: for
// an inline-rep input we copy word1/word2 there and point the body
// at them; for heap rep we just decode the length. Both paths
// converge on indexStringBody<> via BL so the epilogue unwinds the
// frame.
//
// Index takes two []byte; its stock 6-reg slice ABI already lands
// bytes the right way — move R3→R2, R4→R3 and tail-call the body
// since Index itself has no frame.

// func Index(a, b []byte) int
TEXT ·Index<ABIInternal>(SB),NOSPLIT,$0-56
	MOVD	R3, R2
	MOVD	R4, R3
	B	indexStringBody<>(SB)

// func IndexString(a, b string) int
TEXT ·IndexString<ABIInternal>(SB),NOSPLIT,$32-56
	// Decode a into R0=ptr, R1=len. Inline spills to 0..15(RSP).
	CBZ	R0, is_a_inline
	AND	$0x0fffffffffffffff, R2, R1
	B	is_a_done
is_a_inline:
	MOVD	R1, 0(RSP)
	MOVD	R2, 8(RSP)
	MOVD	RSP, R0
	LSR	$60, R2, R1
is_a_done:

	// Decode b into R2=ptr, R3=len. Inline spills to 16..31(RSP).
	CBZ	R3, is_b_inline
	MOVD	R3, R2
	AND	$0x0fffffffffffffff, R5, R3
	B	is_b_done
is_b_inline:
	MOVD	R4, 16(RSP)
	MOVD	R5, 24(RSP)
	ADD	$16, RSP, R2
	LSR	$60, R5, R3
is_b_done:

	BL	indexStringBody<>(SB)
	RET

// indexStringBody is the shared Rabin-Karp-style substring search.
// Entry: R0=a.ptr (haystack), R1=a.len, R2=b.ptr (needle), R3=b.len
// (2 <= b.len <= 32). Return: R0 = index or -1. NOFRAME — either
// tail-called from a no-frame wrapper (Index) or BL'd from a framed
// wrapper (IndexString) that runs its own epilogue.
TEXT indexStringBody<>(SB),NOSPLIT|NOFRAME,$0
	// main idea is to load 'sep' into separate register(s)
	// to avoid repeatedly re-load it again and again
	// for sebsequent substring comparisons
	SUB	R3, R1, R4
	// R4 contains the start of last substring for comparison
	ADD	R0, R4, R4
	ADD	$1, R0, R8

	CMP	$8, R3
	BHI	greater_8
	TBZ	$3, R3, len_2_7
len_8:
	// R5 contains 8-byte of sep
	MOVD	(R2), R5
loop_8:
	// R6 contains substring for comparison
	CMP	R4, R0
	BHI	not_found
	MOVD.P	1(R0), R6
	CMP	R5, R6
	BNE	loop_8
	B	found
len_2_7:
	TBZ	$2, R3, len_2_3
	TBZ	$1, R3, len_4_5
	TBZ	$0, R3, len_6
len_7:
	// R5 and R6 contain 7-byte of sep
	MOVWU	(R2), R5
	// 1-byte overlap with R5
	MOVWU	3(R2), R6
loop_7:
	CMP	R4, R0
	BHI	not_found
	MOVWU.P	1(R0), R3
	CMP	R5, R3
	BNE	loop_7
	MOVWU	2(R0), R3
	CMP	R6, R3
	BNE	loop_7
	B	found
len_6:
	// R5 and R6 contain 6-byte of sep
	MOVWU	(R2), R5
	MOVHU	4(R2), R6
loop_6:
	CMP	R4, R0
	BHI	not_found
	MOVWU.P	1(R0), R3
	CMP	R5, R3
	BNE	loop_6
	MOVHU	3(R0), R3
	CMP	R6, R3
	BNE	loop_6
	B	found
len_4_5:
	TBZ	$0, R3, len_4
len_5:
	// R5 and R7 contain 5-byte of sep
	MOVWU	(R2), R5
	MOVBU	4(R2), R7
loop_5:
	CMP	R4, R0
	BHI	not_found
	MOVWU.P	1(R0), R3
	CMP	R5, R3
	BNE	loop_5
	MOVBU	3(R0), R3
	CMP	R7, R3
	BNE	loop_5
	B	found
len_4:
	// R5 contains 4-byte of sep
	MOVWU	(R2), R5
loop_4:
	CMP	R4, R0
	BHI	not_found
	MOVWU.P	1(R0), R6
	CMP	R5, R6
	BNE	loop_4
	B	found
len_2_3:
	TBZ	$0, R3, len_2
len_3:
	// R6 and R7 contain 3-byte of sep
	MOVHU	(R2), R6
	MOVBU	2(R2), R7
loop_3:
	CMP	R4, R0
	BHI	not_found
	MOVHU.P	1(R0), R3
	CMP	R6, R3
	BNE	loop_3
	MOVBU	1(R0), R3
	CMP	R7, R3
	BNE	loop_3
	B	found
len_2:
	// R5 contains 2-byte of sep
	MOVHU	(R2), R5
loop_2:
	CMP	R4, R0
	BHI	not_found
	MOVHU.P	1(R0), R6
	CMP	R5, R6
	BNE	loop_2
found:
	SUB	R8, R0, R0
	RET
not_found:
	MOVD	$-1, R0
	RET
greater_8:
	SUB	$9, R3, R11	// len(sep) - 9, offset of R0 for last 8 bytes
	CMP	$16, R3
	BHI	greater_16
len_9_16:
	MOVD.P	8(R2), R5	// R5 contains the first 8-byte of sep
	SUB	$16, R3, R7	// len(sep) - 16, offset of R2 for last 8 bytes
	MOVD	(R2)(R7), R6	// R6 contains the last 8-byte of sep
loop_9_16:
	// search the first 8 bytes first
	CMP	R4, R0
	BHI	not_found
	MOVD.P	1(R0), R7
	CMP	R5, R7
	BNE	loop_9_16
	MOVD	(R0)(R11), R7
	CMP	R6, R7		// compare the last 8 bytes
	BNE	loop_9_16
	B	found
greater_16:
	CMP	$24, R3
	BHI	len_25_32
len_17_24:
	LDP.P	16(R2), (R5, R6)	// R5 and R6 contain the first 16-byte of sep
	SUB	$24, R3, R10		// len(sep) - 24
	MOVD	(R2)(R10), R7		// R7 contains the last 8-byte of sep
loop_17_24:
	// search the first 16 bytes first
	CMP	R4, R0
	BHI	not_found
	MOVD.P	1(R0), R10
	CMP	R5, R10
	BNE	loop_17_24
	MOVD	7(R0), R10
	CMP	R6, R10
	BNE	loop_17_24
	MOVD	(R0)(R11), R10
	CMP	R7, R10		// compare the last 8 bytes
	BNE	loop_17_24
	B	found
len_25_32:
	LDP.P	16(R2), (R5, R6)
	MOVD.P	8(R2), R7	// R5, R6 and R7 contain the first 24-byte of sep
	SUB	$32, R3, R12	// len(sep) - 32
	MOVD	(R2)(R12), R10	// R10 contains the last 8-byte of sep
loop_25_32:
	// search the first 24 bytes first
	CMP	R4, R0
	BHI	not_found
	MOVD.P	1(R0), R12
	CMP	R5, R12
	BNE	loop_25_32
	MOVD	7(R0), R12
	CMP	R6, R12
	BNE	loop_25_32
	MOVD	15(R0), R12
	CMP	R7, R12
	BNE	loop_25_32
	MOVD	(R0)(R11), R12
	CMP	R10, R12	// compare the last 8 bytes
	BNE	loop_25_32
	B	found
