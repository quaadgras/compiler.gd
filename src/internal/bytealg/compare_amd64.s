// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "go_asm.h"
#include "asm_amd64.h"
#include "textflag.h"

TEXT ·Compare<ABIInternal>(SB),NOSPLIT,$0-56
	// AX = a_base (want in SI)
	// BX = a_len  (want in BX)
	// CX = a_cap  (unused)
	// DI = b_base (want in DI)
	// SI = b_len  (want in DX)
	// R8 = b_cap  (unused)
	MOVQ	SI, DX
	MOVQ	AX, SI
	JMP	cmpbody<>(SB)

// gd small-string optimization: each string is 24 B (ptr, hash, len),
// passed in 3 int regs. cmpstring(a, b string) int register layout is:
//   a.ptr=AX, a.hash=BX, a.len=CX, b.ptr=DI, b.hash=SI, b.len=R8.
// cmpbody wants SI=a_ptr, BX=a_len, DI=b_ptr, DX=b_len.
//
// For inline-rep strings word 0 (ptr) is nil and the 15 data bytes live
// inside the header (word1 || low 7 B of word2). When BOTH operands are
// inline we stay entirely in registers via a BSWAPQ-and-compare fast
// path; otherwise we spill the inline side's header to the local frame
// and repoint the data pointer before dispatching to cmpbody. 32 B of
// local frame: 16 B spill slot each for a (0..15) and b (16..31).
TEXT runtime·cmpstring<ABIInternal>(SB),NOSPLIT,$32-56
	// BOTH-INLINE fast path: OR word 0 of both operands. If the combined
	// value is zero, neither operand has a heap data pointer — the bytes
	// live in the 24 B header. Skip the stack spill + cmpbody call and
	// decide the ordering with two BSWAPQ + CMPQ pairs in registers.
	//
	// Correctness: bytes 0..7 sit in word 1 little-endian, bytes 8..14 in
	// word 2 low 56 bits little-endian, length in word 2 top nibble, the
	// nibble at bits 56..59 is always zero. BSWAPQ reverses byte order so
	// byte 0 lands in the MSB; an unsigned compare on the reversed value
	// is lexicographic byte order over bytes 0..7 / 8..14. Trailing-zero
	// padding can't confuse the result because (a) positions past each
	// length are zero on both sides when content through the shared
	// prefix matches, and (b) if content through the prefix truly matches
	// and one string is shorter, its word 2 has a smaller length tag —
	// and BSWAPQ places that tag at the LSB of word 2, so the unsigned
	// compare breaks the tie in favour of the longer string (i.e. the
	// shorter string compares less).
	MOVQ	AX, R9
	ORQ	DI, R9
	JNE	cmpstr_any_heap
	BSWAPQ	BX
	BSWAPQ	SI
	CMPQ	BX, SI
	JB	cmpstr_ii_less
	JA	cmpstr_ii_greater
	BSWAPQ	CX
	BSWAPQ	R8
	CMPQ	CX, R8
	JB	cmpstr_ii_less
	JA	cmpstr_ii_greater
	XORL	AX, AX
	RET
cmpstr_ii_less:
	MOVQ	$-1, AX
	RET
cmpstr_ii_greater:
	MOVQ	$1, AX
	RET

cmpstr_any_heap:
	TESTQ	AX, AX
	JNE	cmpstr_a_heap
	MOVQ	BX, 0(SP)
	MOVQ	CX, 8(SP)
	LEAQ	0(SP), AX
	SHRQ	$60, CX   // CX = tag = real length (inline)
cmpstr_a_heap:
	TESTQ	DI, DI
	JNE	cmpstr_b_heap
	MOVQ	SI, 16(SP)
	MOVQ	R8, 24(SP)
	LEAQ	16(SP), DI
	SHRQ	$60, R8
cmpstr_b_heap:
	MOVQ	AX, SI    // SI = a.ptr
	MOVQ	CX, BX    // BX = a.len
	MOVQ	R8, DX    // DX = b.len
	// cmpbody is tail-called via CALL+RET (not JMP) because cmpstring
	// now owns a $32 local frame that must be unwound on return.
	CALL	cmpbody<>(SB)
	RET

// input:
//   SI = a
//   DI = b
//   BX = alen
//   DX = blen
// output:
//   AX = output (-1/0/1)
TEXT cmpbody<>(SB),NOSPLIT,$0-0
	CMPQ	SI, DI
	JEQ	allsame
	CMPQ	BX, DX
	MOVQ	DX, R8
	CMOVQLT	BX, R8 // R8 = min(alen, blen) = # of bytes to compare
	CMPQ	R8, $8
	JB	small

	CMPQ	R8, $63
	JBE	loop
#ifndef hasAVX2
	CMPB	internal∕cpu·X86+const_offsetX86HasAVX2(SB), $1
	JEQ     big_loop_avx2
	JMP	big_loop
#else
	JMP	big_loop_avx2
#endif
loop:
	CMPQ	R8, $16
	JBE	_0through16
	MOVOU	(SI), X0
	MOVOU	(DI), X1
	PCMPEQB X0, X1
	PMOVMSKB X1, AX
	XORQ	$0xffff, AX	// convert EQ to NE
	JNE	diff16	// branch if at least one byte is not equal
	ADDQ	$16, SI
	ADDQ	$16, DI
	SUBQ	$16, R8
	JMP	loop

diff64:
	ADDQ	$48, SI
	ADDQ	$48, DI
	JMP	diff16
diff48:
	ADDQ	$32, SI
	ADDQ	$32, DI
	JMP	diff16
diff32:
	ADDQ	$16, SI
	ADDQ	$16, DI
	// AX = bit mask of differences
diff16:
	BSFQ	AX, BX	// index of first byte that differs
	XORQ	AX, AX
	MOVB	(SI)(BX*1), CX
	CMPB	CX, (DI)(BX*1)
	SETHI	AX
	LEAQ	-1(AX*2), AX	// convert 1/0 to +1/-1
	RET

	// 0 through 16 bytes left, alen>=8, blen>=8
_0through16:
	CMPQ	R8, $8
	JBE	_0through8
	MOVQ	(SI), AX
	MOVQ	(DI), CX
	CMPQ	AX, CX
	JNE	diff8
_0through8:
	MOVQ	-8(SI)(R8*1), AX
	MOVQ	-8(DI)(R8*1), CX
	CMPQ	AX, CX
	JEQ	allsame

	// AX and CX contain parts of a and b that differ.
diff8:
	BSWAPQ	AX	// reverse order of bytes
	BSWAPQ	CX
	XORQ	AX, CX
	BSRQ	CX, CX	// index of highest bit difference
	SHRQ	CX, AX	// move a's bit to bottom
	ANDQ	$1, AX	// mask bit
	LEAQ	-1(AX*2), AX // 1/0 => +1/-1
	RET

	// 0-7 bytes in common
small:
	LEAQ	(R8*8), CX	// bytes left -> bits left
	NEGQ	CX		//  - bits lift (== 64 - bits left mod 64)
	JEQ	allsame

	// load bytes of a into high bytes of AX
	CMPB	SI, $0xf8
	JA	si_high
	MOVQ	(SI), SI
	JMP	si_finish
si_high:
	MOVQ	-8(SI)(R8*1), SI
	SHRQ	CX, SI
si_finish:
	SHLQ	CX, SI

	// load bytes of b in to high bytes of BX
	CMPB	DI, $0xf8
	JA	di_high
	MOVQ	(DI), DI
	JMP	di_finish
di_high:
	MOVQ	-8(DI)(R8*1), DI
	SHRQ	CX, DI
di_finish:
	SHLQ	CX, DI

	BSWAPQ	SI	// reverse order of bytes
	BSWAPQ	DI
	XORQ	SI, DI	// find bit differences
	JEQ	allsame
	BSRQ	DI, CX	// index of highest bit difference
	SHRQ	CX, SI	// move a's bit to bottom
	ANDQ	$1, SI	// mask bit
	LEAQ	-1(SI*2), AX // 1/0 => +1/-1
	RET

allsame:
	XORQ	AX, AX
	XORQ	CX, CX
	CMPQ	BX, DX
	SETGT	AX	// 1 if alen > blen
	SETEQ	CX	// 1 if alen == blen
	LEAQ	-1(CX)(AX*2), AX	// 1,0,-1 result
	RET

	// this works for >= 64 bytes of data.
#ifndef hasAVX2
big_loop:
	MOVOU	(SI), X0
	MOVOU	(DI), X1
	PCMPEQB X0, X1
	PMOVMSKB X1, AX
	XORQ	$0xffff, AX
	JNE	diff16

	MOVOU	16(SI), X0
	MOVOU	16(DI), X1
	PCMPEQB X0, X1
	PMOVMSKB X1, AX
	XORQ	$0xffff, AX
	JNE	diff32

	MOVOU	32(SI), X0
	MOVOU	32(DI), X1
	PCMPEQB X0, X1
	PMOVMSKB X1, AX
	XORQ	$0xffff, AX
	JNE	diff48

	MOVOU	48(SI), X0
	MOVOU	48(DI), X1
	PCMPEQB X0, X1
	PMOVMSKB X1, AX
	XORQ	$0xffff, AX
	JNE	diff64

	ADDQ	$64, SI
	ADDQ	$64, DI
	SUBQ	$64, R8
	CMPQ	R8, $64
	JBE	loop
	JMP	big_loop
#endif

	// Compare 64-bytes per loop iteration.
	// Loop is unrolled and uses AVX2.
big_loop_avx2:
	VMOVDQU	(SI), Y2
	VMOVDQU	(DI), Y3
	VMOVDQU	32(SI), Y4
	VMOVDQU	32(DI), Y5
	VPCMPEQB Y2, Y3, Y0
	VPMOVMSKB Y0, AX
	XORL	$0xffffffff, AX
	JNE	diff32_avx2
	VPCMPEQB Y4, Y5, Y6
	VPMOVMSKB Y6, AX
	XORL	$0xffffffff, AX
	JNE	diff64_avx2

	ADDQ	$64, SI
	ADDQ	$64, DI
	SUBQ	$64, R8
	CMPQ	R8, $64
	JB	big_loop_avx2_exit
	JMP	big_loop_avx2

	// Avoid AVX->SSE transition penalty and search first 32 bytes of 64 byte chunk.
diff32_avx2:
	VZEROUPPER
	JMP diff16

	// Same as diff32_avx2, but for last 32 bytes.
diff64_avx2:
	VZEROUPPER
	JMP diff48

	// For <64 bytes remainder jump to normal loop.
big_loop_avx2_exit:
	VZEROUPPER
	JMP loop
