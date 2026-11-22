// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

#include "textflag.h"

// func memHashInlineAES(w1, w0, h uintptr) uintptr
//
// The hash of the inline string whose words are w0 and w1 (1 to 15 bytes:
// doc/gd/sso-string.md), as memHashAES hashes its bytes, from registers:
// bytes 0..7 are w1, 8..14 w0 below the tag.
TEXT ·memHashInlineAES<ABIInternal>(SB),NOSPLIT,$0-32
	// AX = w1, BX = w0, CX = seed
	MOVQ	BX, DX
	SHRQ	$56, DX
	ANDL	$15, DX			// DX = length
	MOVQ	CX, X0			// 64 bits of per-table hash seed
	PINSRW	$4, DX, X0		// 16 bits of length
	PSHUFHW	$0, X0, X0		// repeat length 4 times total
	PXOR	·aeskeysched(SB), X0	// xor in per-process seed
	AESENC	X0, X0			// scramble seed

	// Clear the bytes from the length on, and the tag.
	MOVQ	DX, CX
	CMPQ	CX, $8
	JAE	high
	SHLQ	$3, CX
	MOVQ	$1, SI
	SHLQ	CX, SI
	DECQ	SI
	ANDQ	SI, AX
	XORL	BX, BX
	JMP	masked
high:
	SUBQ	$8, CX
	SHLQ	$3, CX
	MOVQ	$1, SI
	SHLQ	CX, SI
	DECQ	SI
	ANDQ	SI, BX
masked:
	MOVQ	AX, X1
	PINSRQ	$1, BX, X1
	PXOR	X0, X1			// xor data with seed
	AESENC	X1, X1			// scramble combo 3 times
	AESENC	X1, X1
	AESENC	X1, X1
	MOVQ	X1, AX
	RET
