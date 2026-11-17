// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"fmt"
	"os"
	"reflect"
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
func NewFunc(f any) Ptr {
	p := New[Func]()
	(*Func)(p.obj).f = f
	return p
}

var funcInfo = tinfoOf[Func]()

// FuncOf returns the Go function of type F that p points to, which a
// call through a C function pointer calls.
func FuncOf[F any](p Ptr) F {
	if p.obj == nil || p.off != 0 || p.info() != funcInfo {
		fault("call through a pointer that is not to a function", Addr(p))
	}
	f, ok := (*Func)(p.obj).f.(F)
	if !ok {
		if g, ok := adapt[F]((*Func)(p.obj).f); ok {
			return g
		}
		var z F
		fault(fmt.Sprintf("call of a %T through a pointer to a function of type %T", (*Func)(p.obj).f, z), Addr(p))
	}
	return f
}

// adapt makes f callable as an F when F is a function without a
// prototype (its arguments ...any), as C lets one be called: the
// arguments, promoted, are converted to f's parameters.
func adapt[F any](f any) (F, bool) {
	var z F
	ft, fv := reflect.TypeFor[F](), reflect.ValueOf(f)
	gt := fv.Type()
	if !ft.IsVariadic() || ft.NumIn() != 1 || ft.In(0) != reflect.TypeFor[[]any]() ||
		ft.NumOut() != gt.NumOut() || gt.NumOut() == 1 && !convertible(gt.Out(0), ft.Out(0)) {
		return z, false
	}
	g := reflect.MakeFunc(ft, func(in []reflect.Value) []reflect.Value {
		args := in[0].Interface().([]any)
		var vals []reflect.Value
		for i := 0; i < gt.NumIn(); i++ {
			pt := gt.In(i)
			if gt.IsVariadic() && i == gt.NumIn()-1 {
				for _, a := range args[min(i, len(args)):] {
					vals = append(vals, reflect.ValueOf(a))
				}
				break
			}
			if i >= len(args) {
				vals = append(vals, reflect.Zero(pt))
				continue
			}
			v := reflect.ValueOf(args[i])
			if !convertible(v.Type(), pt) {
				fault(fmt.Sprintf("call passing a %s for a %s", v.Type(), pt), 0)
			}
			vals = append(vals, v.Convert(pt))
		}
		var out []reflect.Value
		if gt.IsVariadic() {
			out = fv.CallSlice(append(vals[:gt.NumIn()-1:gt.NumIn()-1], reflect.ValueOf(vals[gt.NumIn()-1:])))
		} else {
			out = fv.Call(vals)
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

// String allocates a string literal's array: s holds its elements'
// bytes, the null element included.
func String[T any](s string) Ptr {
	p := NewArray[T](uintptr(len(s)))
	copy(unsafe.Slice((*byte)(p.obj), len(s)), s)
	return p
}

// Copy returns a copy of the n bytes at p, in a new object of p's type.
func Copy(p Ptr, n uint64) Ptr {
	ti := p.info()
	q := newOf(ti, uintptr(n))
	Memmove(q, p, uintptr(n))
	return q
}

// newOf allocates n bytes as an array of ti's type.
func newOf(ti *tinfo, n uintptr) Ptr {
	sz := ti.t.Size()
	count := n
	if sz > 0 {
		count = (n + sz - 1) / sz
	}
	st := reflectArrayObj(ti.t, int(count))
	base := newObj(st)
	h := (*header)(base)
	*h = header{magic: magic, ti: ti, size: count * sz}
	return Ptr{obj: unsafe.Add(base, hdrSize), size: count * sz, ti: cref(ti)}
}

// Args returns main's argv.
func Args() Ptr {
	argv := NewArray[unsafe.Pointer](uintptr(len(os.Args)+1) * 8)
	for i, a := range os.Args {
		StorePtr(Add(argv, i*8), CString(a))
	}
	return argv
}

// A Trap is what __builtin_trap and __builtin_unreachable panic with.
func Trap() { panic(&Fault{"trap", 0}) }

// AssertC is C's assert: if ok is false, it reports the assertion's text,
// file and line, and aborts.
func AssertC(ok bool, text, file Ptr, line int32) {
	if !ok {
		Fflush(Ptr{})
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

var vaInfo = tinfoOf[VaList]()

// VaStart is va_start: a va_list of a function's variable arguments.
func VaStart(va []any) Ptr {
	p := New[VaList]()
	(*VaList)(p.obj).args = va
	return p
}

func vaOf(ap Ptr) *VaList {
	if ap.obj == nil || ap.off != 0 || ap.info() != vaInfo {
		fault("va_arg of a va_list not started", Addr(ap))
	}
	return (*VaList)(ap.obj)
}

// VaCopy is va_copy.
func VaCopy(ap Ptr) Ptr {
	v := vaOf(ap)
	p := New[VaList]()
	*(*VaList)(p.obj) = *v
	return p
}

func (v *VaList) next() any {
	if v.i >= len(v.args) {
		fault("va_arg past the last argument", 0)
	}
	a := v.args[v.i]
	v.i++
	return a
}

// VaArg is va_arg(ap, T) of a scalar T. An argument of another type of
// the same size and class is converted, as C allows for signedness.
func VaArg[T any](ap Ptr) T {
	a := vaOf(ap).next()
	if v, ok := a.(T); ok {
		return v
	}
	var r T
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
		fault(fmt.Sprintf("va_arg of a %T as a %T", a, r), 0)
	}
	return r
}

// VaArgPtr is va_arg(ap, T) of a struct T: the c23.Ptr to its copy.
func VaArgPtr(ap Ptr) Ptr {
	a := vaOf(ap).next()
	p, ok := a.(Ptr)
	if !ok {
		fault(fmt.Sprintf("va_arg of a %T as a struct", a), 0)
	}
	return p
}

// ReallocOf is realloc whose result is a T*: n bytes as an array of T,
// with what p's object holds from p on.
func ReallocOf[T any](p Ptr, n uintptr) Ptr {
	q := NewArray[T](n)
	if p.obj != nil {
		Memmove(q, p, min(n, p.size-uintptr(p.off)))
	}
	return q
}
