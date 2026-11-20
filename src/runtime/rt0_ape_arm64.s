// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

#include "go_asm.h"
#include "textflag.h"

// _rt0_arm64_ape is the entry point of a GOOS=ape program run as an ELF
// executable: on Linux.
TEXT _rt0_arm64_ape(SB),NOSPLIT,$0
	MOVD	$const_apeLinux, R2
	MOVB	R2, runtime·apehost(SB)
	JMP	_rt0_arm64_linux(SB)

// _rt0_arm64_ape_darwin is the entry point of a GOOS=ape program run as
// a Mach-O executable: by dyld, as main(argc, argv, envp, apple), or by
// the loader of the program's file (cmd/internal/apepack/loader), as
// main(argc, argv, nil, file).
TEXT _rt0_arm64_ape_darwin(SB),NOSPLIT,$0
	CBNZ	R2, 2(PC)
	MOVD	R3, runtime·apeFile(SB)
	MOVD	$const_apeDarwin, R2
	MOVB	R2, runtime·apehost(SB)
	JMP	_rt0_arm64_darwin(SB)
