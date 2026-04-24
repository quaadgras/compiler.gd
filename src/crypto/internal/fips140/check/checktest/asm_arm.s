// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !purego

#include "textflag.h"

DATA StaticData<>(SB)/4, $10
GLOBL StaticData<>(SB), NOPTR, $4

TEXT StaticText<>(SB), $0
	RET

// gd Phase G.2.1: sig is func(outBuf unsafe.Pointer) *uint32 —
// argframe is 4 (outBuf) + 4 (ret) = 8. Result sits at FP+4.
TEXT ·PtrStaticData(SB), $0-8
	MOVW $StaticData<>(SB), R1
	MOVW R1, ret+4(FP)
	RET

TEXT ·PtrStaticText(SB), $0-4
	MOVW $StaticText<>(SB), R1
	MOVW R1, ret+0(FP)
	RET
