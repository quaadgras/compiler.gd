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
// argframe is 8 (outBuf) + 8 (ret) = 16. Result sits at FP+8.
TEXT ·PtrStaticData(SB), $0-16
	MOVD $StaticData<>(SB), R1
	MOVD R1, ret+8(FP)
	RET

TEXT ·PtrStaticText(SB), $0-8
	MOVD $StaticText<>(SB), R1
	MOVD R1, ret+0(FP)
	RET
