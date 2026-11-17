// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package reflect

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/sys"
	"strconv"
	"sync"
	"unsafe"
)

// newFuncType makes the func type in linear memory, where type
// descriptors are with GOWASM=gc: a struct type made by StructOf is
// not one the program can allocate.
func newFuncType(n int) (*funcType, []*rtype) {
	size := unsafe.Sizeof(funcType{}) + uintptr(n)*unsafe.Sizeof((*rtype)(nil))
	p := gcNewLinear(int(size))
	ft := (*funcType)(p)
	args := unsafe.Slice((**rtype)(unsafe.Add(p, unsafe.Sizeof(funcType{}))), n)[0:0:n]
	return ft, args
}

// With GOWASM=gc, functions are called with their arguments and results
// in WebAssembly parameters and results: a function of a type known at
// run time is called, or made, by code the linker generates for each
// func type of the program (see cmd/link/internal/wasm/gcreflect.go).
const wasmGCMode = true

//go:noescape
func gcreflectcall(t *abi.Type, code uintptr, ctxt, args, results, rcvr unsafe.Pointer, flags int) bool

func gcmakefunc(t *abi.Type) uintptr

// gcValuePtr returns a pointer to a copy of the value of x, of type t.
func gcValuePtr(x Value, t *abi.Type) unsafe.Pointer {
	p := unsafe_New(t)
	if x.flag&(flagIndir|flagInline|flagSpread) != 0 {
		typedmemmove(t, p, (&x).dataPtr())
	} else {
		*(*unsafe.Pointer)(p) = x.ptr
	}
	return p
}

// gcMethodValue is gcMakeMethodValue, once a Value is of a method:
// Value.method, which makes each, sets it. A function made of a method
// value calls through the code the linker generates for each func type
// of the program, and makeMethodValue is called by Value.Interface, as
// any program that prints calls: called through this variable, it is
// linked, with that code, only into a program that has such a Value.
var gcMethodValue func(op string, v Value) Value

// gcMakeMethodValue is makeMethodValue: a function made of the method
// call, as MakeFunc makes.
func gcMakeMethodValue(op string, v Value) Value {
	methodReceiver(op, v, int(v.flag)>>flagMethodShift)
	m := v
	// (The arguments of a variadic function come packed.)
	callOp := "Call"
	if v.Type().IsVariadic() {
		callOp = "CallSlice"
	}
	return makeFuncWasmGC(v.Type().common(), (*funcType)(unsafe.Pointer(v.Type().common())), func(args []Value) []Value {
		gcPassDeferred = gcDeferred
		return m.call(callOp, args)
	})
}

func callWasmGC(op string, t *funcType, fn unsafe.Pointer, rcvr Value, rcvrtype *abi.Type, in []Value) []Value {
	if _, ok := gcDirectFuncs.Load(fn); ok && rcvrtype == nil {
		return callDirect(t, (*makeFuncImpl)(fn), in)
	}
	if rcvrtype == nil {
		if m, ok := gcMethodFuncs.Load(fn); ok {
			// Type.Method(i).Func: the method value of the receiver.
			mf := m.(gcMethod)
			// (Not Method, which would keep every method of the
			// program: a caller of Type.Method does that.)
			v := in[0].assignTo("reflect.Value.Call", &mf.t.t, nil).method(mf.i)
			if t.IsVariadic() {
				return v.CallSlice(in[1:])
			}
			return v.Call(in[1:])
		}
	}
	nin, nout := len(in), t.NumOut()
	// (One more than needed, so that they are arrays.)
	args := make([]unsafe.Pointer, nin+1)
	for i, x := range in {
		targ := t.In(i)
		x = x.assignTo("reflect.Value.Call", targ, nil)
		args[i] = gcValuePtr(x, targ)
	}
	results := make([]unsafe.Pointer, nout+1)
	for i := 0; i < nout; i++ {
		results[i] = unsafe_New(t.Out(i))
	}
	var ctxt, rp unsafe.Pointer
	flags := 0
	if fn == nil {
		panic("reflect: call of nil function")
	}
	code := *(*uintptr)(fn)
	if rcvrtype != nil {
		storeRcvr(rcvr, unsafe.Pointer(&rp), false)
		flags = 1
	} else {
		ctxt = fn
	}
	if gcPassDeferred {
		gcPassDeferred = false
		passDeferredCall()
	}
	ok := gcreflectcall(&t.Type, code, ctxt, unsafe.Pointer(unsafe.SliceData(args)), unsafe.Pointer(unsafe.SliceData(results)), rp, flags)
	if !ok {
		// A func type made at run time: the adapter of one of the
		// program with the same registers.
		if rep := gcSameLayout(t); rep != nil {
			ok = gcreflectcall(rep, code, ctxt, unsafe.Pointer(unsafe.SliceData(args)), unsafe.Pointer(unsafe.SliceData(results)), rp, flags)
		}
	}
	if !ok {
		panic("reflect: " + op + " of a function of type " + stringFor(&t.Type) + " is not supported with GOWASM=gc")
	}
	ret := make([]Value, nout)
	for i := range ret {
		tv := t.Out(i)
		ret[i] = Value{typ_: tv, ptr: results[i], flag: flagIndir | flag(tv.Kind())}
	}
	return ret
}

// gcMakeFuncCall is makeFuncCallGC, for the linker to find when the
// program makes functions.
var gcMakeFuncCall func(*makeFuncImpl, unsafe.Pointer) unsafe.Pointer

func makeFuncWasmGC(t *abi.Type, ftyp *funcType, fn func([]Value) []Value) Value {
	gcMakeFuncCall = makeFuncCallGC
	if wasmSplitMode {
		gcMakeFuncArgs = makeFuncArgsGC
	}
	code := gcmakefunc(t)
	if code == 0 {
		if rep := gcSameLayout(ftyp); rep != nil {
			code = gcmakefunc(rep)
		}
	}
	impl := &makeFuncImpl{makeFuncCtxt: makeFuncCtxt{fn: code}, ftyp: ftyp, fn: fn}
	if code == 0 {
		// No stub has the registers of the type, which the program
		// does not have: only reflect can call such a function (Go
		// code would need its type), and it calls fn itself.
		gcDirectFuncs.Store(unsafe.Pointer(impl), true)
	}
	return Value{typ_: t, ptr: unsafe.Pointer(impl), flag: flag(Func)}
}

// gcMakeFuncArgs is makeFuncArgsGC, for the linker to find likewise
// (GOWASM=gc,split).
var gcMakeFuncArgs func(*makeFuncImpl) unsafe.Pointer

// makeFuncArgsGC returns, with GOWASM=gc,split, the pointers to new
// values of the arguments of a function that MakeFunc made, which the
// function stores its arguments in before it calls makeFuncCallGC
// with them.
func makeFuncArgsGC(ctxt *makeFuncImpl) unsafe.Pointer {
	ftyp := ctxt.ftyp
	args := make([]unsafe.Pointer, ftyp.NumIn()+1)
	for i := range ftyp.NumIn() {
		args[i] = unsafe_New(ftyp.In(i))
	}
	return unsafe.Pointer(unsafe.SliceData(args))
}

// makeFuncCallGC is what a function that MakeFunc made calls, with the
// pointers to its arguments: it returns the pointers to its results.
func makeFuncCallGC(ctxt *makeFuncImpl, args unsafe.Pointer) unsafe.Pointer {
	// (Whether a panic is running this function as a deferred call:
	// a method value passes that on to its method. See gcDeferred.)
	saved := gcDeferred
	gcDeferred = deferredCall(sys.GetCallerSP())
	ftyp := ctxt.ftyp
	nin := ftyp.NumIn()
	ptrs := unsafe.Slice((*unsafe.Pointer)(args), nin)
	in := make([]Value, nin)
	for i := range in {
		t := ftyp.In(i)
		in[i] = Value{typ_: t, ptr: ptrs[i], flag: flagIndir | flag(t.Kind())}
	}
	if gcDeferred {
		// The function recovers as the deferred call it is for.
		deferHere()
	}
	out := ctxt.fn(in)
	gcDeferred = saved
	nout := ftyp.NumOut()
	if len(out) != nout {
		panic("reflect: wrong return count from function created by MakeFunc")
	}
	res := make([]unsafe.Pointer, nout+1)
	for i, v := range out {
		tv := ftyp.Out(i)
		if v.typ() == nil {
			panic("reflect: function created by MakeFunc using " + funcName(ctxt.fn) + " returned zero Value")
		}
		if v.flag&flagRO != 0 {
			panic("reflect: function created by MakeFunc using " + funcName(ctxt.fn) + " returned value obtained from unexported field")
		}
		v = v.assignTo("reflect.MakeFunc", tv, nil)
		res[i] = gcValuePtr(v, tv)
	}
	return unsafe.Pointer(unsafe.SliceData(res))
}

// linearType returns a copy of the type descriptor t in linear memory,
// where the runtime reads type descriptors with GOWASM=gc (a struct
// variable is an object).
func linearType[T any](t *T) *T {
	var zero T
	p := (*T)(gcNewLinear(int(unsafe.Sizeof(zero))))
	*p = *t
	return p
}

// gcKept are the pointers to the linear memory of the types and names
// made at run time. A type descriptor refers to its names, its fields
// and other types by their addresses, which do not keep their blocks
// from being freed when the host collects: they are kept for good, as
// the types Go makes at run time are.
var gcKept struct {
	sync.Mutex
	ptrs []unsafe.Pointer
}

func gcKeep(p unsafe.Pointer) {
	gcKept.Lock()
	gcKept.ptrs = append(gcKept.ptrs, p)
	gcKept.Unlock()
}

// gcSameLayout returns a func type of the program whose arguments and
// results are in the same registers as those of t, or nil: the linker
// makes an adapter (to call) and a stub (to make a function) only for
// the func types of the program, and those of t, made at run time
// (FuncOf, a method's type with its receiver), can use them.
func gcSameLayout(t *funcType) *abi.Type {
	gcLayoutsOnce.Do(func() {
		gcLayouts = make(map[string]*abi.Type)
		add := func(ts []*abi.Type) {
			for _, ft := range ts {
				if ft.Kind() == abi.Func {
					k := gcFuncKey((*funcType)(unsafe.Pointer(ft)))
					if _, ok := gcLayouts[k]; !ok {
						gcLayouts[k] = ft
					}
				}
			}
		}
		first, rest := compiledTypelinks()
		add(first)
		for _, ts := range rest {
			add(ts)
		}
	})
	return gcLayouts[gcFuncKey(t)]
}

var (
	gcLayoutsOnce sync.Once
	gcLayouts     map[string]*abi.Type
)

// gcFuncKey spells the registers of the arguments and results of t as
// the linker does (cmd/link/internal/wasm/gcreflect.go).
func gcFuncKey(t *funcType) string {
	var b []byte
	for _, ts := range [][]*abi.Type{t.InSlice(), t.OutSlice()} {
		for _, at := range ts {
			b = gcAppendRegs(b, at, 0)
			b = append(b, ',')
		}
		b = append(b, ';')
	}
	return string(b)
}

// gcWordKind is the kind of an integer word (ld.WasmGCWordKind): 'w'
// with words of 4 bytes, 'l' with 8.
const gcWordKind = 'w'*(goarch.PtrSize/8^1) + 'l'*(goarch.PtrSize/8)

// gcAppendRegs is cmd/link/internal/ld.wasmGCAppendRegs, spelled.
func gcAppendRegs(b []byte, t *abi.Type, off uintptr) []byte {
	add := func(o uintptr, mem, reg byte, signed bool) {
		b = append(b, mem, reg)
		if signed {
			b = append(b, 's')
		}
		b = strconv.AppendUint(b, uint64(off+o), 10)
	}
	switch k := t.Kind(); k {
	case abi.Struct:
		st := (*structType)(unsafe.Pointer(t))
		for _, f := range st.Fields {
			if f.Typ.Size() == 0 {
				continue
			}
			b = gcAppendRegs(b, f.Typ, off+f.Offset)
		}
	case abi.Array:
		at := (*arrayType)(unsafe.Pointer(t))
		for i := uintptr(0); i < at.Len; i++ {
			b = gcAppendRegs(b, at.Elem, off+i*at.Elem.Size())
		}
	case abi.String, abi.Slice:
		if k == abi.String && goarch.PtrSize == 4 {
			// wasm32's string (ld.WasmGCRegs): word 0 as a pointer
			// if the tag is 0, A over it, and B.
			add(0, 's', 'r', false)
			add(0, 'l', 'l', false)
			add(8, 'l', 'l', false)
			break
		}
		add(0, 'r', 'r', false)
		add(goarch.PtrSize, gcWordKind, 'l', false)
		add(2*goarch.PtrSize, gcWordKind, 'l', false)
	case abi.Interface:
		add(0, gcWordKind, 'r', false)
		add(goarch.PtrSize, 'r', 'r', false)
		add(2*goarch.PtrSize, 'd', 'd', false)
		add(2*goarch.PtrSize+8, 'd', 'd', false)
	case abi.Complex64:
		add(0, 'f', 'f', false)
		add(4, 'f', 'f', false)
	case abi.Complex128:
		add(0, 'd', 'd', false)
		add(8, 'd', 'd', false)
	case abi.Pointer, abi.Map, abi.Chan, abi.Func, abi.UnsafePointer:
		add(0, 'r', 'r', false)
	case abi.Float32:
		add(0, 'f', 'f', false)
	case abi.Float64:
		add(0, 'd', 'd', false)
	default:
		signed := k == abi.Int8 || k == abi.Int16 || k == abi.Int32 || k == abi.Int && t.Size() < 8
		add(0, "bh?w???l"[t.Size()-1], 'l', signed)
	}
	return b
}

// The functions of Type.Method, by the pointer that is their value.
type gcMethod struct {
	t *rtype
	i int
}

var (
	gcMethodFuncs sync.Map // unsafe.Pointer → gcMethod
	gcMethodPtrs  sync.Map // gcMethod → unsafe.Pointer
)

// gcMethodFunc returns the function value of method i of t, whose code
// is tfn: one per method, which callWasmGC knows.
func gcMethodFunc(t *rtype, i int, tfn unsafe.Pointer) unsafe.Pointer {
	k := gcMethod{t, i}
	if p, ok := gcMethodPtrs.Load(k); ok {
		return p.(unsafe.Pointer)
	}
	fn := unsafe.Pointer(&tfn)
	p, _ := gcMethodPtrs.LoadOrStore(k, fn)
	gcMethodFuncs.Store(p, k)
	return p.(unsafe.Pointer)
}

// gcTypeEqual compares two values of type t (which may be a struct type
// made by StructOf).
//
//go:linkname gcTypeEqual runtime.typeEqual
func gcTypeEqual(t *abi.Type, p, q unsafe.Pointer) bool

// gcDirectFuncs are the functions MakeFunc made of types that no stub
// fits: callWasmGC calls their Go function itself.
var gcDirectFuncs sync.Map // *makeFuncImpl → true

// callDirect calls the function of impl, of type t, with in, as a call
// through a stub would.
func callDirect(t *funcType, impl *makeFuncImpl, in []Value) []Value {
	args := make([]Value, len(in))
	for i, x := range in {
		targ := t.In(i)
		x = x.assignTo("reflect.Value.Call", targ, nil)
		// A copy, as an argument is.
		p := gcValuePtr(x, targ)
		args[i] = Value{typ_: targ, ptr: p, flag: flagIndir | flag(targ.Kind())}
	}
	out := impl.fn(args)
	nout := t.NumOut()
	if len(out) != nout {
		panic("reflect: wrong return count from function created by MakeFunc")
	}
	ret := make([]Value, nout)
	for i, v := range out {
		tv := t.Out(i)
		if v.typ() == nil {
			panic("reflect: function created by MakeFunc using " + funcName(impl.fn) + " returned zero Value")
		}
		if v.flag&flagRO != 0 {
			panic("reflect: function created by MakeFunc using " + funcName(impl.fn) + " returned value obtained from unexported field")
		}
		v = v.assignTo("reflect.MakeFunc", tv, nil)
		ret[i] = Value{typ_: tv, ptr: gcValuePtr(v, tv), flag: flagIndir | flag(tv.Kind())}
	}
	return ret
}

// gcDeferred is whether the function MakeFunc made that is running was
// called by a panic, as a deferred call; gcPassDeferred, whether the
// next call of callWasmGC is the method of a method value that was: a
// recover in the method then recovers, as it does when the method
// value is the compiler's.
var gcDeferred, gcPassDeferred bool

func deferredCall(sp uintptr) bool
func passDeferredCall()
func deferHere()

// gcNewLinear returns n zeroed bytes of linear memory for the data of a
// type made at run time, which are kept for good (see gcKept).
func gcNewLinear(n int) unsafe.Pointer {
	p := unsafe_NewArray(rtypeOf(byte(0)), n)
	if wasmSplitMode {
		// Words that may be pointers, which split keeps with them.
		p = unsafe_NewArray(rtypeOf(unsafe.Pointer(nil)), (n+goarch.PtrSize-1)/goarch.PtrSize)
	}
	gcKeep(p)
	return p
}
