// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "go_asm.h"
#include "textflag.h"

// _rt0_wasm_js is not used itself. It only exists to mark the exported functions as alive.
TEXT _rt0_wasm_js(SB),NOSPLIT,$0
	I32Const $wasm_export_run(SB)
	Drop
	I32Const $wasm_export_getsp(SB)
	Drop
	I32Const $wasm_export_sched(SB)
	Drop
	I32Const $wasm_export_gorun(SB)
	Drop
	I32Const $wasm_export_nextg(SB)
	Drop
	I32Const $wasm_export_callback(SB)
	Drop
	I32Const $wasm_export_callbackdone(SB)
	Drop

// wasm_export_run gets called from JavaScript. It initializes the Go runtime, which chooses the first goroutine
// to run (see wasm_export_nextg). It does NOT follow the Go ABI. It has two WebAssembly parameters:
// R0: argc (i32)
// R1: argv (i32)
TEXT wasm_export_run(SB),NOSPLIT,$0
	MOVD $runtime·wasmStack+(m0Stack__size-16)(SB), SP

	Get SP
	Get R0 // argc
	I64ExtendI32U
	I64Store $0

	Get SP
	Get R1 // argv
	I64ExtendI32U
	I64Store $8

	I32Const $0 // entry PC_B
	Call runtime·rt0_go(SB)
	Drop

	Return

// The functions below are called from JavaScript when goroutines run on
// their own WebAssembly stacks; see jspi_js.go in package runtime.

// wasm_export_sched runs the scheduler on g0 and returns the goroutine
// to run next, or 0. It does NOT follow the Go ABI.
TEXT wasm_export_sched(SB),NOSPLIT|TOPFRAME,$0
	MOVD $runtime·g0(SB), R0
	MOVD R0, g
	MOVD g_sched+gobuf_sp(R0), SP

	CALLNORESUME runtime·jssched(SB)

	MOVD runtime·jsnextg(SB), R0
	Get R0
	I32WrapI64
	Return

// wasm_export_gorun starts goroutine gp. JavaScript calls it through
// WebAssembly.promising, so it runs on a WebAssembly stack of its own,
// and it returns, when the goroutine exits, the goroutine to run next,
// or 0. It does NOT follow the Go ABI.
// R0: gp (i32)
TEXT wasm_export_gorun(SB),NOSPLIT|TOPFRAME,$0
	Get R0
	I64ExtendI32U
	Set g
	MOVD $0, runtime·jspending(SB)
	MOVD g_sched+gobuf_sp(g), SP
	MOVD g_sched+gobuf_ctxt(g), CTXT
	MOVD $0, g_sched+gobuf_ctxt(g)

	Block
		TRYCATCH $1 // goexit1 throws this tag
			// Call the function the goroutine starts in. gostartcall
			// pushed its return address, goexit+PCQuantum.
			I32Const $0 // entry PC_B
			I64Load (g_sched+gobuf_pc)(g)
			I64Const $16
			I64ShrU
			I32WrapI64
			CallIndirect $0
			Drop

			// It returned, as if into goexit.
			I32Const $1
			Call runtime·goexit(SB)
			Drop
			UNDEF
		End
	End

	// The goroutine is done. Unless mcall's function already freed
	// it and ran the scheduler (see jsgdead), do that now, on g0.
	MOVD runtime·jsgdead(SB), R2
	Get R2
	I64Eqz
	If
		MOVD $runtime·g0(SB), R2
		MOVD R2, g
		MOVD g_sched+gobuf_sp(R2), SP

		Get SP
		I32Const $8
		I32Sub
		Set SP

		Get SP
		Get R0 // gp
		I64ExtendI32U
		I64Store $0

		CALLNORESUME runtime·jsexit(SB)
	End
	MOVD $0, runtime·jsgdead(SB)

	MOVD runtime·jsnextg(SB), R2
	Get R2
	I32WrapI64
	Return

// wasm_export_callback gets called from JavaScript when it calls a
// function made by syscall/js.FuncOf. It returns a new goroutine that
// runs the function, for JavaScript to start at once. If a goroutine was
// running, it is set aside until that one parks or exits.
TEXT wasm_export_callback(SB),NOSPLIT|TOPFRAME,$0
	MOVD SP, R0
	MOVD $runtime·g0(SB), R1
	MOVD R1, g
	MOVD g_sched+gobuf_sp(R1), SP

	Get SP
	I32Const $8
	I32Sub
	Set SP

	Get SP
	Get R0 // sp
	I64Store $0

	CALLNORESUME runtime·jscallback(SB)

	MOVD runtime·jsnextg(SB), R0
	Get R0
	I32WrapI64
	Return

// wasm_export_callbackdone gets called from JavaScript once the
// goroutine wasm_export_callback returned has parked or exited. If a
// goroutine was set aside in the middle of a call to JavaScript, it
// becomes the current goroutine again.
TEXT wasm_export_callbackdone(SB),NOSPLIT,$0
	MOVD runtime·jsinplace(SB), R0
	Get R0
	I64Eqz
	If
		Return
	End
	MOVD $0, runtime·jsinplace(SB)
	MOVD R0, g
	// The import it called was entered with sched.sp-8, see jscallback.
	I64Load (g_sched+gobuf_sp)(R0)
	I64Const $8
	I64Sub
	I32WrapI64
	Set SP
	Return

// wasm_export_nextg returns the goroutine the scheduler chose during
// wasm_export_run.
TEXT wasm_export_nextg(SB),NOSPLIT,$0
	MOVD runtime·jsnextg(SB), R0
	Get R0
	I32WrapI64
	Return

// wasm_export_getsp gets called from JavaScript to retrieve the SP.
TEXT wasm_export_getsp(SB),NOSPLIT,$0
	Get SP
	Return

TEXT runtime·exit(SB), NOSPLIT, $0-4
	I32Const $0
	Call runtime·wasmExit(SB)
	// Trap: nothing may run after the exit. wasm_exec.js expects it.
	UNDEF
