// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build portable

#include "go_asm.h"
#include "textflag.h"

// _rt0_amd64_portable is the entry point of a GOOS=portable program run as an ELF
// executable. Linux starts a process with DI zero; FreeBSD passes the
// address of argc in it.
//
// (The assembler has GOPORTABLEHOST_linux defined if Linux is a host of the
// program, and so on: a program is for all of GOOS=portable's hosts or for
// some. On a host it is not for, it stops.)
TEXT _rt0_amd64_portable(SB),NOSPLIT,$-8
	TESTQ	DI, DI
	JNZ	freebsd
#ifdef GOPORTABLEHOST_linux
	MOVB	$const_portableLinux, runtime·portablehost(SB)
	JMP	_rt0_amd64_linux(SB)
#else
	MOVL	$1, DI
	MOVL	$231, AX	// exit_group
	SYSCALL
	INT	$3
#endif
freebsd:
#ifdef GOPORTABLEHOST_freebsd
	MOVB	$const_portableFreebsd, runtime·portablehost(SB)
	JMP	_rt0_amd64_freebsd(SB)
#else
	MOVL	$1, DI
	MOVL	$1, AX	// exit
	SYSCALL
	INT	$3
#endif

// _rt0_amd64_portable_windows is the entry point of a GOOS=portable program run as
// a PE executable.
TEXT _rt0_amd64_portable_windows(SB),NOSPLIT,$-8
#ifdef GOPORTABLEHOST_windows
	MOVB	$const_portableWindows, runtime·portablehost(SB)
	JMP	_rt0_amd64_windows(SB)
#else
	INT	$3
#endif

// _rt0_amd64_portable_darwin is the entry point of a GOOS=portable program on
// darwin: the loader of the program's file (cmd/internal/portpack/loader)
// calls it as main(argc, argv, nil, file).
TEXT _rt0_amd64_portable_darwin(SB),NOSPLIT,$-8
	MOVB	$const_portableDarwin, runtime·portablehost(SB)
	MOVQ	CX, runtime·portableFile(SB)
	JMP	runtime·rt0_go(SB)	// with argc in DI and argv in SI

// A GOOS=portable program finds g as a Windows program does, on every host:
// the word at GS:tls_g points at m.tls, which holds g. On Windows, tls_g
// is the offset of a TLS slot in the TEB (wintls); on Linux and FreeBSD
// it is 0, and GS points at m.tls[1], which points at m.tls; on darwin
// it is 0x30, the offset of the slot of the thread's data that the
// system keeps for Go (where a darwin program has g itself).
GLOBL runtime·tls_g+0(SB), NOPTR, $8

// settls(DI = &m.tls) makes the word at GS:tls_g point at m.tls.
TEXT runtime·settls(SB),NOSPLIT|NOFRAME,$0
#ifdef GOPORTABLEHOST_windows
	CMPB	runtime·portablehost(SB), $const_portableWindows
	JNE	unix
	CMPQ	runtime·tls_g(SB), $0
	JNE	2(PC)
	CALL	runtime·windows_wintls(SB)	// keeps DI
	JMP	runtime·windows_settls(SB)
unix:
#endif
	CMPB	runtime·portablehost(SB), $const_portableFreebsd
	JEQ	freebsd
	CMPB	runtime·portablehost(SB), $const_portableDarwin
	JNE	linux
	MOVQ	$0x30, runtime·tls_g(SB)
	MOVQ	DI, 0x30(GS)
	RET
linux:
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
