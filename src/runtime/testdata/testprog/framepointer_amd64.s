// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// gd Phase G.2.1: sig is func(outBuf unsafe.Pointer) *uintptr —
// argframe is 8 (outBuf) + 8 (ret) = 16. Result sits at FP+8.
TEXT	·getFP(SB), NOSPLIT|NOFRAME, $0-16
	MOVQ	BP, ret+8(FP)
	RET
