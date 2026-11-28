// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The calls of the runtime's checks of C's pointers that keep the
// registers (cmd/compile's LoweredC23Keep): the compiled code saves and
// restores nothing around them, and the call is not one where the
// goroutine may stop, so its frame has no map of its pointers there.
//
// What makes that safe while the frame's values are only in registers:
//   - m.locks is held for the call, so the goroutine is not preempted
//     and its stack is not scanned or shrunk until it returns;
//   - the function that calls a stub left room for it in its stack
//     check, so the check never grows the stack (newstack throws if it
//     would, with m.cKeep set);
//   - the checks called here neither claim words nor stop the world
//     for a repair: pointer arithmetic, and the conversion to a type
//     without pointers of what is only loaded.
//
// The pointer is in AX and the result is returned there; every other
// general register is kept. The vector registers are not (the compiler
// knows that they are clobbered), and X15 is 0 as for any call.

#include "go_asm.h"
#include "textflag.h"

// checkptrArithmeticC1(AX, BX)
TEXT runtime·cKeepArith<ABIInternal>(SB),NOSPLIT,$120-0
	MOVQ	BX, 24(SP)
	MOVQ	CX, 32(SP)
	MOVQ	DX, 40(SP)
	MOVQ	SI, 48(SP)
	MOVQ	DI, 56(SP)
	MOVQ	R8, 64(SP)
	MOVQ	R9, 72(SP)
	MOVQ	R10, 80(SP)
	MOVQ	R11, 88(SP)
	MOVQ	R12, 96(SP)
	MOVQ	R13, 104(SP)
	MOVQ	R15, 112(SP)
	MOVQ	g_m(R14), R12
	INCL	m_locks(R12)
	INCL	m_cKeep(R12)
	MOVQ	96(SP), R12
	CALL	runtime·checkptrArithmeticC1<ABIInternal>(SB)
	MOVQ	g_m(R14), R12
	DECL	m_cKeep(R12)
	DECL	m_locks(R12)
	JNE	restore
	// As releasem: a request to preempt that newstack put off.
	CMPB	g_preempt(R14), $0
	JEQ	restore
	MOVQ	$0xfffffffffffffade, R12 // stackPreempt
	MOVQ	R12, g_stackguard0(R14)
restore:
	MOVQ	24(SP), BX
	MOVQ	32(SP), CX
	MOVQ	40(SP), DX
	MOVQ	48(SP), SI
	MOVQ	56(SP), DI
	MOVQ	64(SP), R8
	MOVQ	72(SP), R9
	MOVQ	80(SP), R10
	MOVQ	88(SP), R11
	MOVQ	96(SP), R12
	MOVQ	104(SP), R13
	MOVQ	112(SP), R15
	RET

// checkptrConvCLoad(AX, BX, CX), for an elem without pointers
TEXT runtime·cKeepConvLoad<ABIInternal>(SB),NOSPLIT,$120-0
	MOVQ	BX, 24(SP)
	MOVQ	CX, 32(SP)
	MOVQ	DX, 40(SP)
	MOVQ	SI, 48(SP)
	MOVQ	DI, 56(SP)
	MOVQ	R8, 64(SP)
	MOVQ	R9, 72(SP)
	MOVQ	R10, 80(SP)
	MOVQ	R11, 88(SP)
	MOVQ	R12, 96(SP)
	MOVQ	R13, 104(SP)
	MOVQ	R15, 112(SP)
	MOVQ	g_m(R14), R12
	INCL	m_locks(R12)
	INCL	m_cKeep(R12)
	MOVQ	96(SP), R12
	CALL	runtime·checkptrConvCLoad<ABIInternal>(SB)
	MOVQ	g_m(R14), R12
	DECL	m_cKeep(R12)
	DECL	m_locks(R12)
	JNE	restore
	// As releasem: a request to preempt that newstack put off.
	CMPB	g_preempt(R14), $0
	JEQ	restore
	MOVQ	$0xfffffffffffffade, R12 // stackPreempt
	MOVQ	R12, g_stackguard0(R14)
restore:
	MOVQ	24(SP), BX
	MOVQ	32(SP), CX
	MOVQ	40(SP), DX
	MOVQ	48(SP), SI
	MOVQ	56(SP), DI
	MOVQ	64(SP), R8
	MOVQ	72(SP), R9
	MOVQ	80(SP), R10
	MOVQ	88(SP), R11
	MOVQ	96(SP), R12
	MOVQ	104(SP), R13
	MOVQ	112(SP), R15
	RET
