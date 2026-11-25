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

// jmpBuf is what a jmp_buf points to: and, if sigsetjmp saved it, the
// signal mask that longjmp restores.
type jmpBuf struct {
	f     *JmpFrame
	site  int32
	saved bool
	mask  uint64
}

// Setjmp is setjmp(env) at site of frame f's function: it saves both in
// env, a jmp_buf, and returns 0. What env gets is the pointer to a
// Handle: longjmp goes nowhere but where a setjmp was.
func Setjmp(env *Void, f *JmpFrame, site int32) int32 {
	StorePtr(env, NewHandle(&jmpBuf{f: f, site: site}))
	return 0
}

// Sigsetjmp is sigsetjmp(env, save): setjmp that also saves the calling
// thread's signal mask in env if save is not 0, for longjmp (and
// siglongjmp) to restore, as glibc's and musl's do.
func Sigsetjmp(env *Void, f *JmpFrame, site, save int32) int32 {
	jb := &jmpBuf{f: f, site: site}
	if save != 0 {
		t := SigSelf()
		SigMu.Lock()
		jb.saved, jb.mask = true, t.Mask
		SigMu.Unlock()
	}
	StorePtr(env, NewHandle(jb))
	return 0
}

type longjmpPanic struct {
	f         *JmpFrame
	site, val int32
}

// Longjmp is longjmp.
func Longjmp(env *Void, val int32) {
	p := LoadPtr(env)
	jb, ok := HandleOf[*jmpBuf](p)
	if !ok {
		RaiseFault("longjmp to a jmp_buf setjmp did not set", Addr(p))
	}
	if !jb.f.live {
		RaiseFault("longjmp to a function that has returned", Addr(p))
	}
	if val == 0 {
		val = 1
	}
	if jb.saved {
		t := SigSelf()
		SigMu.Lock()
		t.Mask = jb.mask
		SigMu.Unlock()
		t.DeliverPending()
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
