// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"unsafe"
)

// What the Go that cmd/cc generates calls, besides memory accesses.

// B2I is a Go bool as C's int.
func B2I(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// B2U8 is a Go bool as C's bool.
func B2U8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// If is c ? x : y, for operands without effects.
func If[T any](c bool, x, y T) T {
	if c {
		return x
	}
	return y
}

// Val returns v: a constant that Go may not see as one (a division by
// zero C would only do at run time).
//
//go:noinline
func Val[T any](v T) T { return v }

// A Func is a C function, as an object a C pointer may point to.
type Func struct {
	f any
}

// NewFunc returns the C pointer to the function f.
func NewFunc(f any) *Void {
	p := New[Func]()
	(*Func)(unsafe.Pointer(p)).f = f
	return p
}

var funcInfo = TinfoOf[Func]()

// FuncOf returns the Go function of type F that p points to, which a
// call through a C function pointer calls.
func FuncOf[F any](p *Void) F {
	if p == nil {
		RaiseFault("call through a pointer that is not to a function", Addr(p))
	}
	f, ok := (*Func)(unsafe.Pointer(p)).f.(F)
	if !ok {
		if g, ok := adapt[F]((*Func)(unsafe.Pointer(p)).f); ok {
			return g
		}
		var z F
		RaiseFault(fmt.Sprintf("call of a %T through a pointer to a function of type %T", (*Func)(unsafe.Pointer(p)).f, z), Addr(p))
	}
	return f
}

// adapt makes f callable as an F where C allows the call, with a
// function or a pointer type without a prototype on one side: the
// arguments (as ...any, or each) are converted to f's parameters.
func adapt[F any](f any) (F, bool) {
	var z F
	ft, fv := reflect.TypeFor[F](), reflect.ValueOf(f)
	gt := fv.Type()
	anyArgs := reflect.TypeFor[[]any]()
	fLoose := ft.IsVariadic() && ft.NumIn() == 1 && ft.In(0) == anyArgs
	gLoose := gt.IsVariadic() && gt.NumIn() == 1 && gt.In(0) == anyArgs
	if !fLoose && !gLoose || ft.NumOut() != gt.NumOut() || gt.NumOut() == 1 && !convertible(gt.Out(0), ft.Out(0)) {
		return z, false
	}
	g := reflect.MakeFunc(ft, func(in []reflect.Value) []reflect.Value {
		// The arguments, as values.
		var args []reflect.Value
		if fLoose {
			for _, a := range in[0].Interface().([]any) {
				args = append(args, reflect.ValueOf(a))
			}
		} else {
			args = in
		}
		var out []reflect.Value
		if gLoose {
			as := make([]any, len(args))
			for i, a := range args {
				as[i] = a.Interface()
			}
			out = fv.CallSlice([]reflect.Value{reflect.ValueOf(as)})
		} else {
			var vals []reflect.Value
			n := gt.NumIn()
			if gt.IsVariadic() {
				n--
			}
			for i := 0; i < n; i++ {
				pt := gt.In(i)
				if i >= len(args) {
					vals = append(vals, reflect.Zero(pt))
					continue
				}
				if !convertible(args[i].Type(), pt) {
					RaiseFault(fmt.Sprintf("call passing a %s for a %s", args[i].Type(), pt), 0)
				}
				vals = append(vals, args[i].Convert(pt))
			}
			if gt.IsVariadic() {
				var rest []any
				for _, a := range args[min(n, len(args)):] {
					rest = append(rest, a.Interface())
				}
				vals = append(vals, reflect.ValueOf(rest))
				out = fv.CallSlice(vals)
			} else {
				out = fv.Call(vals)
			}
		}
		if len(out) == 1 {
			out[0] = out[0].Convert(ft.Out(0))
		}
		return out
	})
	return g.Interface().(F), true
}

func convertible(a, b reflect.Type) bool {
	if a == b {
		return true
	}
	num := func(t reflect.Type) bool { return t.Kind() >= reflect.Int && t.Kind() <= reflect.Float64 }
	return num(a) && num(b)
}

// emptyString is the empty string literal: its 0 byte.
var emptyString [1]byte

// String is the string literal s: the pointer to its first character.
// The 0 byte that ends it is the one the compiler puts after the data of
// every string constant, so s must be a constant. Its bytes are
// read-only, as C's literal's are: storing through the pointer is a
// fault.
func String(s string) *Char {
	if len(s) == 0 {
		return (*Char)(unsafe.Pointer(&emptyString))
	}
	return (*Char)(unsafe.Pointer(unsafe.StringData(s)))
}

// StringOf is String for a literal whose array, A ([N]T), cmd/cc knows:
// the same object, of a type the compiler makes rather than reflect.
func StringOf[T, A any](s string) *Void {
	p := LocalT(new(Obj[A]), TinfoOf[T]())
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(s)), s)
	return p
}

// A ByValue is a struct or union passed by value in variable arguments:
// the pointer to a copy of it. (A pointer that is passed is a *Void.)
type ByValue struct {
	P *Void
}

// Copy returns a copy of the n bytes at p.
func Copy(p *Void, n uint64) *Void {
	q := Malloc(n)
	Memmove(q, p, uintptr(n))
	return q
}

// Args returns main's argv.
func Args() *Void {
	argv := NewArray[unsafe.Pointer](uintptr(len(os.Args)+1) * WordSize)
	for i, a := range os.Args {
		StorePtr(Add(argv, i*int(WordSize)), CString(a))
	}
	return argv
}

// A Trap is what __builtin_trap and __builtin_unreachable panic with.
func Trap() { panic(&Fault{"trap", 0}) }

// AssertC is C's assert: if ok is false, it reports the assertion's text,
// file and line, and aborts.
func AssertC(ok bool, text, file *Void, line int32) {
	if !ok {
		Fflush((*Void)(nil))
		fmt.Fprintf(os.Stderr, "%s:%d: assertion failed: %s\n", GoString(file), line, GoString(text))
		Abort()
	}
}

// LDouble is C's long double in memory: x86-64's 16 bytes, holding a
// double.
type LDouble struct {
	V float64
	_ uint64
}

// A VaList is C's va_list: the variable arguments, and the next.
type VaList struct {
	args []any
	i    int
}

var vaInfo = TinfoOf[VaList]()

// VaStart is va_start: a va_list of a function's variable arguments.
func VaStart(va []any) *Void {
	p := New[VaList]()
	(*VaList)(unsafe.Pointer(p)).args = va
	return p
}

func VaOf(ap *Void) *VaList {
	if ap == nil {
		RaiseFault("va_arg of a va_list not started", Addr(ap))
	}
	return (*VaList)(unsafe.Pointer(ap))
}

// VaCopy is va_copy.
func VaCopy(ap *Void) *Void {
	v := VaOf(ap)
	p := New[VaList]()
	*(*VaList)(unsafe.Pointer(p)) = *v
	return p
}

func (v *VaList) Next() any {
	if v.i >= len(v.args) {
		RaiseFault("va_arg past the last argument", 0)
	}
	a := v.args[v.i]
	v.i++
	return a
}

// VaArg is va_arg(ap, T) of a scalar T. An argument of another type of
// the same size and class is converted, as C allows for signedness.
func VaArg[T any](ap *Void) T {
	a := VaOf(ap).Next()
	if v, ok := a.(T); ok {
		return v
	}
	var r T
	if _, ok := any(r).(*Void); ok {
		// An integer passed for a pointer (0 for a null pointer).
		if n, ok := intArgValue(a); ok {
			return any(FromInt(uintptr(n))).(T)
		}
	}
	if p, ok := a.(*Void); ok {
		// An intcap (or a pointer) passed for an integer: its address.
		a = uint64(Addr(p))
	}
	rv := reflect.ValueOf(&r).Elem()
	av := reflect.ValueOf(a)
	switch {
	case av.CanInt() && (rv.CanInt() || rv.CanUint()):
		if rv.CanInt() {
			rv.SetInt(av.Int())
		} else {
			rv.SetUint(uint64(av.Int()))
		}
	case av.CanUint() && (rv.CanInt() || rv.CanUint()):
		if rv.CanInt() {
			rv.SetInt(int64(av.Uint()))
		} else {
			rv.SetUint(av.Uint())
		}
	default:
		RaiseFault(fmt.Sprintf("va_arg of a %T as a %T", a, r), 0)
	}
	return r
}

// VaArgPtr is va_arg(ap, T) of a struct T: the *c23.Void to its copy.
func VaArgPtr(ap *Void) *Void {
	a := VaOf(ap).Next()
	if v, ok := a.(ByValue); ok {
		return v.P
	}
	p, ok := a.(*Void)
	if !ok {
		RaiseFault(fmt.Sprintf("va_arg of a %T as a struct", a), 0)
	}
	return p
}

// ReallocOf is realloc whose result is a T*: n bytes as an array of T,
// with what p's object holds. p is NULL or what malloc returned
// (checkRealloc).
func ReallocOf[T any](p *Void, n uintptr) *Void {
	q := NewArray[T](n)
	if p != nil {
		Memmove(q, p, min(n, sizeOf(unsafe.Pointer(p))))
	}
	return q
}

func intArgValue(a any) (int64, bool) {
	switch v := a.(type) {
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint32:
		return int64(v), true
	case uint64:
		return int64(v), true
	}
	return 0, false
}

// dlfcn.h: C compiled by cmd/cc cannot load native code, so dlopen
// fails, as for a library not found.

func Dlopen(path *Void, flags int32) *Void { return (*Void)(nil) }
func Dlsym(h, name *Void) *Void            { return (*Void)(nil) }
func Dlclose(h *Void) int32                { return 0 }

var (
	dlMsgOnce sync.Once
	dlMsg     *Void
)

func Dlerror() *Void {
	dlMsgOnce.Do(func() { dlMsg = CString("dynamic loading is not supported by cmd/cc") })
	return dlMsg
}
