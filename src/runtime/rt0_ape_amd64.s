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

// _rt0_amd64_ape_windows is the entry point of a GOOS=ape program run as
// a PE executable.
TEXT _rt0_amd64_ape_windows(SB),NOSPLIT,$-8
	MOVB	$const_apeWindows, runtime·apehost(SB)
	JMP	_rt0_amd64_windows(SB)

// A GOOS=ape program finds g as a Windows program does, on every host:
// the word at GS:tls_g points at m.tls, which holds g. On Windows, tls_g
// is the offset of a TLS slot in the TEB (wintls); on Linux and FreeBSD
// it is 0, and GS points at m.tls[1], which points at m.tls.
GLOBL runtime·tls_g+0(SB), NOPTR, $8

// settls(DI = &m.tls) makes the word at GS:tls_g point at m.tls.
TEXT runtime·settls(SB),NOSPLIT|NOFRAME,$0
	CMPB	runtime·apehost(SB), $const_apeWindows
	JNE	unix
	CMPQ	runtime·tls_g(SB), $0
	JNE	2(PC)
	CALL	runtime·windows_wintls(SB)	// keeps DI
	JMP	runtime·windows_settls(SB)
unix:
	CMPB	runtime·apehost(SB), $const_apeFreebsd
	JEQ	freebsd
	MOVQ	DI, 8(DI)	// m.tls[1] = &m.tls
	ADDQ	$8, DI
	MOVQ	DI, SI
	MOVQ	$0x1001, DI	// ARCH_SET_GS
	MOVQ	$158, AX	// SYS_arch_prctl
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1	// crash
	RET
freebsd:
	MOVQ	DI, 8(DI)	// m.tls[1] = &m.tls
	ADDQ	$8, DI
	SUBQ	$16, SP
	MOVQ	DI, 0(SP)
	MOVQ	SP, SI
	MOVQ	$131, DI	// AMD64_SET_GSBASE
	MOVQ	$165, AX	// SYS_sysarch
	SYSCALL
	ADDQ	$16, SP
	JCC	2(PC)
	MOVL	$0xf1, 0xf1	// crash
	RET
