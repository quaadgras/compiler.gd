// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm.jspi

#include "go_asm.h"
#include "funcdata.h"
#include "textflag.h"

// Goroutine switching on js with JSPI, see jspi_js.go.

// func gogo(buf *gobuf)
// Record buf.g as the goroutine for JavaScript to run next and return.
// See jspi_js.go.
TEXT runtime·gogo(SB), NOSPLIT, $0-8
	MOVD buf+0(FP), R0
	MOVD gobuf_g(R0), R1
	MOVD 0(R1), R2	// make sure g != nil
	MOVD R1, runtime·jsnextg(SB)
	MOVD R1, runtime·jspending(SB)
	RET

// func mcall(fn func(*g))
// Switch to m->g0's stack and call fn(g), which ends in the scheduler.
// The scheduler's gogo records the goroutine to run next and returns
// here. If that is another goroutine, park this WebAssembly stack until
// the scheduler chooses g again.
TEXT runtime·mcall(SB), NOSPLIT, $0-8
	// CTXT = fn
	MOVD fn+0(FP), CTXT
	// R1 = g.m
	MOVD g_m(g), R1
	// R2 = g0
	MOVD m_g0(R1), R2
	// R3 = g
	MOVD g, R3

	// save state in g->sched
	MOVD 0(SP), g_sched+gobuf_pc(g)     // caller's PC
	MOVD $fn+0(FP), g_sched+gobuf_sp(g) // caller's SP

	// if g == g0 call badmcall
	Get g
	Get R2
	I64Eq
	If
		JMP runtime·badmcall(SB)
	End

	MOVD $0, runtime·jsnextg(SB)

	// switch to g0's stack, leaving room for jspark's arguments
	I64Load (g_sched+gobuf_sp)(R2)
	I64Const $16
	I64Sub
	I32WrapI64
	Set SP

	// set arg to current g
	MOVD g, 0(SP)

	// switch to g0
	MOVD R2, g

	// call fn
	Get CTXT
	I32WrapI64
	I64Load $0
	CALL

	// If fn destroyed g, unwind this stack to wasm_export_gorun.
	MOVD runtime·jsgdead(SB), R4
	Get R4
	I64Eqz
	I32Eqz
	If
		THROW $1
	End

	// R4 = the goroutine to run next, or 0
	MOVD runtime·jsnextg(SB), R4

	Get R4
	Get R3
	I64Ne
	If
		// Park until the scheduler chooses g again. Nothing below
		// reads g0's stack, which other goroutines use meanwhile.
		I64Load (g_sched+gobuf_sp)(R2)
		I64Const $16
		I64Sub
		Set R5
		MOVD R5, SP
		MOVD R3, 0(R5)
		MOVD R4, 8(R5)
		CALLNORESUME runtime·jspark(SB)
	End

	// g runs. Its stack may have moved while it was parked.
	MOVD R3, g
	MOVD $0, runtime·jspending(SB)
	MOVD g_sched+gobuf_sp(R3), SP
	I32Const $0
	Return

// func jsthrowgoexit()
TEXT runtime·jsthrowgoexit(SB), NOSPLIT, $0-0
	THROW $1
	UNDEF

// func jsthrowrecover()
TEXT runtime·jsthrowrecover(SB), NOSPLIT, $0-0
	MOVD $0, runtime·jsnextg(SB)
	MOVD $0, runtime·jspending(SB)
	THROW $0
	UNDEF

// func jsgoexit(p *_panic)
TEXT runtime·jsgoexit(SB), NOSPLIT, $8-8
	// The frame's only slot is the argument of goexitDefers.
	NO_LOCAL_POINTERS
	// R1 = offset of the caller's frame from the top of the stack,
	// which identifies it even if the stack moves.
	MOVD g_stack+stack_hi(g), R1
	MOVD $p+0(FP), R2
	Get R1
	Get R2
	I64Sub
	Set R1

	Loop
		Block
			TRYCATCH $0
				MOVD p+0(FP), R0
				MOVD R0, 0(SP)
				CALLNORESUME runtime·goexitDefers(SB)
				RET
			End
		End

		// A recovered panic is unwinding. If it does not select the
		// caller's frame, keep unwinding.
		MOVD g_stack+stack_hi(g), R2
		MOVD g_sched+gobuf_sp(g), R3
		Get R2
		Get R3
		I64Sub
		Get R1
		I64Ne
		If
			THROW $0
		End

		// Restore SP: this frame is 8 bytes and a return address
		// below the caller's.
		Get R3
		I64Const $16
		I64Sub
		I32WrapI64
		Set SP
		Br $0
	End
	UNDEF
