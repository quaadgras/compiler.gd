// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "go_asm.h"
#include "go_tls.h"
#include "funcdata.h"
#include "textflag.h"

// fastcbentry is the direct C-ABI virtual-call entry for resident-callback
// mode (see fastcbArmEntry in cgocall.go). The engine calls it with the
// GDExtensionClassCallVirtualWithData arguments in System V registers:
//
//	DI = p_instance   SI = p_name (unused)   DX = p_virtual_call_userdata
//	CX = p_args       R8 = r_ret
//
// Fast-path preconditions, checked before anything is committed (any failure
// tail-jumps, arguments untouched, to the stock entry in fastcbEntryFallback,
// which performs the full crosscall2/cgocallback protocol): this thread has a
// TLS g, its m is the resident m, and the resident goroutine is _Grunning —
// i.e. residency is engaged and not mid-yield. A transient _Gscanrunning is
// admitted for the same reason as the resident fast path in cgocallbackg:
// that is suspendG pinning the status word while it posts a preemption
// request, which the callback's first prologue will honor.
//
// The goroutine-stack frame is arranged exactly like cgocallback's: the
// resident g's sched.pc is pushed as a fake return PC at exactly SP+spdelta,
// where spdelta is the SP delta the assembler tracked up to the CALL (six
// PUSHQs = 48) — the unwinder reads the frame's return PC at that offset, so
// the fake PC must land there and the manual frame is 56 bytes total. The
// unwinder trusts the delta despite the SP write because this function
// carries FuncID_cgocallback (cmd/internal/objabi/funcid.go), so GC stack
// scans and tracebacks walk seamlessly from the callback's frames into the
// frames that entered C; NO_LOCAL_POINTERS gives copystack the (empty)
// locals map it needs to adjust nothing in the frame. On return, sched.sp/pc
// are recomputed from SP rather than restored from saved absolutes — the
// callback may have grown and therefore MOVED the stack; copystack adjusts
// g.sched and the copied frame, and this recomputation (cgocallback's own
// trick) is correct either way.
TEXT runtime·fastcbentry(SB),NOSPLIT|NOFRAME,$0
	NO_LOCAL_POINTERS
	get_tls(R9)
	MOVQ	g(R9), R10
	TESTQ	R10, R10
	JZ	fallback
	MOVQ	g_m(R10), R11
	CMPQ	runtime·fastcbM(SB), R11
	JNE	fallback
	MOVQ	m_curg(R11), R10
	MOVL	g_atomicstatus(R10), AX
	CMPL	AX, $2			// _Grunning
	JE	commit
	CMPL	AX, $0x1002		// _Gscanrunning
	JNE	fallback

commit:
	// Save the C callee-saved registers we may clobber. Six pushes =
	// 48 bytes of tracked SP delta: the unwinder reads this frame's
	// return PC at SP+48, so the fake return PC below MUST sit exactly
	// there.
	PUSHQ	BX
	PUSHQ	BP
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	PUSHQ	R15

	// Enter the Go execution context: TLS g = curg (morestack and the
	// signal path reload g from TLS), R14 = curg (ABIInternal g
	// register), X15 = 0 (ABIInternal zero register; caller-saved in
	// the C ABI, so neither save nor restore is needed).
	MOVQ	R10, g(R9)
	MOVQ	R10, R14
	XORPS	X15, X15

	// Point g0.sched.sp below the LIVE C frames for the duration of the
	// callback, exactly as cgocallback does. Any system-stack excursion
	// during the dispatch (morestack, a preemption's trip through the
	// scheduler, GC assists) resumes g0 at g0.sched.sp — which still
	// points at where asmcgocall (or the last stock base entry) left
	// it, ABOVE the engine's live C frames that called us. Without
	// this, the scheduler runs on top of those frames and corrupts
	// them. The old value is stashed in the goroutine-stack frame
	// (slot 40) and restored on the way out, so stock entries and
	// nested thunk entries interleave correctly.
	MOVQ	m_g0(R11), BX		// g0
	MOVQ	(g_sched+gobuf_sp)(BX), R15
	MOVQ	SP, (g_sched+gobuf_sp)(BX)

	// Switch to the resident goroutine's stack at sched.sp — kept at
	// the correct resume point by cgocallback's exit (stock base
	// entries), gosave_systemstack_switch in asmcgocall (outbound
	// fastcbCallC nesting), and our own exit sequence below. BP is set
	// to sched.bp so frame-pointer unwinders chain into the outer
	// frames.
	MOVQ	(g_sched+gobuf_sp)(R10), R13
	MOVQ	(g_sched+gobuf_pc)(R10), AX
	MOVQ	AX, -8(R13)		// fake return PC (at SP+48 below)
	MOVQ	(g_sched+gobuf_bp)(R10), BP
	MOVQ	SP, R12			// C stack pointer
	LEAQ	-56(R13), SP		// open the 56-byte frame
	MOVQ	DI, 0(SP)		// instance
	MOVQ	DX, 8(SP)		// userdata
	MOVQ	CX, 16(SP)		// args
	MOVQ	R8, 24(SP)		// ret
	MOVQ	R12, 32(SP)		// saved C SP
	MOVQ	R15, 40(SP)		// saved g0.sched.sp
					// Slots 32/40 hold raw pointers into
					// the C stack: copystack never adjusts
					// them (empty locals map) — and the C
					// stack never moves.
	MOVQ	$runtime·fastcbentrygo(SB), AX
	CALL	AX			// indirect to bypass nosplit analysis

	// Restore sched from SP (recompute — the stack may have moved).
	LEAQ	56(SP), AX
	MOVQ	AX, (g_sched+gobuf_sp)(R14)
	MOVQ	48(SP), AX
	MOVQ	AX, (g_sched+gobuf_pc)(R14)
	MOVQ	32(SP), R12		// saved C SP
	MOVQ	40(SP), R15		// saved g0.sched.sp

	// Leave the Go context: restore g0.sched.sp, TLS g back to g0,
	// back onto the C stack, restore the C callee-saved registers.
	MOVQ	g_m(R14), R11
	MOVQ	m_g0(R11), R10
	MOVQ	R15, (g_sched+gobuf_sp)(R10)
	get_tls(R9)
	MOVQ	R10, g(R9)
	MOVQ	R12, SP
	POPQ	R15
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	BP
	POPQ	BX
	RET

fallback:
	MOVQ	runtime·fastcbEntryFallback(SB), AX
	TESTQ	AX, AX
	JZ	drop
	JMP	AX
drop:
	RET
