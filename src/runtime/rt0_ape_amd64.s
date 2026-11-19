// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

#include "go_asm.h"
#include "textflag.h"

// _rt0_amd64_ape is the entry point of a GOOS=ape program run as an ELF
// executable. Linux starts a process with DI zero; FreeBSD passes the
// address of argc in it.
TEXT _rt0_amd64_ape(SB),NOSPLIT,$-8
	TESTQ	DI, DI
	JNZ	freebsd
	MOVB	$const_apeLinux, runtime·apehost(SB)
	JMP	_rt0_amd64_linux(SB)
freebsd:
	MOVB	$const_apeFreebsd, runtime·apehost(SB)
	JMP	_rt0_amd64_freebsd(SB)
