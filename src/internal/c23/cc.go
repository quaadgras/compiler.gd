// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"fmt"
	"internal/abi"
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

// A Func is a C function, as an object a C pointer may point to. Like a
// Handle it is an interface and nothing else, which C cannot look into
// or change, in an object of the heap that was allocated as a Func.
type Func struct {
	f any
}

var funcType = abi.TypeFor[Func]()

// NewFunc returns the C pointer to the function f. A program calls it
// once for each of its functions that a pointer is taken to, as it
// starts: its body at each of them is half a kilobyte that runs once.
//
//go:nocheckptr
//go:noinline
func NewFunc(f any) *Void {
	p := (*Func)(newObject(funcType))
	p.f = f
	return seal(unsafe.Pointer(p))
}

// NewFuncAs is NewFunc for the function f seen as one of the type of
// like, a nil function: the type that a call through the pointer has
// (FuncOf), which differs from f's in what its pointers point to and in
// nothing a call depends on. cmd/cc passes the C function itself, where
// it would otherwise make a Go function for each that does nothing but
// convert pointers and call it.
//
//go:nocheckptr
func NewFuncAs(f, like any) *Void {
	(*abi.EmptyInterface)(unsafe.Pointer(&f)).Type = (*abi.EmptyInterface)(unsafe.Pointer(&like)).Type
	return NewFunc(f)
}

// FuncType returns what FuncInto is given for the functions that are
// called through pointers as functions of like's type, a nil function.
func FuncType(like any) unsafe.Pointer {
	return unsafe.Pointer((*abi.EmptyInterface)(unsafe.Pointer(&like)).Type)
}

// FuncInto makes the descriptor of a function and stores it in *d, if
// *d has none: fv is the function, as the pointer that a Go function
// value is, and typ is FuncType's for the type that calls through
// pointers to it have. It is NewFuncAs for a program that makes a descriptor for each
// of a hundred thousand functions as it starts, in which what a
// statement for each takes is most of that code: this takes three
// addresses.
//
//go:nocheckptr
//go:noinline
func FuncInto(d **Void, fv, typ unsafe.Pointer) {
	if *d != nil {
		return
	}
	var f any
	e := (*abi.EmptyInterface)(unsafe.Pointer(&f))
	e.Type, e.Data = (*abi.Type)(typ), fv
	*d = NewFunc(f)
}

// FillFuncs stores in the n pointers at dst the descriptors that the
// variables src points to an array of hold (nil: none): a table of
// virtual functions, whose entries are not known until the
// descriptors are made.
//
//go:nocheckptr
func FillFuncs(dst, src unsafe.Pointer, n int) {
	const w = unsafe.Sizeof(uintptr(0))
	for i := uintptr(0); i < uintptr(n); i++ {
		if p := *(***Void)(unsafe.Add(src, i*w)); p != nil {
			*(**Void)(unsafe.Add(dst, i*w)) = *p
		}
	}
}

var funcs seen

// funcAt returns the Func that p points to: p is the address of an
// object that NewFunc made, or the call is a fault. Memory that C wrote
// to look like a Func is not one: it could be made to call anything.
//
//go:nocheckptr
func funcAt(p *Void) *Func {
	fn := (*Func)(unseal(&funcs, p, funcType))
	if fn == nil {
		RaiseFault("call through a pointer that is not to a function", uintptr(unsafe.Pointer(p)))
	}
	return fn
}

// FuncOf returns the Go function of type F that p points to, which a
// call through a C function pointer calls.
func FuncOf[F any](p *Void) F {
	fn := funcAt(p)
	f, ok := fn.f.(F)
	if !ok {
		if g, ok := adapt[F](fn.f); ok {
			return g
		}
		var z F
		RaiseFault(fmt.Sprintf("call of a %T through a pointer to a function of type %T", fn.f, z), Addr(p))
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
// fault. (Its conversion is of what the compiler made, and needs no
// check.)
//
//go:nocheckptr
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
	copy(Writable(p, uintptr(len(s))), s)
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

// A VaList is C's va_list: the variable arguments that are left. Like
// a Handle it is something that C cannot look into or change, a slice
// and nothing else, in an object of the heap that was allocated as a
// VaList: an argument is a Go value of its own type, and va_arg gives
// it as that type or not at all.
type VaList struct {
	args []any
}

var vaListType = abi.TypeFor[VaList]()

var vaLists seen

// VaStart is va_start: a va_list of a function's variable arguments.
//
//go:nocheckptr
func VaStart(va []any) *Void {
	v := (*VaList)(newObject(vaListType))
	v.args = va
	return seal(unsafe.Pointer(v))
}

// VaArgs is the variable arguments va as a VaList of the caller's own,
// which no va_list of C's points to: for a function of the library that
// formats its arguments itself, as printf does, and gives them to no
// one.
func VaArgs(va []any) VaList { return VaList{args: va} }

// VaOf returns the VaList that ap points to: one that VaStart or VaCopy
// made.
//
//go:nocheckptr
func VaOf(ap *Void) *VaList {
	v := (*VaList)(unseal(&vaLists, ap, vaListType))
	if v == nil {
		RaiseFault("va_arg of what is not a va_list that va_start set", uintptr(unsafe.Pointer(ap)))
	}
	return v
}

// VaCopy is va_copy.
func VaCopy(ap *Void) *Void { return VaStart(VaOf(ap).args) }

func (v *VaList) Next() any {
	if len(v.args) == 0 {
		RaiseFault("va_arg past the last argument", 0)
	}
	a := v.args[0]
	v.args = v.args[1:]
	return a
}

// VaArg is va_arg(ap, T) of a scalar T. An argument of another type of
// the same size and class is converted, as C allows for signedness. An
// integer that is read as a pointer is the pointer that the integer
// converts to (IntPtr): one that cannot be used, if it is the address
// of something.
func VaArg[T any](ap *Void) T {
	a := VaOf(ap).Next()
	if v, ok := a.(T); ok {
		return v
	}
	var r T
	if _, ok := any(r).(*Void); ok {
		// An integer passed for a pointer (0 for a null pointer).
		if n, ok := intArgValue(a); ok {
			return any(IntPtr(uintptr(n))).(T)
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
	if p != nil && q != nil {
		memmove(unsafe.Pointer(q), unsafe.Pointer(p), min(n, reallocSize(unsafe.Pointer(p))))
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
