// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflectlite

import (
	"internal/abi"
	"internal/goarch"
	"internal/unsafeheader"
	"runtime"
	"unsafe"
)

// Value is the reflection interface to a Go value.
//
// Not all methods apply to all kinds of values. Restrictions,
// if any, are noted in the documentation for each method.
// Use the Kind method to find out the kind of value before
// calling kind-specific methods. Calling a method
// inappropriate to the kind of type causes a run time panic.
//
// The zero Value represents no value.
// Its IsValid method returns false, its Kind method returns Invalid,
// its String method returns "<invalid Value>", and all other methods panic.
// Most functions and methods never return an invalid value.
// If one does, its documentation states the conditions explicitly.
//
// A Value can be used concurrently by multiple goroutines provided that
// the underlying Go value can be used concurrently for the equivalent
// direct operations.
//
// To compare two Values, compare the results of the Interface method.
// Using == on two Values does not compare the underlying values
// they represent.
type Value struct {
	// typ_ holds the type of the value represented by a Value.
	// Access using the typ method to avoid escape of v.
	typ_ *abi.Type

	// Pointer-valued data or, if flagIndir is set, pointer to data.
	// Valid when either flagIndir is set or typ.pointers() is true.
	// If flagInline is set, ptr is ignored and data lives in inline.
	ptr unsafe.Pointer

	// inline stores the bits of pointer-free values up to 16 bytes
	// (int64, complex128, small structs). Used when flagInline is set,
	// in which case unpackEface did not allocate a heap copy. See
	// reflect.Value for details on lifetime constraints.
	inline complex128

	// flag bits mirror reflect.Value. See there for layout.
	flag

	// A method value represents a curried method invocation
	// like r.Read for some receiver r. The typ+val+flag bits describe
	// the receiver r, but the flag's Kind bits say Func (methods are
	// functions), and the top bits of the flag give the method number
	// in r's type's method table.
}

type flag uintptr

const (
	flagKindWidth        = 5 // there are 27 kinds
	flagKindMask    flag = 1<<flagKindWidth - 1
	flagStickyRO    flag = 1 << 5
	flagEmbedRO     flag = 1 << 6
	flagIndir       flag = 1 << 7
	flagAddr        flag = 1 << 8
	flagMethod      flag = 1 << 9
	flagInline      flag = 1 << 10
	flagSpread      flag = 1 << 11
	flagMethodShift      = 12
	flagRO          flag = flagStickyRO | flagEmbedRO
)

func (f flag) kind() Kind {
	return Kind(f & flagKindMask)
}

func (f flag) ro() flag {
	if f&flagRO != 0 {
		return flagStickyRO
	}
	return 0
}

func (v Value) typ() *abi.Type {
	// Types are either static (for compiler-created types) or
	// heap-allocated but always reachable (for reflection-created
	// types, held in the central map). So there is no need to
	// escape types. noescape here help avoid unnecessary escape
	// of v.
	return (*abi.Type)(abi.NoEscape(unsafe.Pointer(v.typ_)))
}

// dataPtr returns a pointer to v's underlying data. When flagInline is
// set the returned pointer refers to v.inline; when flagSpread is set
// it refers to &v.ptr (start of the 24 B spread header). See
// reflect.Value.dataPtr for lifetime constraints.
//
//go:nosplit
func (v *Value) dataPtr() unsafe.Pointer {
	if v.flag&flagInline != 0 {
		return unsafe.Pointer(&v.inline)
	}
	if v.flag&flagSpread != 0 {
		return unsafe.Pointer(&v.ptr)
	}
	return v.ptr
}

// materialize moves inline or spread data to a fresh heap allocation so
// that v.ptr is a stable address. After it returns flagInline /
// flagSpread is cleared.
func (v *Value) materialize() {
	if v.flag&flagInline != 0 {
		t := v.typ()
		c := unsafe_New(t)
		typedmemmove(t, c, unsafe.Pointer(&v.inline))
		v.ptr = c
		v.flag &^= flagInline
		return
	}
	if v.flag&flagSpread != 0 {
		t := v.typ()
		c := unsafe_New(t)
		typedmemmove(t, c, unsafe.Pointer(&v.ptr))
		v.ptr = c
		v.flag &^= flagSpread
	}
}

// pointer returns the underlying pointer represented by v.
// v.Kind() must be Pointer, Map, Chan, Func, or UnsafePointer
func (v Value) pointer() unsafe.Pointer {
	if v.typ().Size() != goarch.PtrSize || !v.typ().Pointers() {
		panic("can't call pointer on a non-pointer Value")
	}
	if v.flag&flagIndir != 0 {
		return *(*unsafe.Pointer)(v.dataPtr())
	}
	return v.dataPtr()
}

// packEface converts v to the empty interface.
func packEface(v Value) any {
	t := v.typ()
	var i any
	e := (*abi.EmptyInterface)(unsafe.Pointer(&i))
	// First, fill in the data portion of the interface.
	switch {
	case t.IsInlineIface():
		// gd fat-iface: payload lives in the Inline slot, Data stays nil.
		src := v.dataPtr()
		if v.flag&(flagIndir|flagInline) != 0 {
			typedmemmove(t, unsafe.Pointer(&e.Inline), src)
		} else {
			// Inline types are pointer-free so they are never IsDirectIface;
			// if we ever get here the bits live in v.ptr itself.
			*(*unsafe.Pointer)(unsafe.Pointer(&e.Inline)) = src
		}
	case t.IsSpreadIface():
		// gd Phase D: Value holds a 24 B header at v.ptr. Split it
		// back into the iface's data slot (word 0) and inline slot
		// (words 1+2) — no heap alloc. Mirrors reflect.packEface.
		src := v.dataPtr()
		e.Data = *(*unsafe.Pointer)(src)
		*(*[16]byte)(unsafe.Pointer(&e.Inline)) = *(*[16]byte)(unsafe.Pointer(uintptr(src) + goarch.PtrSize))
	case !t.IsDirectIface():
		if v.flag&flagIndir == 0 {
			panic("bad indir")
		}
		// Value is indirect, and so is the interface we're making.
		ptr := v.dataPtr()
		if v.flag&flagAddr != 0 {
			c := unsafe_New(t)
			typedmemmove(t, c, ptr)
			ptr = c
		}
		e.Data = ptr
	case v.flag&flagIndir != 0:
		// Value is indirect, but interface is direct. We need
		// to load the data at v.ptr into the interface data word.
		e.Data = *(*unsafe.Pointer)(v.dataPtr())
	default:
		// Value is direct, and so is the interface.
		e.Data = v.dataPtr()
	}
	// Now, fill in the type portion. We're very careful here not
	// to have any operation between the e.word and e.typ assignments
	// that would let the garbage collector observe the partially-built
	// interface value.
	e.Type = t
	return i
}

// unpackEface converts the empty interface i to a Value.
func unpackEface(i any) Value {
	e := (*abi.EmptyInterface)(unsafe.Pointer(&i))
	// NOTE: don't read e.word until we know whether it is really a pointer or not.
	t := e.Type
	if t == nil {
		return Value{}
	}
	f := flag(t.Kind())
	if !t.IsDirectIface() {
		f |= flagIndir
	}
	if t.IsInlineIface() {
		// gd fat-iface: carry the inline payload inside Value itself so we
		// don't have to heap-allocate just to give v.ptr a stable address.
		var v Value
		v.typ_ = t
		typedmemmove(t, unsafe.Pointer(&v.inline), unsafe.Pointer(&e.Inline))
		v.flag = f | flagInline
		return v
	}
	if t.IsSpreadIface() {
		// gd Phase D: carry the 24 B spread header inside Value itself
		// — word 0 in v.ptr, words 1+2 in v.inline. dataPtr returns
		// &v.ptr so accessors see a contiguous header without a heap
		// alloc. Mirrors reflect.unpackEface's spread path.
		var v Value
		v.typ_ = t
		v.ptr = e.Data
		*(*[16]byte)(unsafe.Pointer(&v.inline)) = *(*[16]byte)(unsafe.Pointer(&e.Inline))
		v.flag = f | flagSpread
		return v
	}
	return Value{typ_: t, ptr: e.Data, flag: f}
}

// A ValueError occurs when a Value method is invoked on
// a Value that does not support it. Such cases are documented
// in the description of each method.
type ValueError struct {
	Method string
	Kind   Kind
}

func (e *ValueError) Error() string {
	if e.Kind == 0 {
		return "reflect: call of " + e.Method + " on zero Value"
	}
	return "reflect: call of " + e.Method + " on " + e.Kind.String() + " Value"
}

// methodName returns the name of the calling method,
// assumed to be two stack frames above.
func methodName() string {
	pc, _, _, _ := runtime.Caller(2)
	f := runtime.FuncForPC(pc)
	if f == nil {
		return "unknown method"
	}
	return f.Name()
}

// mustBeExported panics if f records that the value was obtained using
// an unexported field.
func (f flag) mustBeExported() {
	if f == 0 {
		panic(&ValueError{methodName(), 0})
	}
	if f&flagRO != 0 {
		panic("reflect: " + methodName() + " using value obtained using unexported field")
	}
}

// mustBeAssignable panics if f records that the value is not assignable,
// which is to say that either it was obtained using an unexported field
// or it is not addressable.
func (f flag) mustBeAssignable() {
	if f == 0 {
		panic(&ValueError{methodName(), abi.Invalid})
	}
	// Assignable if addressable and not read-only.
	if f&flagRO != 0 {
		panic("reflect: " + methodName() + " using value obtained using unexported field")
	}
	if f&flagAddr == 0 {
		panic("reflect: " + methodName() + " using unaddressable value")
	}
}

// CanSet reports whether the value of v can be changed.
// A Value can be changed only if it is addressable and was not
// obtained by the use of unexported struct fields.
// If CanSet returns false, calling Set or any type-specific
// setter (e.g., SetBool, SetInt) will panic.
func (v Value) CanSet() bool {
	return v.flag&(flagAddr|flagRO) == flagAddr
}

// Elem returns the value that the interface v contains
// or that the pointer v points to.
// It panics if v's Kind is not Interface or Pointer.
// It returns the zero Value if v is nil.
func (v Value) Elem() Value {
	k := v.kind()
	switch k {
	case abi.Interface:
		var eface any
		if v.typ().NumMethod() == 0 {
			eface = *(*any)(v.dataPtr())
		} else {
			eface = (any)(*(*interface {
				M()
			})(v.dataPtr()))
		}
		x := unpackEface(eface)
		if x.flag != 0 {
			x.flag |= v.flag.ro()
		}
		return x
	case abi.Pointer:
		ptr := v.dataPtr()
		if v.flag&flagIndir != 0 {
			ptr = *(*unsafe.Pointer)(ptr)
		}
		// The returned value's address is v's value.
		if ptr == nil {
			return Value{}
		}
		tt := (*ptrType)(unsafe.Pointer(v.typ()))
		typ := tt.Elem
		fl := v.flag&flagRO | flagIndir | flagAddr
		fl |= flag(typ.Kind())
		return Value{typ_: typ, ptr: ptr, flag: fl}
	}
	panic(&ValueError{"reflectlite.Value.Elem", v.kind()})
}

func valueInterface(v Value) any {
	if v.flag == 0 {
		panic(&ValueError{"reflectlite.Value.Interface", 0})
	}

	if v.kind() == abi.Interface {
		// Special case: return the element inside the interface.
		// Empty interface has one layout, all interfaces with
		// methods have a second layout.
		if v.numMethod() == 0 {
			return *(*any)(v.dataPtr())
		}
		return *(*interface {
			M()
		})(v.dataPtr())
	}

	return packEface(v)
}

// IsNil reports whether its argument v is nil. The argument must be
// a chan, func, interface, map, pointer, or slice value; if it is
// not, IsNil panics. Note that IsNil is not always equivalent to a
// regular comparison with nil in Go. For example, if v was created
// by calling ValueOf with an uninitialized interface variable i,
// i==nil will be true but v.IsNil will panic as v will be the zero
// Value.
func (v Value) IsNil() bool {
	k := v.kind()
	switch k {
	case abi.Chan, abi.Func, abi.Map, abi.Pointer, abi.UnsafePointer:
		// if v.flag&flagMethod != 0 {
		// 	return false
		// }
		ptr := v.dataPtr()
		if v.flag&flagIndir != 0 {
			ptr = *(*unsafe.Pointer)(ptr)
		}
		return ptr == nil
	case abi.Interface, abi.Slice:
		// Both interface and slice are nil if first word is 0.
		// Both are always bigger than a word; assume flagIndir.
		return *(*unsafe.Pointer)(v.dataPtr()) == nil
	}
	panic(&ValueError{"reflectlite.Value.IsNil", v.kind()})
}

// IsValid reports whether v represents a value.
// It returns false if v is the zero Value.
// If IsValid returns false, all other methods except String panic.
// Most functions and methods never return an invalid Value.
// If one does, its documentation states the conditions explicitly.
func (v Value) IsValid() bool {
	return v.flag != 0
}

// Kind returns v's Kind.
// If v is the zero Value (IsValid returns false), Kind returns Invalid.
func (v Value) Kind() Kind {
	return v.kind()
}

// implemented in runtime:

//go:noescape
func chanlen(unsafe.Pointer) int

//go:noescape
func maplen(unsafe.Pointer) int

// Len returns v's length.
// It panics if v's Kind is not Array, Chan, Map, Slice, or String.
func (v Value) Len() int {
	k := v.kind()
	switch k {
	case abi.Array:
		tt := (*arrayType)(unsafe.Pointer(v.typ()))
		return int(tt.Len)
	case abi.Chan:
		return chanlen(v.pointer())
	case abi.Map:
		return maplen(v.pointer())
	case abi.Slice:
		// Slice is bigger than a word; assume flagIndir.
		return (*unsafeheader.Slice)(v.dataPtr()).Len
	case abi.String:
		// String is bigger than a word; assume flagIndir.
		return (*unsafeheader.String)(v.dataPtr()).Len
	}
	panic(&ValueError{"reflect.Value.Len", v.kind()})
}

// NumMethod returns the number of exported methods in the value's method set.
func (v Value) numMethod() int {
	if v.typ() == nil {
		panic(&ValueError{"reflectlite.Value.NumMethod", abi.Invalid})
	}
	return v.typ().NumMethod()
}

// Set assigns x to the value v.
// It panics if CanSet returns false.
// As in Go, x's value must be assignable to v's type.
func (v Value) Set(x Value) {
	v.mustBeAssignable()
	x.mustBeExported() // do not let unexported x leak
	var target unsafe.Pointer
	if v.kind() == abi.Interface {
		target = v.dataPtr()
	}
	x = x.assignTo("reflectlite.Set", v.typ(), target)
	if x.flag&flagIndir != 0 {
		typedmemmove(v.typ(), v.dataPtr(), (&x).dataPtr())
	} else {
		*(*unsafe.Pointer)(v.dataPtr()) = x.ptr
	}
}

// Type returns v's type.
func (v Value) Type() Type {
	f := v.flag
	if f == 0 {
		panic(&ValueError{"reflectlite.Value.Type", abi.Invalid})
	}
	// Method values not supported.
	return toRType(v.typ())
}

/*
 * constructors
 */

// implemented in package runtime

//go:noescape
func unsafe_New(*abi.Type) unsafe.Pointer

// ValueOf returns a new Value initialized to the concrete value
// stored in the interface i. ValueOf(nil) returns the zero Value.
func ValueOf(i any) Value {
	if i == nil {
		return Value{}
	}
	return unpackEface(i)
}

// assignTo returns a value v that can be assigned directly to typ.
// It panics if v is not assignable to typ.
// For a conversion to an interface type, target is a suggested scratch space to use.
func (v Value) assignTo(context string, dst *abi.Type, target unsafe.Pointer) Value {
	// if v.flag&flagMethod != 0 {
	// 	v = makeMethodValue(context, v)
	// }

	switch {
	case directlyAssignable(dst, v.typ()):
		// Overwrite type so that they match.
		// Same memory layout, so no harm done.
		fl := v.flag&(flagAddr|flagIndir|flagInline|flagSpread) | v.flag.ro()
		fl |= flag(dst.Kind())
		out := v
		out.typ_ = dst
		out.flag = fl
		return out

	case implements(dst, v.typ()):
		if target == nil {
			target = unsafe_New(dst)
		}
		if v.Kind() == abi.Interface && v.IsNil() {
			// A nil ReadWriter passed to nil Reader is OK,
			// but using ifaceE2I below will panic.
			// Avoid the panic by returning a nil dst (e.g., Reader) explicitly.
			return Value{typ_: dst, ptr: nil, flag: flag(abi.Interface)}
		}
		x := valueInterface(v)
		if dst.NumMethod() == 0 {
			*(*any)(target) = x
		} else {
			ifaceE2I(dst, x, target)
		}
		return Value{typ_: dst, ptr: target, flag: flagIndir | flag(abi.Interface)}
	}

	// Failed.
	panic(context + ": value of type " + toRType(v.typ()).String() + " is not assignable to type " + toRType(dst).String())
}

// arrayAt returns the i-th element of p,
// an array whose elements are eltSize bytes wide.
// The array pointed at by p must have at least i+1 elements:
// it is invalid (but impossible to check here) to pass i >= len,
// because then the result will point outside the array.
// whySafe must explain why i < len. (Passing "i < len" is fine;
// the benefit is to surface this assumption at the call site.)
func arrayAt(p unsafe.Pointer, i int, eltSize uintptr, whySafe string) unsafe.Pointer {
	return add(p, uintptr(i)*eltSize, "i < len")
}

func ifaceE2I(t *abi.Type, src any, dst unsafe.Pointer)

// typedmemmove copies a value of type t to dst from src.
//
//go:noescape
func typedmemmove(t *abi.Type, dst, src unsafe.Pointer)

// Dummy annotation marking that the value x escapes,
// for use in cases where the reflect code is so clever that
// the compiler cannot follow.
func escapes(x any) {
	if dummy.b {
		dummy.x = x
	}
}

var dummy struct {
	b bool
	x any
}
