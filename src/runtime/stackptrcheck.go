// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// GODEBUG=stackptrcheck=1 makes the garbage collector check, as it
// marks, two properties of pointers into goroutine stacks that escape
// analysis is there to guarantee (doc/gd/escape-bits.md, Safety
// properties), and throw when one doesn't hold:
//
//   - No pointer found anywhere points into the part of a goroutine's
//     stack below its stack pointer: the frames that were there have
//     returned. (Checked for goroutines that are not running, whose
//     stack pointer is known.)
//
//   - No pointer in a frame, or in a stack object of a frame, points
//     into its own stack below that frame: a frame that has returned,
//     or one of a callee, which returns first.
//
// A pointer that the heap or a global holds into a live frame is not
// reported: the runtime has some of its own (a sudog's elem, a g's
// defers and panics). Nor is a pointer that a g holds into its own
// stack: some are left behind (param, sched.ctxt).

// checkStackPtr checks the first property for p, which the collector
// found at refBase+refOff (or, if refOff is ^0, somewhere in the page
// of small objects at refBase) while scanning an object,
// a global or a stack, and which points to no heap object. (Not a
// pointer that a write barrier recorded: it may be one that the
// runtime had in a sudog or a defer record when the frame was live.)
func checkStackPtr(p, refBase, refOff uintptr) {
	if s := spanOf(p); s == nil || s.state.get() != mSpanManual {
		return
	}
	var bad *g
	var badSP uintptr
	forEachGRace(func(gp *g) {
		if bad != nil {
			return
		}
		stk := gp.stack
		if p < stk.lo || p >= stk.hi {
			return
		}
		switch readgstatus(gp) &^ _Gscan {
		case _Grunnable, _Gwaiting, _Gpreempted:
		default:
			// Its stack pointer isn't known (running, in a system
			// call), or the stack isn't in use or is being moved.
			return
		}
		sp := gp.sched.sp
		if sp < stk.lo || sp >= stk.hi || p >= sp {
			return
		}
		// The goroutine may have started to run, or its stack to
		// move, since: look again.
		if gp.stack != stk || gp.sched.sp != sp {
			return
		}
		switch readgstatus(gp) &^ _Gscan {
		case _Grunnable, _Gwaiting, _Gpreempted:
			bad, badSP = gp, sp
		}
	})
	if bad == nil {
		return
	}
	// The pointer was read some time ago, and the goroutine has run
	// since: one that the runtime had in a sudog while the frame was
	// live is gone by now. Report what is still there.
	if refOff == ^uintptr(0) {
		found := false
		for a := refBase; a < refBase+pageSize; a += goarch.PtrSize {
			if *(*uintptr)(unsafe.Pointer(a)) == p {
				found = true
				break
			}
		}
		if !found {
			return
		}
	} else if refBase != 0 && *(*uintptr)(unsafe.Pointer(refBase + refOff)) != p {
		return
	}
	// Not what the goroutine's own g holds.
	if g := uintptr(unsafe.Pointer(bad)); refOff == ^uintptr(0) {
		if refBase <= g && g < refBase+pageSize {
			return
		}
	} else if refBase == g {
		return
	}
	printlock()
	print("runtime: pointer ", hex(p), " into the dead part of the stack of goroutine ", bad.goid,
		" [", hex(bad.stack.lo), ", ", hex(bad.stack.hi), "), sp=", hex(badSP), "\n")
	if refOff == ^uintptr(0) {
		print("runtime: found in an object of the page at ", hex(refBase), "\n")
	} else if refBase != 0 {
		print("runtime: found at *(", hex(refBase), "+", hex(refOff), ")\n")
		if s := spanOfHeap(refBase); s != nil {
			gcDumpObject("object", refBase, refOff)
		} else if !printGlobalAt(refBase + refOff) {
			print("runtime: which is not in the heap\n")
		}
	}
	printunlock()
	getg().m.traceback = 2
	throw("found pointer into a returned frame (stackptrcheck)")
}

// badStackPtr reports p, found at addr in a frame with stack pointer
// sp of the stack being scanned, which points below that frame.
func badStackPtr(p, addr, sp uintptr) {
	printlock()
	print("runtime: pointer ", hex(p), " at ", hex(addr), " in a frame with sp=", hex(sp),
		" points into its stack below that frame\n")
	printunlock()
	getg().m.traceback = 2
	throw("found pointer from a frame into a returned frame (stackptrcheck)")
}

// printGlobalAt reports whether addr is in a module's data or bss,
// and prints which if so.
func printGlobalAt(addr uintptr) bool {
	for datap := &firstmoduledata; datap != nil; datap = datap.next {
		if datap.data <= addr && addr < datap.edata {
			print("runtime: which is in data at +", hex(addr-datap.data), "\n")
			return true
		}
		if datap.bss <= addr && addr < datap.ebss {
			print("runtime: which is in bss at +", hex(addr-datap.bss), "\n")
			return true
		}
	}
	return false
}
