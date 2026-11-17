// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

// setjmp and longjmp, as Fil-C has them: longjmp may only go back to a
// function still running, and is checked. A function that calls setjmp
// runs its body in a closure that recovers longjmp's panic and goes back
// to the setjmp's site (cmd/cc's genstmt.go).

// A JmpFrame is a call of a function that calls setjmp.
type JmpFrame struct {
	live bool
}

func NewJmpFrame() *JmpFrame { return &JmpFrame{live: true} }

// Done marks the call returned: longjmp to it faults.
func (f *JmpFrame) Done() { f.live = false }

// jmpBuf is what a jmp_buf points to.
type jmpBuf struct {
	f    *JmpFrame
	site int32
}

var jmpBufInfo = tinfoOf[jmpBuf]()

// Setjmp is setjmp(env) at site of frame f's function: it saves both in
// env, a jmp_buf, and returns 0.
func Setjmp(env Ptr, f *JmpFrame, site int32) int32 {
	p := New[jmpBuf]()
	*(*jmpBuf)(p.obj) = jmpBuf{f, site}
	StorePtr(env, p)
	return 0
}

type longjmpPanic struct {
	f         *JmpFrame
	site, val int32
}

// Longjmp is longjmp.
func Longjmp(env Ptr, val int32) {
	p := LoadPtr(env)
	if p.obj == nil || p.off != 0 || p.info() != jmpBufInfo {
		fault("longjmp to a jmp_buf setjmp did not set", Addr(p))
	}
	jb := (*jmpBuf)(p.obj)
	if !jb.f.live {
		fault("longjmp to a function that has returned", Addr(p))
	}
	if val == 0 {
		val = 1
	}
	panic(&longjmpPanic{jb.f, jb.site, val})
}

// Catch reports whether x, recovered, is a longjmp to f: its site and
// value.
func (f *JmpFrame) Catch(x any) (site, val int32, ok bool) {
	if l, ok := x.(*longjmpPanic); ok && l.f == f {
		return l.site, l.val, true
	}
	return 0, 0, false
}
