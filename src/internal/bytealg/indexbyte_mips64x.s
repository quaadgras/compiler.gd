// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gd SSO: stock 16 B string ABI, no inline-rep prolog. The Go
// fallback in indexbyte_generic.go serves mips64/mips64le until
// this is ported.

//go:build ignore

#include "go_asm.h"
#include "textflag.h"

TEXT ·IndexByte(SB),NOSPLIT,$0-40
	MOVV	b_base+0(FP), R1
	MOVV	b_len+8(FP), R2
	MOVBU	c+24(FP), R3	// byte to find
	MOVV	R1, R4		// store base for later
	ADDV	R1, R2		// end
	ADDV	$-1, R1

loop:
	ADDV	$1, R1
	BEQ	R1, R2, notfound
	MOVBU	(R1), R5
	BNE	R3, R5, loop

	SUBV	R4, R1		// remove base
	MOVV	R1, ret+32(FP)
	RET

notfound:
	MOVV	$-1, R1
	MOVV	R1, ret+32(FP)
	RET

TEXT ·IndexByteString(SB),NOSPLIT,$0-32
	MOVV	s_base+0(FP), R1
	MOVV	s_len+8(FP), R2
	MOVBU	c+16(FP), R3	// byte to find
	MOVV	R1, R4		// store base for later
	ADDV	R1, R2		// end
	ADDV	$-1, R1

loop:
	ADDV	$1, R1
	BEQ	R1, R2, notfound
	MOVBU	(R1), R5
	BNE	R3, R5, loop

	SUBV	R4, R1		// remove base
	MOVV	R1, ret+24(FP)
	RET

notfound:
	MOVV	$-1, R1
	MOVV	R1, ret+24(FP)
	RET
