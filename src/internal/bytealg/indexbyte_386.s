// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "go_asm.h"
#include "textflag.h"

TEXT ·IndexByte(SB),NOSPLIT,$0-20
	MOVL	b_base+0(FP), SI
	MOVL	b_len+4(FP), CX
	MOVB	c+12(FP), AL
	MOVL	SI, DI
	CLD; REPN; SCASB
	JZ 3(PC)
	MOVL	$-1, ret+16(FP)
	RET
	SUBL	SI, DI
	SUBL	$1, DI
	MOVL	DI, ret+16(FP)
	RET

// gd: a string is {ptr, hash, len} (doc/gd/sso-string.md; with 4-byte
// words it has no inline form), so its length is at 8.
TEXT ·IndexByteString(SB),NOSPLIT,$0-20
	MOVL	s_base+0(FP), SI
	MOVL	s_len+8(FP), CX
	MOVB	c+12(FP), AL
	MOVL	SI, DI
	CLD; REPN; SCASB
	JZ 3(PC)
	MOVL	$-1, ret+16(FP)
	RET
	SUBL	SI, DI
	SUBL	$1, DI
	MOVL	DI, ret+16(FP)
	RET
