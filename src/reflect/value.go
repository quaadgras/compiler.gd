// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect

import (
	"errors"
	"internal/abi"
	"internal/goarch"
	"internal/strconv"
	"internal/unsafeheader"
	"iter"
	"math"
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
// Its [Value.IsValid] method returns false, its Kind method returns [Invalid],
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
	// If flagSpread is set, ptr holds word 0 of a 24-byte spread header
	// (string or slice) and inline holds words 1+2 — the whole 24 B
	// layout lives contiguously at &ptr.
	ptr unsafe.Pointer

	// inline stores the bits of the value directly, for pointer-free
	// types whose size fits in 16 bytes (int64, complex128, small
	// structs, etc.). Used only when flagInline is set — in which case
	// unpackEface did not allocate a heap copy. When flagSpread is set,
	// inline holds words 1+2 of the spread value (word 0 is in ptr).
	//
	// Because inline lives inside the Value struct itself, its address
	// is only valid while the owning Value is on the stack/heap; do not
	// stash &v.inline into a sub-Value. Call materialize() first when
	// a stable pointer is required.
	inline complex128

	// flag holds metadata about the value.
	//
	// The lowest five bits give the Kind of the value, mirroring typ.Kind().
	//
	// The next set of bits are flag bits:
	//	- flagStickyRO: obtained via unexported not embedded field, so read-only
	//	- flagEmbedRO: obtained via unexported embedded field, so read-only
	//	- flagIndir: val holds a pointer to the data
	//	- flagAddr: v.CanAddr is true (implies flagIndir and ptr is non-nil)
	//	- flagMethod: v is a method value.
	//	- flagInline: v's data lives in the inline field (implies flagIndir,
	//	  excludes flagAddr since the inline storage has no stable address).
	//	- flagSpread: v's data is a 24-byte spread header (string or slice)
	//	  laid out contiguously across ptr (word 0) and inline (words 1+2).
	//	  Implies flagIndir. Like flagInline, excludes flagAddr. Set by
	//	  unpackEface for spread types so ValueOf doesn't heap-alloc.
	// If !typ.IsDirectIface(), code can assume that flagIndir is set.
	//
	// The remaining 21+ bits give a method number for method values.
	// If flag.kind() != Func, code can assume that flagMethod is unset.
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

// typ returns the *abi.Type stored in the Value. This method is fast,
// but it doesn't always return the correct type for the Value.
// See abiType and Type, which do return the correct type.
func (v Value) typ() *abi.Type {
	// Types are either static (for compiler-created types) or
	// heap-allocated but always reachable (for reflection-created
	// types, held in the central map). So there is no need to
	// escape types. noescape here help avoid unnecessary escape
	// of v.
	return (*abi.Type)(abi.NoEscape(unsafe.Pointer(v.typ_)))
}

// dataPtr returns a pointer to v's underlying data. When flagInline is
// set the returned pointer refers to v.inline inside the caller's local
// Value; when flagSpread is set it refers to &v.ptr (start of the 24 B
// spread header that spans v.ptr and v.inline). In both cases the
// returned pointer is valid only for the duration of the current method
// call and must not be stashed into a sub-Value or returned through a
// ptr field. Use materialize to obtain a stable pointer.
//
//go:nosplit
func (v *Value) dataPtr() unsafe.Pointer {
	if v.flag&flagInline != 0 {
		return abi.NoEscape(unsafe.Pointer(&v.inline))
	}
	if v.flag&flagSpread != 0 {
		return abi.NoEscape(unsafe.Pointer(&v.ptr))
	}
	return v.ptr
}

// materialize moves inline or spread data to a fresh heap allocation so
// that v.ptr is a stable address suitable for sub-Values (Field, Index,
// Elem, Addr). After materialize the value is no longer flagInline /
// flagSpread and v.ptr points at the heap copy. Callers that only need
// to read data should use dataPtr instead to avoid the allocation.
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
		// The 24 B header lives at &v.ptr (ptr, inline-lo, inline-hi).
		// Copy it out before overwriting v.ptr with the heap copy.
		t := v.typ()
		c := unsafe_New(t)
		typedmemmove(t, c, unsafe.Pointer(&v.ptr))
		v.ptr = c
		v.flag &^= flagSpread
	}
}

// pointer returns the underlying pointer represented by v.
// v.Kind() must be Pointer, Map, Chan, Func, or UnsafePointer
// if v.Kind() == Pointer, the base type must not be not-in-heap.
func (v Value) pointer() unsafe.Pointer {
	if v.typ_.Size_ != goarch.PtrSize || v.typ_.PtrBytes == 0 {
		panic("can't call pointer on a non-pointer Value")
	}
	if v.flag&flagIndir != 0 {
		return *(*unsafe.Pointer)(v.ptr)
	}
	return v.ptr
}

// packEface converts v to the empty interface.
func packEface(v Value) any {
	t := v.typ()
	var ei abi.EmptyInterface
	if t.IsInlineIface() {
		// gd fat-iface: payload lives in the Inline slot, Data stays nil.
		src := v.dataPtr()
		if v.flag&(flagIndir|flagInline) != 0 {
			typedmemmove(t, unsafe.Pointer(&ei.Inline), src)
		} else {
			// Inline types are pointer-free so they are never IsDirectIface;
			// if a non-indir Value somehow reaches here its bits live in v.ptr.
			*(*unsafe.Pointer)(unsafe.Pointer(&ei.Inline)) = src
		}
	} else if t.IsSpreadIface() {
		// gd Phase D: reflect stores spread-iface values with flagIndir
		// pointing at a 24 B heap-allocated header. Unpack word 0 into
		// ei.Data and words 1+2 into ei.Inline so the iface layout
		// matches the compiler's 4-slot OMAKEFACE.
		src := v.dataPtr()
		ei.Data = *(*unsafe.Pointer)(src)
		*(*[16]byte)(unsafe.Pointer(&ei.Inline)) = *(*[16]byte)(unsafe.Pointer(uintptr(src) + goarch.PtrSize))
	} else {
		ei.Data = packEfaceData(v)
	}
	ei.Type = t
	return *(*any)(unsafe.Pointer(&ei))
}

// packEfaceData is a helper that packs the Data part of an interface,
// if v were to be stored in an interface. Callers must ensure that
// v.typ() is not IsInlineIface — those use the Inline slot instead.
func packEfaceData(v Value) unsafe.Pointer {
	t := v.typ()
	switch {
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
		return ptr
	case v.flag&flagIndir != 0:
		// Value is indirect, but interface is direct. We need
		// to load the data at v.ptr into the interface data word.
		return *(*unsafe.Pointer)(v.dataPtr())
	default:
		// Value is direct, and so is the interface.
		return v.dataPtr()
	}
}

// unpackEface converts the empty interface i to a Value.
func unpackEface(i any) Value {
	e := (*abi.EmptyInterface)(unsafe.Pointer(&i))
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
		// don't have to heap-allocate just to make v.ptr stable. The
		// returned Value's inline field is a fresh copy of e.Inline.
		var v Value
		v.typ_ = t
		typedmemmove(t, unsafe.Pointer(&v.inline), unsafe.Pointer(&e.Inline))
		v.flag = f | flagInline
		return v
	}
	if t.IsSpreadIface() {
		// gd Phase D: store the 24 B spread header inside the Value
		// itself — word 0 in v.ptr, words 1+2 in v.inline. dataPtr
		// returns &v.ptr so accessors see a contiguous header without
		// a heap alloc. GC stays honest because v.ptr holds a real
		// heap pointer (string bytes / slice backing array).
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
// a [Value] that does not support it. Such cases are documented
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

// valueMethodName returns the name of the exported calling method on Value.
func valueMethodName() string {
	var pc [5]uintptr
	n := runtime.Callers(1, pc[:])
	frames := runtime.CallersFrames(pc[:n])
	var frame runtime.Frame
	for more := true; more; {
		const prefix = "reflect.Value."
		frame, more = frames.Next()
		name := frame.Function
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			methodName := name[len(prefix):]
			if len(methodName) > 0 && 'A' <= methodName[0] && methodName[0] <= 'Z' {
				return name
			}
		}
	}
	return "unknown method"
}

// nonEmptyInterface is the header for an interface value with methods.
// Under gd it carries the 16-byte Inline payload so reflect can read the
// right receiver word without peeking past the struct.
type nonEmptyInterface struct {
	itab   *abi.ITab
	word   unsafe.Pointer
	inline complex128
}

// mustBe panics if f's kind is not expected.
// Making this a method on flag instead of on Value
// (and embedding flag in Value) means that we can write
// the very clear v.mustBe(Bool) and have it compile into
// v.flag.mustBe(Bool), which will only bother to copy the
// single important word for the receiver.
func (f flag) mustBe(expected Kind) {
	// TODO(mvdan): use f.kind() again once mid-stack inlining gets better
	if Kind(f&flagKindMask) != expected {
		panic(&ValueError{valueMethodName(), f.kind()})
	}
}

// mustBeExported panics if f records that the value was obtained using
// an unexported field.
func (f flag) mustBeExported() {
	if f == 0 || f&flagRO != 0 {
		f.mustBeExportedSlow()
	}
}

func (f flag) mustBeExportedSlow() {
	if f == 0 {
		panic(&ValueError{valueMethodName(), Invalid})
	}
	if f&flagRO != 0 {
		panic("reflect: " + valueMethodName() + " using value obtained using unexported field")
	}
}

// mustBeAssignable panics if f records that the value is not assignable,
// which is to say that either it was obtained using an unexported field
// or it is not addressable.
func (f flag) mustBeAssignable() {
	if f&flagRO != 0 || f&flagAddr == 0 {
		f.mustBeAssignableSlow()
	}
}

func (f flag) mustBeAssignableSlow() {
	if f == 0 {
		panic(&ValueError{valueMethodName(), Invalid})
	}
	// Assignable if addressable and not read-only.
	if f&flagRO != 0 {
		panic("reflect: " + valueMethodName() + " using value obtained using unexported field")
	}
	if f&flagAddr == 0 {
		panic("reflect: " + valueMethodName() + " using unaddressable value")
	}
}

// Addr returns a pointer value representing the address of v.
// It panics if [Value.CanAddr] returns false.
// Addr is typically used to obtain a pointer to a struct field
// or slice element in order to call a method that requires a
// pointer receiver.
func (v Value) Addr() Value {
	if v.flag&flagAddr == 0 {
		panic("reflect.Value.Addr of unaddressable value")
	}
	// Preserve flagRO instead of using v.flag.ro() so that
	// v.Addr().Elem() is equivalent to v (#32772)
	// Addr requires flagAddr, which is incompatible with flagInline, so v.ptr is stable.
	fl := v.flag & flagRO
	return Value{typ_: ptrTo(v.typ()), ptr: v.ptr, flag: fl | flag(Pointer)}
}

// Bool returns v's underlying value.
// It panics if v's kind is not [Bool].
func (v Value) Bool() bool {
	// panicNotBool is split out to keep Bool inlineable.
	v.panicNotBool()
	if v.flag&flagInline != 0 {
		return v.inline != 0
	}
	return *(*bool)(v.ptr)
}

func (v Value) panicNotBool() {
	if v.kind() != Bool {
		v.mustBe(Bool)
	}
}

var bytesType = rtypeOf(([]byte)(nil))

// Bytes returns v's underlying value.
// It panics if v's underlying value is not a slice of bytes or
// an addressable array of bytes.
func (v Value) Bytes() []byte {
	// Fast path: spread []byte — the common case after fat-iface, since
	// every reflect.ValueOf([]byte) lands here. Non-spread []byte (e.g.
	// a slice element pulled out by Index, where flagIndir is set) and
	// other byte-shaped Values fall through to bytesSlow. Splitting
	// keeps Bytes inlinable.
	//
	// Spread layout: Data=v.ptr, Len=inline.lo, Cap=inline.hi.
	// abi.NoEscape hides the &v.ptr address-of from escape analysis;
	// without it, taking the address of v.ptr forces v's storage onto
	// the heap and cascades into "x escapes to heap" for callers like
	// reflect.ValueOf(structWithStringField).Field(i).Bytes().
	if v.typ_ == bytesType && v.flag&flagSpread != 0 { // ok to use v.typ_ directly as comparison doesn't cause escape
		return *(*[]byte)(abi.NoEscape(unsafe.Pointer(&v.ptr)))
	}
	return v.bytesSlow()
}

func (v Value) bytesSlow() []byte {
	switch v.kind() {
	case Slice:
		if v.typ().Elem().Kind() != abi.Uint8 {
			panic("reflect.Value.Bytes of non-byte slice")
		}
		// Slice is always bigger than a word; assume flagIndir.
		return *(*[]byte)(v.dataPtr())
	case Array:
		if v.typ().Elem().Kind() != abi.Uint8 {
			panic("reflect.Value.Bytes of non-byte array")
		}
		if !v.CanAddr() {
			panic("reflect.Value.Bytes of unaddressable byte array")
		}
		p := (*byte)(v.dataPtr())
		n := int((*arrayType)(unsafe.Pointer(v.typ())).Len)
		return unsafe.Slice(p, n)
	}
	panic(&ValueError{"reflect.Value.Bytes", v.kind()})
}

// runes returns v's underlying value.
// It panics if v's underlying value is not a slice of runes (int32s).
func (v Value) runes() []rune {
	v.mustBe(Slice)
	if v.typ().Elem().Kind() != abi.Int32 {
		panic("reflect.Value.Bytes of non-rune slice")
	}
	// Slice is always bigger than a word; assume flagIndir.
	return *(*[]rune)(v.dataPtr())
}

// CanAddr reports whether the value's address can be obtained with [Value.Addr].
// Such values are called addressable. A value is addressable if it is
// an element of a slice, an element of an addressable array,
// a field of an addressable struct, or the result of dereferencing a pointer.
// If CanAddr returns false, calling [Value.Addr] will panic.
func (v Value) CanAddr() bool {
	return v.flag&flagAddr != 0
}

// CanSet reports whether the value of v can be changed.
// A [Value] can be changed only if it is addressable and was not
// obtained by the use of unexported struct fields.
// If CanSet returns false, calling [Value.Set] or any type-specific
// setter (e.g., [Value.SetBool], [Value.SetInt]) will panic.
func (v Value) CanSet() bool {
	return v.flag&(flagAddr|flagRO) == flagAddr
}

// Call calls the function v with the input arguments in.
// For example, if len(in) == 3, v.Call(in) represents the Go call v(in[0], in[1], in[2]).
// Call panics if v's Kind is not [Func].
// It returns the output results as Values.
// As in Go, each input argument must be assignable to the
// type of the function's corresponding input parameter.
// If v is a variadic function, Call creates the variadic slice parameter
// itself, copying in the corresponding values.
// It panics if the Value was obtained by accessing unexported struct fields.
//
// gd: the returned slice reuses in's backing array when cap(in) is at
// least the number of return values, so callers in hot paths can
// preallocate `in := make([]Value, 0, maxArgs)` once and reuse it for
// every call — zero []Value allocation per call. Callers that kept a
// reference to in observe the returned results at in[:nout] instead of
// the original arguments. Patterns like calling two functions back to
// back with the same in slice must clone before the second call.
func (v Value) Call(in []Value) []Value {
	v.mustBe(Func)
	v.mustBeExported()
	return v.call("Call", in)
}

// CallSlice calls the variadic function v with the input arguments in,
// assigning the slice in[len(in)-1] to v's final variadic argument.
// For example, if len(in) == 3, v.CallSlice(in) represents the Go call v(in[0], in[1], in[2]...).
// CallSlice panics if v's Kind is not [Func] or if v is not variadic.
// It returns the output results as Values.
// As in Go, each input argument must be assignable to the
// type of the function's corresponding input parameter.
// It panics if the Value was obtained by accessing unexported struct fields.
//
// gd: see [Value.Call] — the returned slice reuses in's backing array
// when cap(in) is at least the number of return values.
func (v Value) CallSlice(in []Value) []Value {
	v.mustBe(Func)
	v.mustBeExported()
	return v.call("CallSlice", in)
}

var callGC bool // for testing; see TestCallMethodJump and TestCallArgLive

const debugReflectCall = false

func (v Value) call(op string, in []Value) []Value {
	// Get function pointer, type.
	t := (*funcType)(unsafe.Pointer(v.typ()))
	var (
		fn       unsafe.Pointer
		rcvr     Value
		rcvrtype *abi.Type
	)
	if v.flag&flagMethod != 0 {
		rcvr = v
		rcvrtype, t, fn = methodReceiver(op, v, int(v.flag)>>flagMethodShift)
	} else if v.flag&flagIndir != 0 {
		fn = *(*unsafe.Pointer)(v.dataPtr())
	} else {
		fn = v.dataPtr()
	}

	if fn == nil {
		panic("reflect.Value.Call: call of nil function")
	}

	isSlice := op == "CallSlice"
	// gd Phase G.2.1: FuncType.InCount includes synthesised outBuf
	// params. The user-facing arg-count check and type-assignability
	// check operate on the user-visible count (NumUserIn); outBufs
	// are padded with typed nils further down so the reflectcall
	// frame still matches the callee's extended ABI.
	nOut := t.NumOutBufs()
	n := t.NumUserIn()
	isVariadic := t.IsVariadic()
	if isSlice {
		if !isVariadic {
			panic("reflect: CallSlice of non-variadic function")
		}
		if len(in) < n {
			panic("reflect: CallSlice with too few input arguments")
		}
		if len(in) > n {
			panic("reflect: CallSlice with too many input arguments")
		}
	} else {
		if isVariadic {
			n--
		}
		if len(in) < n {
			panic("reflect: Call with too few input arguments")
		}
		if !isVariadic && len(in) > n {
			panic("reflect: Call with too many input arguments")
		}
	}
	for _, x := range in {
		if x.Kind() == Invalid {
			panic("reflect: " + op + " using zero Value argument")
		}
	}
	for i := 0; i < n; i++ {
		if xt, targ := in[i].Type(), t.In(i); !xt.AssignableTo(toRType(targ)) {
			panic("reflect: " + op + " using " + xt.String() + " as type " + stringFor(targ))
		}
	}
	if !isSlice && isVariadic {
		// prepare slice for remaining values
		m := len(in) - n
		slice := MakeSlice(toRType(t.In(n)), m, m)
		elem := toRType(t.In(n)).Elem() // FIXME cast to slice type and Elem()
		for i := 0; i < m; i++ {
			x := in[n+i]
			if xt := x.Type(); !xt.AssignableTo(elem) {
				panic("reflect: cannot use " + xt.String() + " as type " + elem.String() + " in " + op)
			}
			slice.Index(i).Set(x)
		}
		origIn := in
		in = make([]Value, n+1)
		copy(in[:n], origIn)
		in[n] = slice
	}

	// gd Phase G.2.1: pad with typed-nil outBuf Values so the
	// marshaling loop below covers the full extended frame. Each
	// outBuf slot has type unsafe.Pointer; Zero() gives us a
	// correctly-shaped nil that marshals to an empty register /
	// stack slot.
	if nOut > 0 {
		ins := t.InSlice()
		padded := make([]Value, 0, len(in)+nOut)
		padded = append(padded, in...)
		totalIn := t.NumIn() // masks PhaseGExtendedFlag
		for i := totalIn - nOut; i < totalIn; i++ {
			padded = append(padded, Zero(toRType(ins[i])))
		}
		in = padded
	}

	nin := len(in)
	if nin != t.NumIn() {
		panic("reflect.Value.Call: wrong argument count")
	}
	nout := t.NumOut()

	// Register argument space.
	var regArgs abi.RegArgs

	// Compute frame type.
	frametype, framePool, abid := funcLayout(t, rcvrtype)

	// Allocate a chunk of memory for frame if needed.
	var stackArgs unsafe.Pointer
	if frametype.Size() != 0 {
		if nout == 0 {
			stackArgs = framePool.Get().(unsafe.Pointer)
		} else {
			// Can't use pool if the function has return values.
			// We will leak pointer to args in ret, so its lifetime is not scoped.
			stackArgs = unsafe_New(frametype)
		}
	}
	frameSize := frametype.Size()

	if debugReflectCall {
		println("reflect.call", stringFor(&t.Type))
		abid.dump()
	}

	// Copy inputs into args.

	// Handle receiver.
	inStart := 0
	if rcvrtype != nil {
		// Guaranteed to only be one word in size,
		// so it will only take up exactly 1 abiStep (either
		// in a register or on the stack).
		switch st := abid.call.steps[0]; st.kind {
		case abiStepStack:
			storeRcvr(rcvr, stackArgs)
		case abiStepPointer:
			storeRcvr(rcvr, unsafe.Pointer(&regArgs.Ptrs[st.ireg]))
			fallthrough
		case abiStepIntReg:
			storeRcvr(rcvr, unsafe.Pointer(&regArgs.Ints[st.ireg]))
		case abiStepFloatReg:
			storeRcvr(rcvr, unsafe.Pointer(&regArgs.Floats[st.freg]))
		default:
			panic("unknown ABI parameter kind")
		}
		inStart = 1
	}

	// Handle arguments.
	for i, v := range in {
		v.mustBeExported()
		targ := toRType(t.In(i))
		// TODO(mknyszek): Figure out if it's possible to get some
		// scratch space for this assignment check. Previously, it
		// was possible to use space in the argument frame.
		v = v.assignTo("reflect.Value.Call", &targ.t, nil)
	stepsLoop:
		for _, st := range abid.call.stepsForValue(i + inStart) {
			switch st.kind {
			case abiStepStack:
				// Copy values to the "stack."
				addr := add(stackArgs, st.stkOff, "precomputed stack arg offset")
				if v.flag&flagIndir != 0 {
					typedmemmove(&targ.t, addr, v.dataPtr())
				} else {
					*(*unsafe.Pointer)(addr) = v.dataPtr()
				}
				// There's only one step for a stack-allocated value.
				break stepsLoop
			case abiStepIntReg, abiStepPointer:
				// Copy values to "integer registers."
				if v.flag&flagIndir != 0 {
					offset := add(v.dataPtr(), st.offset, "precomputed value offset")
					if st.kind == abiStepPointer {
						// Duplicate this pointer in the pointer area of the
						// register space. Otherwise, there's the potential for
						// this to be the last reference to v.ptr.
						regArgs.Ptrs[st.ireg] = *(*unsafe.Pointer)(offset)
					}
					intToReg(&regArgs, st.ireg, st.size, offset)
				} else {
					if st.kind == abiStepPointer {
						// See the comment in abiStepPointer case above.
						regArgs.Ptrs[st.ireg] = v.dataPtr()
					}
					regArgs.Ints[st.ireg] = uintptr(v.dataPtr())
				}
			case abiStepFloatReg:
				// Copy values to "float registers."
				if v.flag&flagIndir == 0 {
					panic("attempted to copy pointer to FP register")
				}
				offset := add(v.dataPtr(), st.offset, "precomputed value offset")
				floatToReg(&regArgs, st.freg, st.size, offset)
			default:
				panic("unknown ABI part kind")
			}
		}
	}
	// TODO(mknyszek): Remove this when we no longer have
	// caller reserved spill space.
	frameSize = align(frameSize, goarch.PtrSize)
	frameSize += abid.spill

	// Mark pointers in registers for the return path.
	regArgs.ReturnIsPtr = abid.outRegPtrs

	if debugReflectCall {
		regArgs.Dump()
	}

	// For testing; see TestCallArgLive.
	if callGC {
		runtime.GC()
	}

	// Call.
	call(frametype, fn, stackArgs, uint32(frametype.Size()), uint32(abid.retOffset), uint32(frameSize), &regArgs)

	// For testing; see TestCallMethodJump.
	if callGC {
		runtime.GC()
	}

	var ret []Value
	if nout == 0 {
		if stackArgs != nil {
			typedmemclr(frametype, stackArgs)
			framePool.Put(stackArgs)
		}
	} else {
		if stackArgs != nil {
			// Zero the now unused input area of args,
			// because the Values returned by this function contain pointers to the args object,
			// and will thus keep the args object alive indefinitely.
			typedmemclrpartial(frametype, stackArgs, 0, abid.retOffset)
		}

		// Wrap Values around return values in args.
		//
		// gd: reuse the caller's input slice's backing array when it
		// already has capacity for nout. Callers in hot reflection
		// paths (encoders, marshalers, rpc dispatch) commonly alloc
		// one in []Value = make([]Value, 0, maxArgs) and hand the
		// same slice to each Call; no fresh []Value allocation per
		// call. Callers who retained a reference to in observe the
		// results at in[:nout] instead of the original arguments —
		// documented on Call / CallSlice.
		if cap(in) >= nout {
			ret = in[:nout]
			// Clear the reused backing so the per-slot fill below
			// sees zero-initialised Value headers.
			clear(ret)
		} else {
			ret = make([]Value, nout)
		}
		for i := 0; i < nout; i++ {
			tv := t.Out(i)
			if tv.Size() == 0 {
				// For zero-sized return value, args+off may point to the next object.
				// In this case, return the zero value instead.
				ret[i] = Zero(toRType(tv))
				continue
			}
			steps := abid.ret.stepsForValue(i)
			if st := steps[0]; st.kind == abiStepStack {
				// This value is on the stack. If part of a value is stack
				// allocated, the entire value is according to the ABI. So
				// just make an indirection into the allocated frame.
				fl := flagIndir | flag(tv.Kind())
				ret[i] = Value{typ_: tv, ptr: add(stackArgs, st.stkOff, "tv.Size() != 0"), flag: fl}
				// Note: this does introduce false sharing between results -
				// if any result is live, they are all live.
				// (And the space for the args is live as well, but as we've
				// cleared that space it isn't as big a deal.)
				continue
			}

			// Handle pointers passed in registers.
			if tv.IsDirectIface() {
				// Pointer-valued data gets put directly
				// into v.ptr.
				if steps[0].kind != abiStepPointer {
					print("kind=", steps[0].kind, ", type=", stringFor(tv), "\n")
					panic("mismatch between ABI description and types")
				}
				ret[i] = Value{typ_: tv, ptr: regArgs.Ptrs[steps[0].ireg], flag: flag(tv.Kind())}
				continue
			}

			// All that's left is values passed in registers that we need to
			// create space for and copy values back into.
			//
			// gd Phase D: for spread types (string / slice return), write
			// the three register halves directly into the Value's own
			// storage (ptr + inline, contiguous in the struct) and set
			// flagSpread — no heap alloc per call-return.
			if tv.IsSpreadIface() {
				var v Value
				v.typ_ = tv
				base := unsafe.Pointer(&v.ptr)
				for _, st := range steps {
					switch st.kind {
					case abiStepIntReg:
						offset := add(base, st.offset, "precomputed value offset")
						intFromReg(&regArgs, st.ireg, st.size, offset)
					case abiStepPointer:
						off := add(base, st.offset, "precomputed value offset")
						*((*unsafe.Pointer)(off)) = regArgs.Ptrs[st.ireg]
					case abiStepFloatReg:
						offset := add(base, st.offset, "precomputed value offset")
						floatFromReg(&regArgs, st.freg, st.size, offset)
					case abiStepStack:
						panic("register-based return value has stack component")
					default:
						panic("unknown ABI part kind")
					}
				}
				v.flag = flagIndir | flagSpread | flag(tv.Kind())
				ret[i] = v
				continue
			}
			// gd: pointer-free types that fit in the 16 B inline slot
			// (int, int64, small structs, complex128) ride inside the
			// returned Value rather than heap-allocating an 8–16 B
			// backing buffer. Same "materialise on demand" contract as
			// flagInline values produced by unpackEface.
			if tv.IsInlineIface() {
				var v Value
				v.typ_ = tv
				base := unsafe.Pointer(&v.inline)
				for _, st := range steps {
					switch st.kind {
					case abiStepIntReg:
						offset := add(base, st.offset, "precomputed value offset")
						intFromReg(&regArgs, st.ireg, st.size, offset)
					case abiStepPointer:
						panic("pointer in inline (pointer-free) return")
					case abiStepFloatReg:
						offset := add(base, st.offset, "precomputed value offset")
						floatFromReg(&regArgs, st.freg, st.size, offset)
					case abiStepStack:
						panic("register-based return value has stack component")
					default:
						panic("unknown ABI part kind")
					}
				}
				v.flag = flagIndir | flagInline | flag(tv.Kind())
				ret[i] = v
				continue
			}
			//
			// TODO(mknyszek): We make a new allocation for each register-allocated
			// value, but previously we could always point into the heap-allocated
			// stack frame. This is a regression that could be fixed by adding
			// additional space to the allocated stack frame and storing the
			// register-allocated return values into the allocated stack frame and
			// referring there in the resulting Value.
			s := unsafe_New(tv)
			for _, st := range steps {
				switch st.kind {
				case abiStepIntReg:
					offset := add(s, st.offset, "precomputed value offset")
					intFromReg(&regArgs, st.ireg, st.size, offset)
				case abiStepPointer:
					s := add(s, st.offset, "precomputed value offset")
					*((*unsafe.Pointer)(s)) = regArgs.Ptrs[st.ireg]
				case abiStepFloatReg:
					offset := add(s, st.offset, "precomputed value offset")
					floatFromReg(&regArgs, st.freg, st.size, offset)
				case abiStepStack:
					panic("register-based return value has stack component")
				default:
					panic("unknown ABI part kind")
				}
			}
			ret[i] = Value{typ_: tv, ptr: s, flag: flagIndir | flag(tv.Kind())}
		}
	}

	return ret
}

// callReflect is the call implementation used by a function
// returned by MakeFunc. In many ways it is the opposite of the
// method Value.call above. The method above converts a call using Values
// into a call of a function with a concrete argument frame, while
// callReflect converts a call of a function with a concrete argument
// frame into a call using Values.
// It is in this file so that it can be next to the call method above.
// The remainder of the MakeFunc implementation is in makefunc.go.
//
// NOTE: This function must be marked as a "wrapper" in the generated code,
// so that the linker can make it work correctly for panic and recover.
// The gc compilers know to do that for the name "reflect.callReflect".
//
// ctxt is the "closure" generated by MakeFunc.
// frame is a pointer to the arguments to that closure on the stack.
// retValid points to a boolean which should be set when the results
// section of frame is set.
//
// regs contains the argument values passed in registers and will contain
// the values returned from ctxt.fn in registers.
func callReflect(ctxt *makeFuncImpl, frame unsafe.Pointer, retValid *bool, regs *abi.RegArgs) {
	if callGC {
		// Call GC upon entry during testing.
		// Getting our stack scanned here is the biggest hazard, because
		// our caller (makeFuncStub) could have failed to place the last
		// pointer to a value in regs' pointer space, in which case it
		// won't be visible to the GC.
		runtime.GC()
	}
	ftyp := ctxt.ftyp
	f := ctxt.fn

	_, _, abid := funcLayout(ftyp, nil)

	// Copy arguments into Values.
	ptr := frame
	in := make([]Value, 0, int(ftyp.InCount))
	for i, typ := range ftyp.InSlice() {
		if typ.Size() == 0 {
			in = append(in, Zero(toRType(typ)))
			continue
		}
		v := Value{typ_: typ, ptr: nil, flag: flag(typ.Kind())}
		steps := abid.call.stepsForValue(i)
		if st := steps[0]; st.kind == abiStepStack {
			if !typ.IsDirectIface() {
				// value cannot be inlined in interface data.
				// Must make a copy, because f might keep a reference to it,
				// and we cannot let f keep a reference to the stack frame
				// after this function returns, not even a read-only reference.
				v.ptr = unsafe_New(typ)
				if typ.Size() > 0 {
					typedmemmove(typ, v.dataPtr(), add(ptr, st.stkOff, "typ.size > 0"))
				}
				v.flag |= flagIndir
			} else {
				v.ptr = *(*unsafe.Pointer)(add(ptr, st.stkOff, "1-ptr"))
			}
		} else {
			if !typ.IsDirectIface() {
				// All that's left is values passed in registers that we need to
				// create space for the values.
				v.flag |= flagIndir
				v.ptr = unsafe_New(typ)
				for _, st := range steps {
					switch st.kind {
					case abiStepIntReg:
						offset := add(v.dataPtr(), st.offset, "precomputed value offset")
						intFromReg(regs, st.ireg, st.size, offset)
					case abiStepPointer:
						s := add(v.dataPtr(), st.offset, "precomputed value offset")
						*((*unsafe.Pointer)(s)) = regs.Ptrs[st.ireg]
					case abiStepFloatReg:
						offset := add(v.dataPtr(), st.offset, "precomputed value offset")
						floatFromReg(regs, st.freg, st.size, offset)
					case abiStepStack:
						panic("register-based return value has stack component")
					default:
						panic("unknown ABI part kind")
					}
				}
			} else {
				// Pointer-valued data gets put directly
				// into v.ptr.
				if steps[0].kind != abiStepPointer {
					print("kind=", steps[0].kind, ", type=", stringFor(typ), "\n")
					panic("mismatch between ABI description and types")
				}
				v.ptr = regs.Ptrs[steps[0].ireg]
			}
		}
		in = append(in, v)
	}

	// gd Phase G.2.1: strip synthesised outBuf args before handing
	// the in[] slice to the user's MakeFunc callback. The user
	// wrote a function matching the source-level signature, so
	// they see N user args only; the K trailing outBufs stay in
	// the frame untouched.
	if nOut := ftyp.NumOutBufs(); nOut > 0 && len(in) >= nOut {
		in = in[:len(in)-nOut]
	}

	// Call underlying function.
	out := f(in)
	numOut := ftyp.NumOut()
	if len(out) != numOut {
		panic("reflect: wrong return count from function created by MakeFunc")
	}

	// Copy results back into argument frame and register space.
	if numOut > 0 {
		for i, typ := range ftyp.OutSlice() {
			v := out[i]
			if v.typ() == nil {
				panic("reflect: function created by MakeFunc using " + funcName(f) +
					" returned zero Value")
			}
			if v.flag&flagRO != 0 {
				panic("reflect: function created by MakeFunc using " + funcName(f) +
					" returned value obtained from unexported field")
			}
			if typ.Size() == 0 {
				continue
			}

			// Convert v to type typ if v is assignable to a variable
			// of type t in the language spec.
			// See issue 28761.
			//
			//
			// TODO(mknyszek): In the switch to the register ABI we lost
			// the scratch space here for the register cases (and
			// temporarily for all the cases).
			//
			// If/when this happens, take note of the following:
			//
			// We must clear the destination before calling assignTo,
			// in case assignTo writes (with memory barriers) to the
			// target location used as scratch space. See issue 39541.
			v = v.assignTo("reflect.MakeFunc", typ, nil)
		stepsLoop:
			for _, st := range abid.ret.stepsForValue(i) {
				switch st.kind {
				case abiStepStack:
					// Copy values to the "stack."
					addr := add(ptr, st.stkOff, "precomputed stack arg offset")
					// Do not use write barriers. The stack space used
					// for this call is not adequately zeroed, and we
					// are careful to keep the arguments alive until we
					// return to makeFuncStub's caller.
					if v.flag&flagIndir != 0 {
						memmove(addr, v.dataPtr(), st.size)
					} else {
						// This case must be a pointer type.
						*(*uintptr)(addr) = uintptr(v.dataPtr())
					}
					// There's only one step for a stack-allocated value.
					break stepsLoop
				case abiStepIntReg, abiStepPointer:
					// Copy values to "integer registers."
					if v.flag&flagIndir != 0 {
						offset := add(v.dataPtr(), st.offset, "precomputed value offset")
						intToReg(regs, st.ireg, st.size, offset)
					} else {
						// Only populate the Ints space on the return path.
						// This is safe because out is kept alive until the
						// end of this function, and the return path through
						// makeFuncStub has no preemption, so these pointers
						// are always visible to the GC.
						regs.Ints[st.ireg] = uintptr(v.dataPtr())
					}
				case abiStepFloatReg:
					// Copy values to "float registers."
					if v.flag&flagIndir == 0 {
						panic("attempted to copy pointer to FP register")
					}
					offset := add(v.dataPtr(), st.offset, "precomputed value offset")
					floatToReg(regs, st.freg, st.size, offset)
				default:
					panic("unknown ABI part kind")
				}
			}
		}
	}

	// Announce that the return values are valid.
	// After this point the runtime can depend on the return values being valid.
	*retValid = true

	// We have to make sure that the out slice lives at least until
	// the runtime knows the return values are valid. Otherwise, the
	// return values might not be scanned by anyone during a GC.
	// (out would be dead, and the return slots not yet alive.)
	runtime.KeepAlive(out)

	// runtime.getArgInfo expects to be able to find ctxt on the
	// stack when it finds our caller, makeFuncStub. Make sure it
	// doesn't get garbage collected.
	runtime.KeepAlive(ctxt)
}

// methodReceiver returns information about the receiver
// described by v. The Value v may or may not have the
// flagMethod bit set, so the kind cached in v.flag should
// not be used.
// The return value rcvrtype gives the method's actual receiver type.
// The return value t gives the method type signature (without the receiver).
// The return value fn is a pointer to the method code.
func methodReceiver(op string, v Value, methodIndex int) (rcvrtype *abi.Type, t *funcType, fn unsafe.Pointer) {
	i := methodIndex
	if v.typ().Kind() == abi.Interface {
		tt := (*interfaceType)(unsafe.Pointer(v.typ()))
		if uint(i) >= uint(len(tt.Methods)) {
			panic("reflect: internal error: invalid method index")
		}
		m := &tt.Methods[i]
		if !tt.nameOff(m.Name).IsExported() {
			panic("reflect: " + op + " of unexported method")
		}
		iface := (*nonEmptyInterface)(v.dataPtr())
		if iface.itab == nil {
			panic("reflect: " + op + " of method on nil interface value")
		}
		rcvrtype = iface.itab.Type
		fn = unsafe.Pointer(&unsafe.Slice(&iface.itab.Fun[0], i+1)[i])
		t = (*funcType)(unsafe.Pointer(tt.typeOff(m.Typ)))
	} else {
		rcvrtype = v.typ()
		ms := v.typ().ExportedMethods()
		if uint(i) >= uint(len(ms)) {
			panic("reflect: internal error: invalid method index")
		}
		m := ms[i]
		if !nameOffFor(v.typ(), m.Name).IsExported() {
			panic("reflect: " + op + " of unexported method")
		}
		ifn := textOffFor(v.typ(), m.Ifn)
		fn = unsafe.Pointer(&ifn)
		t = (*funcType)(unsafe.Pointer(typeOffFor(v.typ(), m.Mtyp)))
	}
	return
}

// v is a method receiver. Store at p the word which is used to
// encode that receiver at the start of the argument list.
// Reflect uses the "interface" calling convention for
// methods, which always uses one word to record the receiver.
func storeRcvr(v Value, p unsafe.Pointer) {
	t := v.typ()
	if t.Kind() == abi.Interface {
		// the interface data word becomes the receiver word
		iface := (*nonEmptyInterface)(v.dataPtr())
		if iface.itab != nil && iface.itab.Inline != 0 {
			// gd fat-iface: payload lives in the inline slot; the
			// receiver word is &iface.inline.
			*(*unsafe.Pointer)(p) = unsafe.Pointer(&iface.inline)
		} else {
			*(*unsafe.Pointer)(p) = iface.word
		}
	} else if v.flag&flagIndir != 0 && t.IsDirectIface() {
		*(*unsafe.Pointer)(p) = *(*unsafe.Pointer)(v.dataPtr())
	} else {
		*(*unsafe.Pointer)(p) = v.dataPtr()
	}
}

// align returns the result of rounding x up to a multiple of n.
// n must be a power of two.
func align(x, n uintptr) uintptr {
	return (x + n - 1) &^ (n - 1)
}

// callMethod is the call implementation used by a function returned
// by makeMethodValue (used by v.Method(i).Interface()).
// It is a streamlined version of the usual reflect call: the caller has
// already laid out the argument frame for us, so we don't have
// to deal with individual Values for each argument.
// It is in this file so that it can be next to the two similar functions above.
// The remainder of the makeMethodValue implementation is in makefunc.go.
//
// NOTE: This function must be marked as a "wrapper" in the generated code,
// so that the linker can make it work correctly for panic and recover.
// The gc compilers know to do that for the name "reflect.callMethod".
//
// ctxt is the "closure" generated by makeMethodValue.
// frame is a pointer to the arguments to that closure on the stack.
// retValid points to a boolean which should be set when the results
// section of frame is set.
//
// regs contains the argument values passed in registers and will contain
// the values returned from ctxt.fn in registers.
func callMethod(ctxt *methodValue, frame unsafe.Pointer, retValid *bool, regs *abi.RegArgs) {
	rcvr := ctxt.rcvr
	rcvrType, valueFuncType, methodFn := methodReceiver("call", rcvr, ctxt.method)

	// There are two ABIs at play here.
	//
	// methodValueCall was invoked with the ABI assuming there was no
	// receiver ("value ABI") and that's what frame and regs are holding.
	//
	// Meanwhile, we need to actually call the method with a receiver, which
	// has its own ABI ("method ABI"). Everything that follows is a translation
	// between the two.
	_, _, valueABI := funcLayout(valueFuncType, nil)
	valueFrame, valueRegs := frame, regs
	methodFrameType, methodFramePool, methodABI := funcLayout(valueFuncType, rcvrType)

	// Make a new frame that is one word bigger so we can store the receiver.
	// This space is used for both arguments and return values.
	methodFrame := methodFramePool.Get().(unsafe.Pointer)
	var methodRegs abi.RegArgs

	// Deal with the receiver. It's guaranteed to only be one word in size.
	switch st := methodABI.call.steps[0]; st.kind {
	case abiStepStack:
		// Only copy the receiver to the stack if the ABI says so.
		// Otherwise, it'll be in a register already.
		storeRcvr(rcvr, methodFrame)
	case abiStepPointer:
		// Put the receiver in a register.
		storeRcvr(rcvr, unsafe.Pointer(&methodRegs.Ptrs[st.ireg]))
		fallthrough
	case abiStepIntReg:
		storeRcvr(rcvr, unsafe.Pointer(&methodRegs.Ints[st.ireg]))
	case abiStepFloatReg:
		storeRcvr(rcvr, unsafe.Pointer(&methodRegs.Floats[st.freg]))
	default:
		panic("unknown ABI parameter kind")
	}

	// Translate the rest of the arguments.
	for i, t := range valueFuncType.InSlice() {
		valueSteps := valueABI.call.stepsForValue(i)
		methodSteps := methodABI.call.stepsForValue(i + 1)

		// Zero-sized types are trivial: nothing to do.
		if len(valueSteps) == 0 {
			if len(methodSteps) != 0 {
				panic("method ABI and value ABI do not align")
			}
			continue
		}

		// There are four cases to handle in translating each
		// argument:
		// 1. Stack -> stack translation.
		// 2. Stack -> registers translation.
		// 3. Registers -> stack translation.
		// 4. Registers -> registers translation.

		// If the value ABI passes the value on the stack,
		// then the method ABI does too, because it has strictly
		// fewer arguments. Simply copy between the two.
		if vStep := valueSteps[0]; vStep.kind == abiStepStack {
			mStep := methodSteps[0]
			// Handle stack -> stack translation.
			if mStep.kind == abiStepStack {
				if vStep.size != mStep.size {
					panic("method ABI and value ABI do not align")
				}
				typedmemmove(t,
					add(methodFrame, mStep.stkOff, "precomputed stack offset"),
					add(valueFrame, vStep.stkOff, "precomputed stack offset"))
				continue
			}
			// Handle stack -> register translation.
			for _, mStep := range methodSteps {
				from := add(valueFrame, vStep.stkOff+mStep.offset, "precomputed stack offset")
				switch mStep.kind {
				case abiStepPointer:
					// Do the pointer copy directly so we get a write barrier.
					methodRegs.Ptrs[mStep.ireg] = *(*unsafe.Pointer)(from)
					fallthrough // We need to make sure this ends up in Ints, too.
				case abiStepIntReg:
					intToReg(&methodRegs, mStep.ireg, mStep.size, from)
				case abiStepFloatReg:
					floatToReg(&methodRegs, mStep.freg, mStep.size, from)
				default:
					panic("unexpected method step")
				}
			}
			continue
		}
		// Handle register -> stack translation.
		if mStep := methodSteps[0]; mStep.kind == abiStepStack {
			for _, vStep := range valueSteps {
				to := add(methodFrame, mStep.stkOff+vStep.offset, "precomputed stack offset")
				switch vStep.kind {
				case abiStepPointer:
					// Do the pointer copy directly so we get a write barrier.
					*(*unsafe.Pointer)(to) = valueRegs.Ptrs[vStep.ireg]
				case abiStepIntReg:
					intFromReg(valueRegs, vStep.ireg, vStep.size, to)
				case abiStepFloatReg:
					floatFromReg(valueRegs, vStep.freg, vStep.size, to)
				default:
					panic("unexpected value step")
				}
			}
			continue
		}
		// Handle register -> register translation.
		if len(valueSteps) != len(methodSteps) {
			// Because it's the same type for the value, and it's assigned
			// to registers both times, it should always take up the same
			// number of registers for each ABI.
			panic("method ABI and value ABI don't align")
		}
		for i, vStep := range valueSteps {
			mStep := methodSteps[i]
			if mStep.kind != vStep.kind {
				panic("method ABI and value ABI don't align")
			}
			switch vStep.kind {
			case abiStepPointer:
				// Copy this too, so we get a write barrier.
				methodRegs.Ptrs[mStep.ireg] = valueRegs.Ptrs[vStep.ireg]
				fallthrough
			case abiStepIntReg:
				methodRegs.Ints[mStep.ireg] = valueRegs.Ints[vStep.ireg]
			case abiStepFloatReg:
				methodRegs.Floats[mStep.freg] = valueRegs.Floats[vStep.freg]
			default:
				panic("unexpected value step")
			}
		}
	}

	methodFrameSize := methodFrameType.Size()
	// TODO(mknyszek): Remove this when we no longer have
	// caller reserved spill space.
	methodFrameSize = align(methodFrameSize, goarch.PtrSize)
	methodFrameSize += methodABI.spill

	// Mark pointers in registers for the return path.
	methodRegs.ReturnIsPtr = methodABI.outRegPtrs

	// Call.
	// Call copies the arguments from scratch to the stack, calls fn,
	// and then copies the results back into scratch.
	call(methodFrameType, methodFn, methodFrame, uint32(methodFrameType.Size()), uint32(methodABI.retOffset), uint32(methodFrameSize), &methodRegs)

	// Copy return values.
	//
	// This is somewhat simpler because both ABIs have an identical
	// return value ABI (the types are identical). As a result, register
	// results can simply be copied over. Stack-allocated values are laid
	// out the same, but are at different offsets from the start of the frame
	// Ignore any changes to args.
	// Avoid constructing out-of-bounds pointers if there are no return values.
	// because the arguments may be laid out differently.
	if valueRegs != nil {
		*valueRegs = methodRegs
	}
	if retSize := methodFrameType.Size() - methodABI.retOffset; retSize > 0 {
		valueRet := add(valueFrame, valueABI.retOffset, "valueFrame's size > retOffset")
		methodRet := add(methodFrame, methodABI.retOffset, "methodFrame's size > retOffset")
		// This copies to the stack. Write barriers are not needed.
		memmove(valueRet, methodRet, retSize)
	}

	// Tell the runtime it can now depend on the return values
	// being properly initialized.
	*retValid = true

	// Clear the scratch space and put it back in the pool.
	// This must happen after the statement above, so that the return
	// values will always be scanned by someone.
	typedmemclr(methodFrameType, methodFrame)
	methodFramePool.Put(methodFrame)

	// See the comment in callReflect.
	runtime.KeepAlive(ctxt)

	// Keep valueRegs alive because it may hold live pointer results.
	// The caller (methodValueCall) has it as a stack object, which is only
	// scanned when there is a reference to it.
	runtime.KeepAlive(valueRegs)
}

// funcName returns the name of f, for use in error messages.
func funcName(f func([]Value) []Value) string {
	pc := *(*uintptr)(unsafe.Pointer(&f))
	rf := runtime.FuncForPC(pc)
	if rf != nil {
		return rf.Name()
	}
	return "closure"
}

// Cap returns v's capacity.
// It panics if v's Kind is not [Array], [Chan], [Slice] or pointer to [Array].
func (v Value) Cap() int {
	// Fast path: spread slice — the common case after fat-iface, since
	// every reflect.ValueOf(slice) lands here. Non-spread slices and
	// all other kinds fall through to capNonSlice. Splitting keeps Cap
	// inlinable.
	//
	// Spread layout: Data=v.ptr, Len=inline.lo, Cap=inline.hi.
	// abi.NoEscape on &v.inline keeps v from leaking into escape
	// analysis (see Bytes for the cascading rationale).
	if v.flag&(flagKindMask|flagSpread) == flag(Slice)|flagSpread {
		return (*[2]int)(abi.NoEscape(unsafe.Pointer(&v.inline)))[1]
	}
	return v.capNonSlice()
}

func (v Value) capNonSlice() int {
	k := v.kind()
	switch k {
	case Slice:
		// Non-spread slice: heap header at v.ptr.
		return (*unsafeheader.Slice)(v.ptr).Cap
	case Array:
		return v.typ().Len()
	case Chan:
		return chancap(v.pointer())
	case Ptr:
		if v.typ().Elem().Kind() == abi.Array {
			return v.typ().Elem().Len()
		}
		panic("reflect: call of reflect.Value.Cap on ptr to non-array Value")
	}
	panic(&ValueError{"reflect.Value.Cap", v.kind()})
}

// Close closes the channel v.
// It panics if v's Kind is not [Chan] or
// v is a receive-only channel.
func (v Value) Close() {
	v.mustBe(Chan)
	v.mustBeExported()
	tt := (*chanType)(unsafe.Pointer(v.typ()))
	if ChanDir(tt.Dir)&SendDir == 0 {
		panic("reflect: close of receive-only channel")
	}

	chanclose(v.pointer())
}

// CanComplex reports whether [Value.Complex] can be used without panicking.
func (v Value) CanComplex() bool {
	switch v.kind() {
	case Complex64, Complex128:
		return true
	default:
		return false
	}
}

// Complex returns v's underlying value, as a complex128.
// It panics if v's Kind is not [Complex64] or [Complex128]
func (v Value) Complex() complex128 {
	k := v.kind()
	var p = v.ptr
	if v.flag&flagInline != 0 {
		p = unsafe.Pointer(&v.inline)
	}
	switch k {
	case Complex64:
		return complex128(*(*complex64)(p))
	case Complex128:
		return *(*complex128)(p)
	}
	panic(&ValueError{"reflect.Value.Complex", v.kind()})
}

// Elem returns the value that the interface v contains
// or that the pointer v points to.
// It panics if v's Kind is not [Interface] or [Pointer].
// It returns the zero Value if v is nil.
func (v Value) Elem() Value {
	k := v.kind()
	switch k {
	case Interface:
		x := unpackEface(packIfaceValueIntoEmptyIface(v))
		if x.flag != 0 {
			x.flag |= v.flag.ro()
		}
		return x
	case Pointer:
		ptr := v.dataPtr()
		if v.flag&flagIndir != 0 {
			if !v.typ().IsDirectIface() {
				// This is a pointer to a not-in-heap object. ptr points to a uintptr
				// in the heap. That uintptr is the address of a not-in-heap object.
				// In general, pointers to not-in-heap objects can be total junk.
				// But Elem() is asking to dereference it, so the user has asserted
				// that at least it is a valid pointer (not just an integer stored in
				// a pointer slot). So let's check, to make sure that it isn't a pointer
				// that the runtime will crash on if it sees it during GC or write barriers.
				// Since it is a not-in-heap pointer, all pointers to the heap are
				// forbidden! That makes the test pretty easy.
				// See issue 48399.
				if !verifyNotInHeapPtr(*(*uintptr)(ptr)) {
					panic("reflect: reflect.Value.Elem on an invalid notinheap pointer")
				}
			}
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
	panic(&ValueError{"reflect.Value.Elem", v.kind()})
}

// Field returns the i'th field of the struct v.
// It panics if v's Kind is not [Struct] or i is out of range.
func (v Value) Field(i int) Value {
	if v.kind() != Struct {
		panic(&ValueError{"reflect.Value.Field", v.kind()})
	}
	tt := (*structType)(unsafe.Pointer(v.typ()))
	if uint(i) >= uint(len(tt.Fields)) {
		panic("reflect: Field index out of range")
	}
	field := &tt.Fields[i]
	typ := field.Typ

	// Inherit permission bits from v, but clear flagEmbedRO.
	fl := v.flag&(flagStickyRO|flagIndir|flagAddr) | flag(typ.Kind())
	// Using an unexported field forces flagRO.
	if !field.Name.IsExported() {
		if field.Embedded() {
			fl |= flagEmbedRO
		} else {
			fl |= flagStickyRO
		}
	}
	if fl&flagIndir == 0 && typ.Size() == 0 {
		// Special case for picking a field out of a direct struct.
		// A direct struct must have a pointer field and possibly a
		// bunch of zero-sized fields. We must return the zero-sized
		// fields indirectly, as only ptr-shaped things can be direct.
		// See issue 74935.
		// We use &zeroVal[0] instead of v.ptr as it doesn't matter and
		// we can avoid pinning a possibly now-unused object.
		// Don't use nil, see issue 77779.
		return Value{typ_: typ, ptr: unsafe.Pointer(&zeroVal[0]), flag: fl | flagIndir}
	}

	// Either flagIndir is set and v.ptr points at struct,
	// or flagIndir is not set and v.ptr is the actual struct data.
	// In the former case, we want v.ptr + offset.
	// In the latter case, we must have field.offset = 0,
	// so v.ptr + field.offset is still the correct address.
	if v.flag&flagInline != 0 {
		// Parent is inline; propagate inline storage to the sub-Value
		// instead of materializing (which would allocate per Field call).
		var sub Value
		sub.typ_ = typ
		typedmemmove(typ, unsafe.Pointer(&sub.inline),
			add(unsafe.Pointer(&v.inline), field.Offset, "field within inline parent"))
		sub.flag = fl | flagInline
		return sub
	}
	ptr := add(v.ptr, field.Offset, "same as non-reflect &v.field")
	return Value{typ_: typ, ptr: ptr, flag: fl}
}

// FieldByIndex returns the nested field corresponding to index.
// It panics if evaluation requires stepping through a nil
// pointer or a field that is not a struct.
func (v Value) FieldByIndex(index []int) Value {
	if len(index) == 1 {
		return v.Field(index[0])
	}
	v.mustBe(Struct)
	for i, x := range index {
		if i > 0 {
			if v.Kind() == Pointer && v.typ().Elem().Kind() == abi.Struct {
				if v.IsNil() {
					panic("reflect: indirection through nil pointer to embedded struct")
				}
				v = v.Elem()
			}
		}
		v = v.Field(x)
	}
	return v
}

// FieldByIndexErr returns the nested field corresponding to index.
// It returns an error if evaluation requires stepping through a nil
// pointer, but panics if it must step through a field that
// is not a struct.
func (v Value) FieldByIndexErr(index []int) (Value, error) {
	if len(index) == 1 {
		return v.Field(index[0]), nil
	}
	v.mustBe(Struct)
	for i, x := range index {
		if i > 0 {
			if v.Kind() == Ptr && v.typ().Elem().Kind() == abi.Struct {
				if v.IsNil() {
					return Value{}, errors.New("reflect: indirection through nil pointer to embedded struct field " + nameFor(v.typ().Elem()))
				}
				v = v.Elem()
			}
		}
		v = v.Field(x)
	}
	return v, nil
}

// FieldByName returns the struct field with the given name.
// It returns the zero Value if no field was found.
// It panics if v's Kind is not [Struct].
func (v Value) FieldByName(name string) Value {
	v.mustBe(Struct)
	if f, ok := toRType(v.typ()).FieldByName(name); ok {
		return v.FieldByIndex(f.Index)
	}
	return Value{}
}

// FieldByNameFunc returns the struct field with a name
// that satisfies the match function.
// It panics if v's Kind is not [Struct].
// It returns the zero Value if no field was found.
func (v Value) FieldByNameFunc(match func(string) bool) Value {
	if f, ok := toRType(v.typ()).FieldByNameFunc(match); ok {
		return v.FieldByIndex(f.Index)
	}
	return Value{}
}

// CanFloat reports whether [Value.Float] can be used without panicking.
func (v Value) CanFloat() bool {
	switch v.kind() {
	case Float32, Float64:
		return true
	default:
		return false
	}
}

// Float returns v's underlying value, as a float64.
// It panics if v's Kind is not [Float32] or [Float64]
func (v Value) Float() float64 {
	var ptr = v.ptr
	if v.flag&flagInline != 0 {
		ptr = unsafe.Pointer(&v.inline)
	}
	k := v.kind()
	switch k {
	case Float32:
		return float64(*(*float32)(ptr))
	case Float64:
		return *(*float64)(ptr)
	}
	panic(&ValueError{"reflect.Value.Float", v.kind()})
}

var uint8Type = rtypeOf(uint8(0))

// Index returns v's i'th element.
// It panics if v's Kind is not [Array], [Slice], or [String] or i is out of range.
func (v Value) Index(i int) Value {
	switch v.kind() {
	case Array:
		tt := (*arrayType)(unsafe.Pointer(v.typ()))
		if uint(i) >= uint(tt.Len) {
			panic("reflect: array index out of range")
		}
		typ := tt.Elem
		offset := uintptr(i) * typ.Size()

		// Either flagIndir is set and v.ptr points at array,
		// or flagIndir is not set and v.ptr is the actual array data.
		// In the former case, we want v.ptr + offset.
		// In the latter case, we must be doing Index(0), so offset = 0,
		// so v.ptr + offset is still the correct address.
		if v.flag&flagInline != 0 {
			// Parent array lives in v.inline; copy the element into the
			// sub-Value's inline slot to avoid a heap alloc per Index.
			fl := v.flag&flagRO | flag(typ.Kind()) | flagIndir | flagInline
			var sub Value
			sub.typ_ = typ
			typedmemmove(typ, unsafe.Pointer(&sub.inline),
				add(unsafe.Pointer(&v.inline), offset, "element within inline array"))
			sub.flag = fl
			return sub
		}
		val := add(v.ptr, offset, "same as &v[i], i < tt.len")
		fl := v.flag&(flagIndir|flagAddr) | v.flag.ro() | flag(typ.Kind()) // bits same as overall array
		return Value{typ_: typ, ptr: val, flag: fl}

	case Slice:
		// Element flag same as Elem of Pointer.
		// Addressable, indirect, possibly read-only.
		s := (*unsafeheader.Slice)(v.dataPtr())
		if uint(i) >= uint(s.Len) {
			panic("reflect: slice index out of range")
		}
		tt := (*sliceType)(unsafe.Pointer(v.typ()))
		typ := tt.Elem
		val := arrayAt(s.Data, i, typ.Size(), "i < s.Len")
		fl := flagAddr | flagIndir | v.flag.ro() | flag(typ.Kind())
		return Value{typ_: typ, ptr: val, flag: fl}

	case String:
		// gd small-string optimization: decode logical length from the
		// tag nibble. For inline-rep strings the bytes live inside the
		// header itself; for flagSpread values the header lives in
		// v.inline on this method's stack frame, so a byte pointer
		// into it would dangle after Index returns. Copy the byte out
		// into the returned Value's own inline slot (uint8 fits) and
		// hand back a self-contained flagInline sub-Value.
		s := (*unsafeheader.String)(v.dataPtr())
		if tag := uint(s.Len) >> abi.StringTagShift; tag != 0 {
			if uint(i) >= tag {
				panic("reflect: string index out of range")
			}
			b := *(*byte)(unsafe.Pointer(uintptr(v.dataPtr()) + goarch.PtrSize + uintptr(i)))
			var sub Value
			sub.typ_ = uint8Type
			*(*byte)(unsafe.Pointer(&sub.inline)) = b
			sub.flag = v.flag.ro() | flag(Uint8) | flagIndir | flagInline
			return sub
		}
		if uint(i) >= uint(s.Len) {
			panic("reflect: string index out of range")
		}
		p := arrayAt(s.Data, i, 1, "i < s.Len")
		fl := v.flag.ro() | flag(Uint8) | flagIndir
		return Value{typ_: uint8Type, ptr: p, flag: fl}
	}
	panic(&ValueError{"reflect.Value.Index", v.kind()})
}

// CanInt reports whether Int can be used without panicking.
func (v Value) CanInt() bool {
	switch v.kind() {
	case Int, Int8, Int16, Int32, Int64:
		return true
	default:
		return false
	}
}

// Int returns v's underlying value, as an int64.
// It panics if v's Kind is not [Int], [Int8], [Int16], [Int32], or [Int64].
func (v Value) Int() int64 {
	k := v.kind()
	var p = v.ptr
	if v.flag&flagInline != 0 {
		p = unsafe.Pointer(&v.inline)
	}
	switch k {
	case Int:
		return int64(*(*int)(p))
	case Int8:
		return int64(*(*int8)(p))
	case Int16:
		return int64(*(*int16)(p))
	case Int32:
		return int64(*(*int32)(p))
	case Int64:
		return *(*int64)(p)
	}
	panic(&ValueError{"reflect.Value.Int", v.kind()})
}

// CanInterface reports whether [Value.Interface] can be used without panicking.
func (v Value) CanInterface() bool {
	if v.flag == 0 {
		panic(&ValueError{"reflect.Value.CanInterface", Invalid})
	}
	return v.flag&flagRO == 0
}

// Interface returns v's current value as an interface{}.
// It is equivalent to:
//
//	var i interface{} = (v's underlying value)
//
// It panics if the Value was obtained by accessing
// unexported struct fields.
func (v Value) Interface() (i any) {
	return valueInterface(v, true)
}

func valueInterface(v Value, safe bool) any {
	if v.flag == 0 {
		panic(&ValueError{"reflect.Value.Interface", Invalid})
	}
	if safe && v.flag&flagRO != 0 {
		// Do not allow access to unexported values via Interface,
		// because they might be pointers that should not be
		// writable or methods or function that should not be callable.
		panic("reflect.Value.Interface: cannot return value obtained from unexported field or method")
	}
	if v.flag&flagMethod != 0 {
		v = makeMethodValue("Interface", v)
	}

	if v.kind() == Interface {
		// Special case: return the element inside the interface.
		return packIfaceValueIntoEmptyIface(v)
	}

	return packEface(v)
}

// TypeAssert is semantically equivalent to:
//
//	v2, ok := v.Interface().(T)
func TypeAssert[T any](v Value) (T, bool) {
	if v.flag == 0 {
		panic(&ValueError{"reflect.TypeAssert", Invalid})
	}
	if v.flag&flagRO != 0 {
		// Do not allow access to unexported values via TypeAssert,
		// because they might be pointers that should not be
		// writable or methods or function that should not be callable.
		panic("reflect.TypeAssert: cannot return value obtained from unexported field or method")
	}

	if v.flag&flagMethod != 0 {
		v = makeMethodValue("TypeAssert", v)
	}

	typ := abi.TypeFor[T]()

	// If v is an interface, return the element inside the interface.
	//
	// T is a concrete type and v is an interface. For example:
	//
	//	var v any = int(1)
	//	val := ValueOf(&v).Elem()
	//	TypeAssert[int](val) == val.Interface().(int)
	//
	// T is a interface and v is a non-nil interface value. For example:
	//
	//	var v any = &someError{}
	//	val := ValueOf(&v).Elem()
	//	TypeAssert[error](val) == val.Interface().(error)
	//
	// T is a interface and v is a nil interface value. For example:
	//
	//	var v error = nil
	//	val := ValueOf(&v).Elem()
	//	TypeAssert[error](val) == val.Interface().(error)
	if v.kind() == Interface {
		v, ok := packIfaceValueIntoEmptyIface(v).(T)
		return v, ok
	}

	// If T is an interface and v is a concrete type. For example:
	//
	//	TypeAssert[any](ValueOf(1)) == ValueOf(1).Interface().(any)
	//	TypeAssert[error](ValueOf(&someError{})) == ValueOf(&someError{}).Interface().(error)
	if typ.Kind() == abi.Interface {
		// To avoid allocating memory, in case the type assertion fails,
		// first do the type assertion with a nil Data pointer.
		iface := *(*any)(unsafe.Pointer(&abi.EmptyInterface{Type: v.typ(), Data: nil}))
		if out, ok := iface.(T); ok {
			// Now populate the payload properly: we already got the right
			// itab from the initial nil-payload assertion, so avoid a second
			// runtime type-assert by writing directly into the CommonInterface.
			t := v.typ()
			outC := (*abi.CommonInterface)(unsafe.Pointer(&out))
			if t.IsInlineIface() {
				// gd fat-iface: payload lives in Inline, Data stays nil.
				if v.flag&flagIndir != 0 {
					typedmemmove(t, unsafe.Pointer(&outC.Inline), v.dataPtr())
				} else {
					*(*unsafe.Pointer)(unsafe.Pointer(&outC.Inline)) = v.dataPtr()
				}
			} else if t.IsSpreadIface() {
				// gd Phase D: split the 24 B spread header across
				// the iface's Data slot (word 0) and Inline slot
				// (words 1+2). v.dataPtr() returns the start of the
				// header whether it lives inline in the Value
				// (flagSpread) or at a heap address (flagIndir only).
				src := v.dataPtr()
				outC.Data = *(*unsafe.Pointer)(src)
				*(*[16]byte)(unsafe.Pointer(&outC.Inline)) = *(*[16]byte)(unsafe.Pointer(uintptr(src) + goarch.PtrSize))
			} else {
				outC.Data = packEfaceData(v)
			}
			return out, true
		}
		var zero T
		return zero, false
	}

	// Both v and T must be concrete types.
	// The only way for an type-assertion to match is if the types are equal.
	if typ != v.typ() {
		var zero T
		return zero, false
	}
	if v.flag&flagIndir == 0 {
		return *(*T)(unsafe.Pointer(&v.ptr)), true
	}
	return *(*T)(v.dataPtr()), true
}

// packIfaceValueIntoEmptyIface converts an interface Value into an empty interface.
//
// Precondition: v.kind() == Interface
func packIfaceValueIntoEmptyIface(v Value) any {
	// Empty interface has one layout, all interfaces with
	// methods have a second layout.
	if v.NumMethod() == 0 {
		return *(*any)(v.dataPtr())
	}
	return *(*interface {
		M()
	})(v.dataPtr())
}

// InterfaceData returns a pair of unspecified uintptr values.
// It panics if v's Kind is not Interface.
//
// In earlier versions of Go, this function returned the interface's
// value as a uintptr pair. As of Go 1.4, the implementation of
// interface values precludes any defined use of InterfaceData.
//
// Deprecated: The memory representation of interface values is not
// compatible with InterfaceData.
func (v Value) InterfaceData() [2]uintptr {
	v.mustBe(Interface)
	// The compiler loses track as it converts to uintptr. Force escape.
	escapes(v.dataPtr())
	// We treat this as a read operation, so we allow
	// it even for unexported data, because the caller
	// has to import "unsafe" to turn it into something
	// that can be abused.
	// Interface value is always bigger than a word; assume flagIndir.
	return *(*[2]uintptr)(v.dataPtr())
}

// IsNil reports whether its argument v is nil. The argument must be
// a chan, func, interface, map, pointer, or slice value; if it is
// not, IsNil panics. Note that IsNil is not always equivalent to a
// regular comparison with nil in Go. For example, if v was created
// by calling [ValueOf] with an uninitialized interface variable i,
// i==nil will be true but v.IsNil will panic as v will be the zero
// Value.
func (v Value) IsNil() bool {
	switch v.kind() {
	case Chan, Func, Map, Pointer, UnsafePointer, Interface, Slice:
		if v.flag&flagMethod != 0 {
			return false // method values (only Func) are never nil
		}
		// First word location:
		//   - flagSpread set     → header lives in v's bytes; word 0 is v.ptr.
		//   - flagIndir unset    → direct iface; v.ptr is the value itself.
		//   - flagIndir set only → v.ptr → header on heap; deref it.
		if v.flag&(flagSpread|flagIndir) != flagIndir {
			return v.ptr == nil
		}
		return *(*unsafe.Pointer)(v.ptr) == nil
	}
	panic(&ValueError{"reflect.Value.IsNil", v.kind()})
}

// IsValid reports whether v represents a value.
// It returns false if v is the zero Value.
// If [Value.IsValid] returns false, all other methods except String panic.
// Most functions and methods never return an invalid Value.
// If one does, its documentation states the conditions explicitly.
func (v Value) IsValid() bool {
	return v.flag != 0
}

// IsZero reports whether v is the zero value for its type.
// It panics if the argument is invalid.
func (v Value) IsZero() bool {
	switch v.kind() {
	case Bool:
		return !v.Bool()
	case Int, Int8, Int16, Int32, Int64:
		return v.Int() == 0
	case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
		return v.Uint() == 0
	case Float32, Float64:
		return v.Float() == 0
	case Complex64, Complex128:
		return v.Complex() == 0
	case Array:
		if v.flag&flagIndir == 0 {
			return v.dataPtr() == nil
		}
		if v.dataPtr() == unsafe.Pointer(&zeroVal[0]) {
			return true
		}
		typ := (*abi.ArrayType)(unsafe.Pointer(v.typ()))
		// If the type is comparable, then compare directly with zero.
		if typ.Equal != nil && typ.Size() <= abi.ZeroValSize {
			// v.ptr doesn't escape, as Equal functions are compiler generated
			// and never escape. The escape analysis doesn't know, as it is a
			// function pointer call.
			return typ.Equal(abi.NoEscape(v.dataPtr()), unsafe.Pointer(&zeroVal[0]))
		}
		if typ.TFlag&abi.TFlagRegularMemory != 0 {
			// For some types where the zero value is a value where all bits of this type are 0
			// optimize it.
			return isZero(unsafe.Slice(((*byte)(v.dataPtr())), typ.Size()))
		}
		n := int(typ.Len)
		for i := 0; i < n; i++ {
			if !v.Index(i).IsZero() {
				return false
			}
		}
		return true
	case Chan, Func, Interface, Map, Pointer, Slice, UnsafePointer:
		return v.IsNil()
	case String:
		return v.Len() == 0
	case Struct:
		if v.flag&flagIndir == 0 {
			return v.dataPtr() == nil
		}
		if v.dataPtr() == unsafe.Pointer(&zeroVal[0]) {
			return true
		}
		typ := (*abi.StructType)(unsafe.Pointer(v.typ()))
		// If the type is comparable, then compare directly with zero.
		if typ.Equal != nil && typ.Size() <= abi.ZeroValSize {
			// See noescape justification above.
			return typ.Equal(abi.NoEscape(v.dataPtr()), unsafe.Pointer(&zeroVal[0]))
		}
		if typ.TFlag&abi.TFlagRegularMemory != 0 {
			// For some types where the zero value is a value where all bits of this type are 0
			// optimize it.
			return isZero(unsafe.Slice(((*byte)(v.dataPtr())), typ.Size()))
		}

		n := v.NumField()
		for i := 0; i < n; i++ {
			if !v.Field(i).IsZero() && v.Type().Field(i).Name != "_" {
				return false
			}
		}
		return true
	default:
		// This should never happen, but will act as a safeguard for later,
		// as a default value doesn't makes sense here.
		panic(&ValueError{"reflect.Value.IsZero", v.Kind()})
	}
}

// isZero For all zeros, performance is not as good as
// return bytealg.Count(b, byte(0)) == len(b)
func isZero(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	const n = 32
	// Align memory addresses to 8 bytes.
	for uintptr(unsafe.Pointer(&b[0]))%8 != 0 {
		if b[0] != 0 {
			return false
		}
		b = b[1:]
		if len(b) == 0 {
			return true
		}
	}
	for len(b)%8 != 0 {
		if b[len(b)-1] != 0 {
			return false
		}
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return true
	}
	w := unsafe.Slice((*uint64)(unsafe.Pointer(&b[0])), len(b)/8)
	for len(w)%n != 0 {
		if w[0] != 0 {
			return false
		}
		w = w[1:]
	}
	for len(w) >= n {
		if w[0] != 0 || w[1] != 0 || w[2] != 0 || w[3] != 0 ||
			w[4] != 0 || w[5] != 0 || w[6] != 0 || w[7] != 0 ||
			w[8] != 0 || w[9] != 0 || w[10] != 0 || w[11] != 0 ||
			w[12] != 0 || w[13] != 0 || w[14] != 0 || w[15] != 0 ||
			w[16] != 0 || w[17] != 0 || w[18] != 0 || w[19] != 0 ||
			w[20] != 0 || w[21] != 0 || w[22] != 0 || w[23] != 0 ||
			w[24] != 0 || w[25] != 0 || w[26] != 0 || w[27] != 0 ||
			w[28] != 0 || w[29] != 0 || w[30] != 0 || w[31] != 0 {
			return false
		}
		w = w[n:]
	}
	return true
}

// SetZero sets v to be the zero value of v's type.
// It panics if [Value.CanSet] returns false.
func (v Value) SetZero() {
	v.mustBeAssignable()
	switch v.kind() {
	case Bool:
		*(*bool)(v.dataPtr()) = false
	case Int:
		*(*int)(v.dataPtr()) = 0
	case Int8:
		*(*int8)(v.dataPtr()) = 0
	case Int16:
		*(*int16)(v.dataPtr()) = 0
	case Int32:
		*(*int32)(v.dataPtr()) = 0
	case Int64:
		*(*int64)(v.dataPtr()) = 0
	case Uint:
		*(*uint)(v.dataPtr()) = 0
	case Uint8:
		*(*uint8)(v.dataPtr()) = 0
	case Uint16:
		*(*uint16)(v.dataPtr()) = 0
	case Uint32:
		*(*uint32)(v.dataPtr()) = 0
	case Uint64:
		*(*uint64)(v.dataPtr()) = 0
	case Uintptr:
		*(*uintptr)(v.dataPtr()) = 0
	case Float32:
		*(*float32)(v.dataPtr()) = 0
	case Float64:
		*(*float64)(v.dataPtr()) = 0
	case Complex64:
		*(*complex64)(v.dataPtr()) = 0
	case Complex128:
		*(*complex128)(v.dataPtr()) = 0
	case String:
		*(*string)(v.dataPtr()) = ""
	case Slice:
		*(*unsafeheader.Slice)(v.dataPtr()) = unsafeheader.Slice{}
	case Interface:
		*(*abi.EmptyInterface)(v.dataPtr()) = abi.EmptyInterface{}
	case Chan, Func, Map, Pointer, UnsafePointer:
		*(*unsafe.Pointer)(v.dataPtr()) = nil
	case Array, Struct:
		typedmemclr(v.typ(), v.dataPtr())
	default:
		// This should never happen, but will act as a safeguard for later,
		// as a default value doesn't makes sense here.
		panic(&ValueError{"reflect.Value.SetZero", v.Kind()})
	}
}

// Kind returns v's Kind.
// If v is the zero Value ([Value.IsValid] returns false), Kind returns Invalid.
func (v Value) Kind() Kind {
	return v.kind()
}

// Len returns v's length.
// It panics if v's Kind is not [Array], [Chan], [Map], [Slice], [String], or pointer to [Array].
func (v Value) Len() int {
	// Fast path: spread slice — the common case after fat-iface, since
	// every reflect.ValueOf(slice) lands here. Non-spread slices and
	// all other kinds fall through to lenNonSlice. Splitting keeps Len
	// inlinable.
	//
	// Spread layout: Data=v.ptr, Len=inline.lo, Cap=inline.hi.
	// abi.NoEscape on &v.inline keeps v from leaking into escape
	// analysis (see Bytes for the cascading rationale).
	if v.flag&(flagKindMask|flagSpread) == flag(Slice)|flagSpread {
		return *(*int)(abi.NoEscape(unsafe.Pointer(&v.inline)))
	}
	return v.lenNonSlice()
}

func (v Value) lenNonSlice() int {
	switch k := v.kind(); k {
	case Slice:
		// Non-spread slice: heap header at v.ptr.
		return (*unsafeheader.Slice)(v.ptr).Len
	case Array:
		tt := (*arrayType)(unsafe.Pointer(v.typ()))
		return int(tt.Len)
	case Chan:
		return chanlen(v.pointer())
	case Map:
		return maplen(v.pointer())
	case String:
		// String is bigger than a word; assume flagIndir. gd: Len is
		// the raw word2 (tag nibble + length/bytes); decode the tag
		// to get the logical length.
		raw := uint((*unsafeheader.String)(v.dataPtr()).Len)
		if tag := raw >> abi.StringTagShift; tag != 0 {
			return int(tag)
		}
		return int(raw & abi.StringLenMask)
	case Ptr:
		if v.typ().Elem().Kind() == abi.Array {
			return v.typ().Elem().Len()
		}
		panic("reflect: call of reflect.Value.Len on ptr to non-array Value")
	}
	panic(&ValueError{"reflect.Value.Len", v.kind()})
}

// copyVal returns a Value containing the map key or value at ptr,
// allocating a new variable as needed.
func copyVal(typ *abi.Type, fl flag, ptr unsafe.Pointer) Value {
	if !typ.IsDirectIface() {
		// gd Phase D: strings and slices (spread types) ride inside
		// the Value struct itself — word 0 in v.ptr, words 1+2 in
		// v.inline. No heap alloc per map entry / call-return.
		if typ.IsSpreadIface() {
			var v Value
			v.typ_ = typ
			v.ptr = *(*unsafe.Pointer)(ptr)
			*(*[16]byte)(unsafe.Pointer(&v.inline)) = *(*[16]byte)(unsafe.Pointer(uintptr(ptr) + goarch.PtrSize))
			v.flag = fl | flagIndir | flagSpread
			return v
		}
		// Copy result so future changes to the map
		// won't change the underlying value.
		c := unsafe_New(typ)
		typedmemmove(typ, c, ptr)
		return Value{typ_: typ, ptr: c, flag: fl | flagIndir}
	}
	return Value{typ_: typ, ptr: *(*unsafe.Pointer)(ptr), flag: fl}
}

// Method returns a function value corresponding to v's i'th method.
// The arguments to a Call on the returned function should not include
// a receiver; the returned function will always use v as the receiver.
// Method panics if i is out of range or if v is a nil interface value.
//
// Calling this method will force the linker to retain all exported methods in all packages.
// This may make the executable binary larger but will not affect execution time.
func (v Value) Method(i int) Value {
	if v.typ() == nil {
		panic(&ValueError{"reflect.Value.Method", Invalid})
	}
	if v.flag&flagMethod != 0 || uint(i) >= uint(toRType(v.typ()).NumMethod()) {
		panic("reflect: Method index out of range")
	}
	if v.typ().Kind() == abi.Interface && v.IsNil() {
		panic("reflect: Method on nil interface value")
	}
	(&v).materialize() // method value holds a reference to receiver data
	fl := v.flag.ro() | (v.flag & flagIndir)
	fl |= flag(Func)
	fl |= flag(i)<<flagMethodShift | flagMethod
	return Value{typ_: v.typ(), ptr: v.ptr, flag: fl}
}

// NumMethod returns the number of methods in the value's method set.
//
// For a non-interface type, it returns the number of exported methods.
//
// For an interface type, it returns the number of exported and unexported methods.
func (v Value) NumMethod() int {
	if v.typ() == nil {
		panic(&ValueError{"reflect.Value.NumMethod", Invalid})
	}
	if v.flag&flagMethod != 0 {
		return 0
	}
	return toRType(v.typ()).NumMethod()
}

// MethodByName returns a function value corresponding to the method
// of v with the given name.
// The arguments to a Call on the returned function should not include
// a receiver; the returned function will always use v as the receiver.
// It returns the zero Value if no method was found.
//
// Calling this method will cause the linker to retain all methods with this name in all packages.
// If the linker can't determine the name, it will retain all exported methods.
// This may make the executable binary larger but will not affect execution time.
func (v Value) MethodByName(name string) Value {
	if v.typ() == nil {
		panic(&ValueError{"reflect.Value.MethodByName", Invalid})
	}
	if v.flag&flagMethod != 0 {
		return Value{}
	}
	m, ok := toRType(v.typ()).MethodByName(name)
	if !ok {
		return Value{}
	}
	return v.Method(m.Index)
}

// NumField returns the number of fields in the struct v.
// It panics if v's Kind is not [Struct].
func (v Value) NumField() int {
	v.mustBe(Struct)
	tt := (*structType)(unsafe.Pointer(v.typ()))
	return len(tt.Fields)
}

// OverflowComplex reports whether the complex128 x cannot be represented by v's type.
// It panics if v's Kind is not [Complex64] or [Complex128].
func (v Value) OverflowComplex(x complex128) bool {
	k := v.kind()
	switch k {
	case Complex64:
		return overflowFloat32(real(x)) || overflowFloat32(imag(x))
	case Complex128:
		return false
	}
	panic(&ValueError{"reflect.Value.OverflowComplex", v.kind()})
}

// OverflowFloat reports whether the float64 x cannot be represented by v's type.
// It panics if v's Kind is not [Float32] or [Float64].
func (v Value) OverflowFloat(x float64) bool {
	k := v.kind()
	switch k {
	case Float32:
		return overflowFloat32(x)
	case Float64:
		return false
	}
	panic(&ValueError{"reflect.Value.OverflowFloat", v.kind()})
}

func overflowFloat32(x float64) bool {
	if x < 0 {
		x = -x
	}
	return math.MaxFloat32 < x && x <= math.MaxFloat64
}

// OverflowInt reports whether the int64 x cannot be represented by v's type.
// It panics if v's Kind is not [Int], [Int8], [Int16], [Int32], or [Int64].
func (v Value) OverflowInt(x int64) bool {
	k := v.kind()
	switch k {
	case Int, Int8, Int16, Int32, Int64:
		bitSize := v.typ().Size() * 8
		trunc := (x << (64 - bitSize)) >> (64 - bitSize)
		return x != trunc
	}
	panic(&ValueError{"reflect.Value.OverflowInt", v.kind()})
}

// OverflowUint reports whether the uint64 x cannot be represented by v's type.
// It panics if v's Kind is not [Uint], [Uintptr], [Uint8], [Uint16], [Uint32], or [Uint64].
func (v Value) OverflowUint(x uint64) bool {
	k := v.kind()
	switch k {
	case Uint, Uintptr, Uint8, Uint16, Uint32, Uint64:
		bitSize := v.typ_.Size() * 8 // ok to use v.typ_ directly as Size doesn't escape
		trunc := (x << (64 - bitSize)) >> (64 - bitSize)
		return x != trunc
	}
	panic(&ValueError{"reflect.Value.OverflowUint", v.kind()})
}

//go:nocheckptr
// This prevents inlining Value.Pointer when -d=checkptr is enabled,
// which ensures cmd/compile can recognize unsafe.Pointer(v.Pointer())
// and make an exception.

// Pointer returns v's value as a uintptr.
// It panics if v's Kind is not [Chan], [Func], [Map], [Pointer], [Slice], [String], or [UnsafePointer].
//
// If v's Kind is [Func], the returned pointer is an underlying
// code pointer, but not necessarily enough to identify a
// single function uniquely. The only guarantee is that the
// result is zero if and only if v is a nil func Value.
//
// If v's Kind is [Slice], the returned pointer is to the first
// element of the slice. If the slice is nil the returned value
// is 0.  If the slice is empty but non-nil the return value is non-zero.
//
// If v's Kind is [String], the returned pointer is to the first
// element of the underlying bytes of string.
//
// It's preferred to use uintptr(Value.UnsafePointer()) to get the equivalent result.
func (v Value) Pointer() uintptr {
	// The compiler loses track as it converts to uintptr. Force escape.
	escapes(v.dataPtr())

	k := v.kind()
	switch k {
	case Pointer:
		if !v.typ().Pointers() {
			val := *(*uintptr)(v.dataPtr())
			// Since it is a not-in-heap pointer, all pointers to the heap are
			// forbidden! See comment in Value.Elem and issue #48399.
			if !verifyNotInHeapPtr(val) {
				panic("reflect: reflect.Value.Pointer on an invalid notinheap pointer")
			}
			return val
		}
		fallthrough
	case Chan, Map, UnsafePointer:
		return uintptr(v.pointer())
	case Func:
		if v.flag&flagMethod != 0 {
			// As the doc comment says, the returned pointer is an
			// underlying code pointer but not necessarily enough to
			// identify a single function uniquely. All method expressions
			// created via reflect have the same underlying code pointer,
			// so their Pointers are equal. The function used here must
			// match the one used in makeMethodValue.
			return methodValueCallCodePtr()
		}
		p := v.pointer()
		// Non-nil func value points at data block.
		// First word of data block is actual code.
		if p != nil {
			p = *(*unsafe.Pointer)(p)
		}
		return uintptr(p)
	case Slice:
		return uintptr((*unsafeheader.Slice)(v.dataPtr()).Data)
	case String:
		// gd: for heap-rep strings Data is the byte pointer. For
		// inline-rep the bytes live in the header itself — return
		// &s.Hash, which points at the first inline byte. The
		// lifetime matches the Value's flagIndir storage.
		sh := (*unsafeheader.String)(v.dataPtr())
		if sh.Data != nil {
			return uintptr(sh.Data)
		}
		return uintptr(v.dataPtr()) + goarch.PtrSize
	}
	panic(&ValueError{"reflect.Value.Pointer", v.kind()})
}

// Recv receives and returns a value from the channel v.
// It panics if v's Kind is not [Chan].
// The receive blocks until a value is ready.
// The boolean value ok is true if the value x corresponds to a send
// on the channel, false if it is a zero value received because the channel is closed.
func (v Value) Recv() (x Value, ok bool) {
	v.mustBe(Chan)
	v.mustBeExported()
	return v.recv(false)
}

// internal recv, possibly non-blocking (nb).
// v is known to be a channel.
func (v Value) recv(nb bool) (val Value, ok bool) {
	tt := (*chanType)(unsafe.Pointer(v.typ()))
	if ChanDir(tt.Dir)&RecvDir == 0 {
		panic("reflect: recv on send-only channel")
	}
	t := tt.Elem
	val = Value{typ_: t, ptr: nil, flag: flag(t.Kind())}
	var p unsafe.Pointer
	if !t.IsDirectIface() {
		p = unsafe_New(t)
		val.ptr = p
		val.flag |= flagIndir
	} else {
		p = unsafe.Pointer(&val.ptr)
	}
	selected, ok := chanrecv(v.pointer(), nb, p)
	if !selected {
		val = Value{}
	}
	return
}

// Send sends x on the channel v.
// It panics if v's kind is not [Chan] or if x's type is not the same type as v's element type.
// As in Go, x's value must be assignable to the channel's element type.
func (v Value) Send(x Value) {
	v.mustBe(Chan)
	v.mustBeExported()
	v.send(x, false)
}

// internal send, possibly non-blocking.
// v is known to be a channel.
func (v Value) send(x Value, nb bool) (selected bool) {
	tt := (*chanType)(unsafe.Pointer(v.typ()))
	if ChanDir(tt.Dir)&SendDir == 0 {
		panic("reflect: send on recv-only channel")
	}
	x.mustBeExported()
	x = x.assignTo("reflect.Value.Send", tt.Elem, nil)
	var p unsafe.Pointer
	if x.flag&flagIndir != 0 {
		p = (&x).dataPtr()
	} else {
		p = unsafe.Pointer(&x.ptr)
	}
	return chansend(v.pointer(), p, nb)
}

// Set assigns x to the value v.
// It panics if [Value.CanSet] returns false.
// As in Go, x's value must be assignable to v's type and
// must not be derived from an unexported field.
func (v Value) Set(x Value) {
	v.mustBeAssignable()
	x.mustBeExported() // do not let unexported x leak
	var target unsafe.Pointer
	if v.kind() == Interface {
		target = v.dataPtr()
	}
	x = x.assignTo("reflect.Set", v.typ(), target)
	if x.flag&flagIndir != 0 {
		if (&x).dataPtr() == unsafe.Pointer(&zeroVal[0]) {
			typedmemclr(v.typ(), v.dataPtr())
		} else {
			typedmemmove(v.typ(), v.dataPtr(), (&x).dataPtr())
		}
	} else {
		*(*unsafe.Pointer)(v.dataPtr()) = (&x).dataPtr()
	}
}

// SetBool sets v's underlying value.
// It panics if v's Kind is not [Bool] or if [Value.CanSet] returns false.
func (v Value) SetBool(x bool) {
	v.mustBeAssignable()
	v.mustBe(Bool)
	*(*bool)(v.dataPtr()) = x
}

// SetBytes sets v's underlying value.
// It panics if v's underlying value is not a slice of bytes
// or if [Value.CanSet] returns false.
func (v Value) SetBytes(x []byte) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	if toRType(v.typ()).Elem().Kind() != Uint8 { // TODO add Elem method, fix mustBe(Slice) to return slice.
		panic("reflect.Value.SetBytes of non-byte slice")
	}
	*(*[]byte)(v.dataPtr()) = x
}

// setRunes sets v's underlying value.
// It panics if v's underlying value is not a slice of runes (int32s)
// or if [Value.CanSet] returns false.
func (v Value) setRunes(x []rune) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	if v.typ().Elem().Kind() != abi.Int32 {
		panic("reflect.Value.setRunes of non-rune slice")
	}
	*(*[]rune)(v.dataPtr()) = x
}

// SetComplex sets v's underlying value to x.
// It panics if v's Kind is not [Complex64] or [Complex128],
// or if [Value.CanSet] returns false.
func (v Value) SetComplex(x complex128) {
	v.mustBeAssignable()
	switch k := v.kind(); k {
	default:
		panic(&ValueError{"reflect.Value.SetComplex", v.kind()})
	case Complex64:
		*(*complex64)(v.dataPtr()) = complex64(x)
	case Complex128:
		*(*complex128)(v.dataPtr()) = x
	}
}

// SetFloat sets v's underlying value to x.
// It panics if v's Kind is not [Float32] or [Float64],
// or if [Value.CanSet] returns false.
func (v Value) SetFloat(x float64) {
	v.mustBeAssignable()
	switch k := v.kind(); k {
	default:
		panic(&ValueError{"reflect.Value.SetFloat", v.kind()})
	case Float32:
		*(*float32)(v.dataPtr()) = float32(x)
	case Float64:
		*(*float64)(v.dataPtr()) = x
	}
}

// SetInt sets v's underlying value to x.
// It panics if v's Kind is not [Int], [Int8], [Int16], [Int32], or [Int64],
// or if [Value.CanSet] returns false.
func (v Value) SetInt(x int64) {
	v.mustBeAssignable()
	switch k := v.kind(); k {
	default:
		panic(&ValueError{"reflect.Value.SetInt", v.kind()})
	case Int:
		*(*int)(v.dataPtr()) = int(x)
	case Int8:
		*(*int8)(v.dataPtr()) = int8(x)
	case Int16:
		*(*int16)(v.dataPtr()) = int16(x)
	case Int32:
		*(*int32)(v.dataPtr()) = int32(x)
	case Int64:
		*(*int64)(v.dataPtr()) = x
	}
}

// SetLen sets v's length to n.
// It panics if v's Kind is not [Slice], or if n is negative or
// greater than the capacity of the slice,
// or if [Value.CanSet] returns false.
func (v Value) SetLen(n int) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	s := (*unsafeheader.Slice)(v.dataPtr())
	if uint(n) > uint(s.Cap) {
		panic("reflect: slice length out of range in SetLen")
	}
	s.Len = n
}

// SetCap sets v's capacity to n.
// It panics if v's Kind is not [Slice], or if n is smaller than the length or
// greater than the capacity of the slice,
// or if [Value.CanSet] returns false.
func (v Value) SetCap(n int) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	s := (*unsafeheader.Slice)(v.dataPtr())
	if n < s.Len || n > s.Cap {
		panic("reflect: slice capacity out of range in SetCap")
	}
	s.Cap = n
}

// SetUint sets v's underlying value to x.
// It panics if v's Kind is not [Uint], [Uintptr], [Uint8], [Uint16], [Uint32], or [Uint64],
// or if [Value.CanSet] returns false.
func (v Value) SetUint(x uint64) {
	v.mustBeAssignable()
	switch k := v.kind(); k {
	default:
		panic(&ValueError{"reflect.Value.SetUint", v.kind()})
	case Uint:
		*(*uint)(v.dataPtr()) = uint(x)
	case Uint8:
		*(*uint8)(v.dataPtr()) = uint8(x)
	case Uint16:
		*(*uint16)(v.dataPtr()) = uint16(x)
	case Uint32:
		*(*uint32)(v.dataPtr()) = uint32(x)
	case Uint64:
		*(*uint64)(v.dataPtr()) = x
	case Uintptr:
		*(*uintptr)(v.dataPtr()) = uintptr(x)
	}
}

// SetPointer sets the [unsafe.Pointer] value v to x.
// It panics if v's Kind is not [UnsafePointer]
// or if [Value.CanSet] returns false.
func (v Value) SetPointer(x unsafe.Pointer) {
	v.mustBeAssignable()
	v.mustBe(UnsafePointer)
	*(*unsafe.Pointer)(v.dataPtr()) = x
}

// SetString sets v's underlying value to x.
// It panics if v's Kind is not [String] or if [Value.CanSet] returns false.
func (v Value) SetString(x string) {
	v.mustBeAssignable()
	v.mustBe(String)
	*(*string)(v.dataPtr()) = x
}

// Slice returns v[i:j].
// It panics if v's Kind is not [Array], [Slice] or [String], or if v is an unaddressable array,
// or if the indexes are out of bounds.
func (v Value) Slice(i, j int) Value {
	var (
		cap  int
		typ  *sliceType
		base unsafe.Pointer
	)
	switch kind := v.kind(); kind {
	default:
		panic(&ValueError{"reflect.Value.Slice", v.kind()})

	case Array:
		if v.flag&flagAddr == 0 {
			panic("reflect.Value.Slice: slice of unaddressable array")
		}
		tt := (*arrayType)(unsafe.Pointer(v.typ()))
		cap = int(tt.Len)
		typ = (*sliceType)(unsafe.Pointer(tt.Slice))
		base = v.dataPtr()

	case Slice:
		typ = (*sliceType)(unsafe.Pointer(v.typ()))
		s := (*unsafeheader.Slice)(v.dataPtr())
		base = s.Data
		cap = s.Cap

	case String:
		// gd: decode inline vs heap, then produce a fresh string
		// header. For an inline source, slicing must not expose a
		// pointer into the source's header (it would dangle once the
		// source goes out of scope); build the result via
		// runtime.sliceinlinestring which returns a self-contained
		// inline value.
		s := (*unsafeheader.String)(v.dataPtr())
		raw := uint(s.Len)
		var logicalLen int
		if tag := raw >> abi.StringTagShift; tag != 0 {
			logicalLen = int(tag)
		} else {
			logicalLen = int(raw & abi.StringLenMask)
		}
		if i < 0 || j < i || j > logicalLen {
			panic("reflect.Value.Slice: string slice index out of bounds")
		}
		var t string
		if i < logicalLen {
			// Route through the compiler's OSLICESTR path by slicing
			// a string-typed copy. This picks up the rep-aware
			// stringSlice helper (heap: ptr+i; inline: fresh inline).
			src := *(*string)(v.dataPtr())
			t = src[i:j]
		}
		// Return a flagIndir Value pointing at t. Clear flagSpread /
		// flagInline: the result's 24 B header lives at &t, not inside
		// the Value struct.
		fl := v.flag &^ (flagSpread | flagInline)
		return Value{typ_: v.typ(), ptr: unsafe.Pointer(&t), flag: fl}
	}

	if i < 0 || j < i || j > cap {
		panic("reflect.Value.Slice: slice index out of bounds")
	}

	// Declare slice so that gc can see the base pointer in it.
	var x []unsafe.Pointer

	// Reinterpret as *unsafeheader.Slice to edit.
	s := (*unsafeheader.Slice)(unsafe.Pointer(&x))
	s.Len = j - i
	s.Cap = cap - i
	if cap-i > 0 {
		s.Data = arrayAt(base, i, typ.Elem.Size(), "i < cap")
	} else {
		// do not advance pointer, to avoid pointing beyond end of slice
		s.Data = base
	}

	fl := v.flag.ro() | flagIndir | flag(Slice)
	return Value{typ_: typ.Common(), ptr: unsafe.Pointer(&x), flag: fl}
}

// Slice3 is the 3-index form of the slice operation: it returns v[i:j:k].
// It panics if v's Kind is not [Array] or [Slice], or if v is an unaddressable array,
// or if the indexes are out of bounds.
func (v Value) Slice3(i, j, k int) Value {
	var (
		cap  int
		typ  *sliceType
		base unsafe.Pointer
	)
	switch kind := v.kind(); kind {
	default:
		panic(&ValueError{"reflect.Value.Slice3", v.kind()})

	case Array:
		if v.flag&flagAddr == 0 {
			panic("reflect.Value.Slice3: slice of unaddressable array")
		}
		tt := (*arrayType)(unsafe.Pointer(v.typ()))
		cap = int(tt.Len)
		typ = (*sliceType)(unsafe.Pointer(tt.Slice))
		base = v.dataPtr()

	case Slice:
		typ = (*sliceType)(unsafe.Pointer(v.typ()))
		s := (*unsafeheader.Slice)(v.dataPtr())
		base = s.Data
		cap = s.Cap
	}

	if i < 0 || j < i || k < j || k > cap {
		panic("reflect.Value.Slice3: slice index out of bounds")
	}

	// Declare slice so that the garbage collector
	// can see the base pointer in it.
	var x []unsafe.Pointer

	// Reinterpret as *unsafeheader.Slice to edit.
	s := (*unsafeheader.Slice)(unsafe.Pointer(&x))
	s.Len = j - i
	s.Cap = k - i
	if k-i > 0 {
		s.Data = arrayAt(base, i, typ.Elem.Size(), "i < k <= cap")
	} else {
		// do not advance pointer, to avoid pointing beyond end of slice
		s.Data = base
	}

	fl := v.flag.ro() | flagIndir | flag(Slice)
	return Value{typ_: typ.Common(), ptr: unsafe.Pointer(&x), flag: fl}
}

// String returns the string v's underlying value, as a string.
// String is a special case because of Go's String method convention.
// Unlike the other getters, it does not panic if v's Kind is not [String].
// Instead, it returns a string of the form "<T value>" where T is v's type.
// The fmt package treats Values specially. It does not call their String
// method implicitly but instead prints the concrete values they hold.
func (v Value) String() string {
	// Fast path: spread string — the common case after fat-iface, since
	// every reflect.ValueOf(string) lands here. Non-spread strings and
	// non-string kinds fall through to stringNonString. Splitting keeps
	// String inlinable.
	//
	// Spread layout: 3-word string header lives contiguously at &v.ptr
	// (word 0 = data ptr or inline-rep word0, words 1+2 = v.inline).
	// abi.NoEscape on &v.ptr keeps v from leaking into escape analysis
	// (see Bytes for the cascading rationale).
	if v.flag&(flagKindMask|flagSpread) == flag(String)|flagSpread {
		return *(*string)(abi.NoEscape(unsafe.Pointer(&v.ptr)))
	}
	return v.stringNonString()
}

func (v Value) stringNonString() string {
	switch v.kind() {
	case String:
		// Non-spread string: header lives at v.ptr (flagIndir).
		return *(*string)(v.dataPtr())
	case Invalid:
		return "<invalid Value>"
	}
	// If you call String on a reflect.Value of other type, it's better to
	// print something than to panic. Useful in debugging.
	return "<" + v.Type().String() + " Value>"
}

// TryRecv attempts to receive a value from the channel v but will not block.
// It panics if v's Kind is not [Chan].
// If the receive delivers a value, x is the transferred value and ok is true.
// If the receive cannot finish without blocking, x is the zero Value and ok is false.
// If the channel is closed, x is the zero value for the channel's element type and ok is false.
func (v Value) TryRecv() (x Value, ok bool) {
	v.mustBe(Chan)
	v.mustBeExported()
	return v.recv(true)
}

// TrySend attempts to send x on the channel v but will not block.
// It panics if v's Kind is not [Chan].
// It reports whether the value was sent.
// As in Go, x's value must be assignable to the channel's element type.
func (v Value) TrySend(x Value) bool {
	v.mustBe(Chan)
	v.mustBeExported()
	return v.send(x, true)
}

// Type returns v's type.
func (v Value) Type() Type {
	if v.flag != 0 && v.flag&flagMethod == 0 {
		return (*rtype)(abi.NoEscape(unsafe.Pointer(v.typ_))) // inline of toRType(v.typ()), for own inlining in inline test
	}
	return v.typeSlow()
}

//go:noinline
func (v Value) typeSlow() Type {
	return toRType(v.abiTypeSlow())
}

func (v Value) abiType() *abi.Type {
	if v.flag != 0 && v.flag&flagMethod == 0 {
		return v.typ()
	}
	return v.abiTypeSlow()
}

func (v Value) abiTypeSlow() *abi.Type {
	if v.flag == 0 {
		panic(&ValueError{"reflect.Value.Type", Invalid})
	}

	typ := v.typ()
	if v.flag&flagMethod == 0 {
		return v.typ()
	}

	// Method value.
	// v.typ describes the receiver, not the method type.
	i := int(v.flag) >> flagMethodShift
	if v.typ().Kind() == abi.Interface {
		// Method on interface.
		tt := (*interfaceType)(unsafe.Pointer(typ))
		if uint(i) >= uint(len(tt.Methods)) {
			panic("reflect: internal error: invalid method index")
		}
		m := &tt.Methods[i]
		return typeOffFor(typ, m.Typ)
	}
	// Method on concrete type.
	ms := typ.ExportedMethods()
	if uint(i) >= uint(len(ms)) {
		panic("reflect: internal error: invalid method index")
	}
	m := ms[i]
	return typeOffFor(typ, m.Mtyp)
}

// CanUint reports whether [Value.Uint] can be used without panicking.
func (v Value) CanUint() bool {
	switch v.kind() {
	case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
		return true
	default:
		return false
	}
}

// Uint returns v's underlying value, as a uint64.
// It panics if v's Kind is not [Uint], [Uintptr], [Uint8], [Uint16], [Uint32], or [Uint64].
func (v Value) Uint() uint64 {
	k := v.kind()
	var p = v.ptr
	if v.flag&flagInline != 0 {
		p = unsafe.Pointer(&v.inline)
	}
	switch k {
	case Uint:
		return uint64(*(*uint)(p))
	case Uint8:
		return uint64(*(*uint8)(p))
	case Uint16:
		return uint64(*(*uint16)(p))
	case Uint32:
		return uint64(*(*uint32)(p))
	case Uint64:
		return *(*uint64)(p)
	case Uintptr:
		return uint64(*(*uintptr)(p))
	}
	panic(&ValueError{"reflect.Value.Uint", v.kind()})
}

//go:nocheckptr
// This prevents inlining Value.UnsafeAddr when -d=checkptr is enabled,
// which ensures cmd/compile can recognize unsafe.Pointer(v.UnsafeAddr())
// and make an exception.

// UnsafeAddr returns a pointer to v's data, as a uintptr.
// It panics if v is not addressable.
//
// It's preferred to use uintptr(Value.Addr().UnsafePointer()) to get the equivalent result.
func (v Value) UnsafeAddr() uintptr {
	if v.typ() == nil {
		panic(&ValueError{"reflect.Value.UnsafeAddr", Invalid})
	}
	if v.flag&flagAddr == 0 {
		panic("reflect.Value.UnsafeAddr of unaddressable value")
	}
	var p = v.ptr
	if v.flag&flagInline != 0 {
		p = unsafe.Pointer(&v.inline)
	}
	escapes(p) // The compiler loses track as it converts to uintptr. Force escape.
	return uintptr(p)
}

// UnsafePointer returns v's value as a [unsafe.Pointer].
// It panics if v's Kind is not [Chan], [Func], [Map], [Pointer], [Slice], [String] or [UnsafePointer].
//
// If v's Kind is [Func], the returned pointer is an underlying
// code pointer, but not necessarily enough to identify a
// single function uniquely. The only guarantee is that the
// result is zero if and only if v is a nil func Value.
//
// If v's Kind is [Slice], the returned pointer is to the first
// element of the slice. If the slice is nil the returned value
// is nil.  If the slice is empty but non-nil the return value is non-nil.
//
// If v's Kind is [String], the returned pointer is to the first
// element of the underlying bytes of string.
func (v Value) UnsafePointer() unsafe.Pointer {
	k := v.kind()
	switch k {
	case Pointer:
		if !v.typ().Pointers() {
			// Since it is a not-in-heap pointer, all pointers to the heap are
			// forbidden! See comment in Value.Elem and issue #48399.
			if !verifyNotInHeapPtr(*(*uintptr)(v.dataPtr())) {
				panic("reflect: reflect.Value.UnsafePointer on an invalid notinheap pointer")
			}
			return *(*unsafe.Pointer)(v.dataPtr())
		}
		fallthrough
	case Chan, Map, UnsafePointer:
		return v.pointer()
	case Func:
		if v.flag&flagMethod != 0 {
			// As the doc comment says, the returned pointer is an
			// underlying code pointer but not necessarily enough to
			// identify a single function uniquely. All method expressions
			// created via reflect have the same underlying code pointer,
			// so their Pointers are equal. The function used here must
			// match the one used in makeMethodValue.
			code := methodValueCallCodePtr()
			return *(*unsafe.Pointer)(unsafe.Pointer(&code))
		}
		p := v.pointer()
		// Non-nil func value points at data block.
		// First word of data block is actual code.
		if p != nil {
			p = *(*unsafe.Pointer)(p)
		}
		return p
	case Slice:
		return (*unsafeheader.Slice)(v.dataPtr()).Data
	case String:
		// gd: for inline-rep strings Data is nil and the bytes live
		// in the header itself — return &s.Hash so callers get a
		// valid byte pointer. Lifetime is tied to the Value's
		// flagIndir storage.
		sh := (*unsafeheader.String)(v.dataPtr())
		if sh.Data != nil {
			return sh.Data
		}
		return unsafe.Pointer(uintptr(v.dataPtr()) + goarch.PtrSize)
	}
	panic(&ValueError{"reflect.Value.UnsafePointer", v.kind()})
}

// Fields returns an iterator over each [StructField] of v along with its [Value].
//
// The sequence is equivalent to calling [Value.Field] successively
// for each index i in the range [0, NumField()).
//
// It panics if v's Kind is not Struct.
func (v Value) Fields() iter.Seq2[StructField, Value] {
	t := v.Type()
	if t.Kind() != Struct {
		panic("reflect: Fields of non-struct type " + t.String())
	}
	return func(yield func(StructField, Value) bool) {
		for i := range v.NumField() {
			if !yield(t.Field(i), v.Field(i)) {
				return
			}
		}
	}
}

// Methods returns an iterator over each [Method] of v along with the corresponding
// method [Value]; this is a function with v bound as the receiver. As such, the
// receiver shouldn't be included in the arguments to [Value.Call].
//
// The sequence is equivalent to calling [Value.Method] successively
// for each index i in the range [0, NumMethod()).
//
// Methods panics if v is a nil interface value.
//
// Calling this method will force the linker to retain all exported methods in all packages.
// This may make the executable binary larger but will not affect execution time.
func (v Value) Methods() iter.Seq2[Method, Value] {
	return func(yield func(Method, Value) bool) {
		rtype := v.Type()
		for i := range v.NumMethod() {
			if !yield(rtype.Method(i), v.Method(i)) {
				return
			}
		}
	}
}

// StringHeader is the runtime representation of a string.
// It cannot be used safely or portably and its representation may
// change in a later release.
// Moreover, the Data field is not sufficient to guarantee the data
// it references will not be garbage collected, so programs must keep
// a separate, correctly typed pointer to the underlying data.
//
// Deprecated: Use unsafe.String or unsafe.StringData instead.
type StringHeader struct {
	Data uintptr
	Len  int
}

// SliceHeader is the runtime representation of a slice.
// It cannot be used safely or portably and its representation may
// change in a later release.
// Moreover, the Data field is not sufficient to guarantee the data
// it references will not be garbage collected, so programs must keep
// a separate, correctly typed pointer to the underlying data.
//
// Deprecated: Use unsafe.Slice or unsafe.SliceData instead.
type SliceHeader struct {
	Data uintptr
	Len  int
	Cap  int
}

func typesMustMatch(what string, t1, t2 Type) {
	if t1 != t2 {
		panic(what + ": " + t1.String() + " != " + t2.String())
	}
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

// Grow increases the slice's capacity, if necessary, to guarantee space for
// another n elements. After Grow(n), at least n elements can be appended
// to the slice without another allocation.
//
// It panics if v's Kind is not a [Slice], or if n is negative or too large to
// allocate the memory, or if [Value.CanSet] returns false.
func (v Value) Grow(n int) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	v.grow(n)
}

// grow is identical to Grow but does not check for assignability.
func (v Value) grow(n int) {
	p := (*unsafeheader.Slice)(v.dataPtr())
	switch {
	case n < 0:
		panic("reflect.Value.Grow: negative len")
	case p.Len+n < 0:
		panic("reflect.Value.Grow: slice overflow")
	case p.Len+n > p.Cap:
		t := v.typ().Elem()
		*p = growslice(t, *p, n)
	}
}

// extendSlice extends a slice by n elements.
//
// Unlike Value.grow, which modifies the slice in place and
// does not change the length of the slice in place,
// extendSlice returns a new slice value with the length
// incremented by the number of specified elements.
func (v Value) extendSlice(n int) Value {
	v.mustBeExported()
	v.mustBe(Slice)

	// Shallow copy the slice header to avoid mutating the source slice.
	sh := *(*unsafeheader.Slice)(v.dataPtr())
	s := &sh
	v.ptr = unsafe.Pointer(s)
	v.flag = flagIndir | flag(Slice) // equivalent flag to MakeSlice

	v.grow(n) // fine to treat as assignable since we allocate a new slice header
	s.Len += n
	return v
}

// Clear clears the contents of a map or zeros the contents of a slice.
//
// It panics if v's Kind is not [Map] or [Slice].
func (v Value) Clear() {
	switch v.Kind() {
	case Slice:
		sh := *(*unsafeheader.Slice)(v.dataPtr())
		st := (*sliceType)(unsafe.Pointer(v.typ()))
		typedarrayclear(st.Elem, sh.Data, sh.Len)
	case Map:
		mapclear(v.typ(), v.pointer())
	default:
		panic(&ValueError{"reflect.Value.Clear", v.Kind()})
	}
}

// Append appends the values x to a slice s and returns the resulting slice.
// As in Go, each x's value must be assignable to the slice's element type.
func Append(s Value, x ...Value) Value {
	s.mustBe(Slice)
	n := s.Len()
	s = s.extendSlice(len(x))
	for i, v := range x {
		s.Index(n + i).Set(v)
	}
	return s
}

// AppendSlice appends a slice t to a slice s and returns the resulting slice.
// The slices s and t must have the same element type.
func AppendSlice(s, t Value) Value {
	s.mustBe(Slice)
	t.mustBe(Slice)
	typesMustMatch("reflect.AppendSlice", s.Type().Elem(), t.Type().Elem())
	ns := s.Len()
	nt := t.Len()
	s = s.extendSlice(nt)
	Copy(s.Slice(ns, ns+nt), t)
	return s
}

// Copy copies the contents of src into dst until either
// dst has been filled or src has been exhausted.
// It returns the number of elements copied.
// Dst and src each must have kind [Slice] or [Array], and
// dst and src must have the same element type.
// It dst is an [Array], it panics if [Value.CanSet] returns false.
//
// As a special case, src can have kind [String] if the element type of dst is kind [Uint8].
func Copy(dst, src Value) int {
	dk := dst.kind()
	if dk != Array && dk != Slice {
		panic(&ValueError{"reflect.Copy", dk})
	}
	if dk == Array {
		dst.mustBeAssignable()
	}
	dst.mustBeExported()

	sk := src.kind()
	var stringCopy bool
	if sk != Array && sk != Slice {
		stringCopy = sk == String && dst.typ().Elem().Kind() == abi.Uint8
		if !stringCopy {
			panic(&ValueError{"reflect.Copy", sk})
		}
	}
	src.mustBeExported()

	de := dst.typ().Elem()
	if !stringCopy {
		se := src.typ().Elem()
		typesMustMatch("reflect.Copy", toType(de), toType(se))
	}

	var ds, ss unsafeheader.Slice
	if dk == Array {
		ds.Data = (&dst).dataPtr()
		ds.Len = dst.Len()
		ds.Cap = ds.Len
	} else {
		ds = *(*unsafeheader.Slice)((&dst).dataPtr())
	}
	if sk == Array {
		ss.Data = (&src).dataPtr()
		ss.Len = src.Len()
		ss.Cap = ss.Len
	} else if sk == Slice {
		ss = *(*unsafeheader.Slice)((&src).dataPtr())
	} else {
		// gd: decode inline-vs-heap for the source string and produce
		// a Slice view of its bytes. For inline rep Data must point
		// at the header's inline-byte area, not nil.
		srcPtr := (&src).dataPtr()
		sh := *(*unsafeheader.String)(srcPtr)
		raw := uint(sh.Len)
		if tag := raw >> abi.StringTagShift; tag != 0 {
			ss.Data = unsafe.Pointer(uintptr(srcPtr) + goarch.PtrSize)
			ss.Len = int(tag)
		} else {
			ss.Data = sh.Data
			ss.Len = int(raw & abi.StringLenMask)
		}
		ss.Cap = ss.Len
	}

	return typedslicecopy(de.Common(), ds, ss)
}

// A runtimeSelect is a single case passed to rselect.
// This must match ../runtime/select.go:/runtimeSelect
type runtimeSelect struct {
	dir SelectDir      // SelectSend, SelectRecv or SelectDefault
	typ *rtype         // channel type
	ch  unsafe.Pointer // channel
	val unsafe.Pointer // ptr to data (SendDir) or ptr to receive buffer (RecvDir)
}

// rselect runs a select. It returns the index of the chosen case.
// If the case was a receive, val is filled in with the received value.
// The conventional OK bool indicates whether the receive corresponds
// to a sent value.
//
// rselect generally doesn't escape the runtimeSelect slice, except
// that for the send case the value to send needs to escape. We don't
// have a way to represent that in the function signature. So we handle
// that with a forced escape in function Select.
//
//go:noescape
func rselect([]runtimeSelect) (chosen int, recvOK bool)

// A SelectDir describes the communication direction of a select case.
type SelectDir int

// NOTE: These values must match ../runtime/select.go:/selectDir.

const (
	_             SelectDir = iota
	SelectSend              // case Chan <- Send
	SelectRecv              // case <-Chan:
	SelectDefault           // default
)

// A SelectCase describes a single case in a select operation.
// The kind of case depends on Dir, the communication direction.
//
// If Dir is SelectDefault, the case represents a default case.
// Chan and Send must be zero Values.
//
// If Dir is SelectSend, the case represents a send operation.
// Normally Chan's underlying value must be a channel, and Send's underlying value must be
// assignable to the channel's element type. As a special case, if Chan is a zero Value,
// then the case is ignored, and the field Send will also be ignored and may be either zero
// or non-zero.
//
// If Dir is [SelectRecv], the case represents a receive operation.
// Normally Chan's underlying value must be a channel and Send must be a zero Value.
// If Chan is a zero Value, then the case is ignored, but Send must still be a zero Value.
// When a receive operation is selected, the received Value is returned by Select.
type SelectCase struct {
	Dir  SelectDir // direction of case
	Chan Value     // channel to use (for send or receive)
	Send Value     // value to send (for send)
}

// Select executes a select operation described by the list of cases.
// Like the Go select statement, it blocks until at least one of the cases
// can proceed, makes a uniform pseudo-random choice,
// and then executes that case. It returns the index of the chosen case
// and, if that case was a receive operation, the value received and a
// boolean indicating whether the value corresponds to a send on the channel
// (as opposed to a zero value received because the channel is closed).
// Select supports a maximum of 65536 cases.
func Select(cases []SelectCase) (chosen int, recv Value, recvOK bool) {
	if len(cases) > 65536 {
		panic("reflect.Select: too many cases (max 65536)")
	}
	// NOTE: Do not trust that caller is not modifying cases data underfoot.
	// The range is safe because the caller cannot modify our copy of the len
	// and each iteration makes its own copy of the value c.
	var runcases []runtimeSelect
	if len(cases) > 4 {
		// Slice is heap allocated due to runtime dependent capacity.
		runcases = make([]runtimeSelect, len(cases))
	} else {
		// Slice can be stack allocated due to constant capacity.
		runcases = make([]runtimeSelect, len(cases), 4)
	}

	haveDefault := false
	for i, c := range cases {
		rc := &runcases[i]
		rc.dir = c.Dir
		switch c.Dir {
		default:
			panic("reflect.Select: invalid Dir")

		case SelectDefault: // default
			if haveDefault {
				panic("reflect.Select: multiple default cases")
			}
			haveDefault = true
			if c.Chan.IsValid() {
				panic("reflect.Select: default case has Chan value")
			}
			if c.Send.IsValid() {
				panic("reflect.Select: default case has Send value")
			}

		case SelectSend:
			ch := c.Chan
			if !ch.IsValid() {
				break
			}
			ch.mustBe(Chan)
			ch.mustBeExported()
			tt := (*chanType)(unsafe.Pointer(ch.typ()))
			if ChanDir(tt.Dir)&SendDir == 0 {
				panic("reflect.Select: SendDir case using recv-only channel")
			}
			rc.ch = ch.pointer()
			rc.typ = toRType(&tt.Type)
			v := c.Send
			if !v.IsValid() {
				panic("reflect.Select: SendDir case missing Send value")
			}
			v.mustBeExported()
			v = v.assignTo("reflect.Select", tt.Elem, nil)
			if v.flag&flagIndir != 0 {
				(&v).materialize() // rc.val escapes to runtime; need stable pointer
				rc.val = v.ptr
			} else {
				rc.val = unsafe.Pointer(&v.ptr)
			}
			// The value to send needs to escape. See the comment at rselect for
			// why we need forced escape.
			escapes(rc.val)

		case SelectRecv:
			if c.Send.IsValid() {
				panic("reflect.Select: RecvDir case has Send value")
			}
			ch := c.Chan
			if !ch.IsValid() {
				break
			}
			ch.mustBe(Chan)
			ch.mustBeExported()
			tt := (*chanType)(unsafe.Pointer(ch.typ()))
			if ChanDir(tt.Dir)&RecvDir == 0 {
				panic("reflect.Select: RecvDir case using send-only channel")
			}
			rc.ch = ch.pointer()
			rc.typ = toRType(&tt.Type)
			rc.val = unsafe_New(tt.Elem)
		}
	}

	chosen, recvOK = rselect(runcases)
	if runcases[chosen].dir == SelectRecv {
		tt := (*chanType)(unsafe.Pointer(runcases[chosen].typ))
		t := tt.Elem
		p := runcases[chosen].val
		fl := flag(t.Kind())
		if !t.IsDirectIface() {
			recv = Value{typ_: t, ptr: p, flag: fl | flagIndir}
		} else {
			recv = Value{typ_: t, ptr: *(*unsafe.Pointer)(p), flag: fl}
		}
	}
	return chosen, recv, recvOK
}

/*
 * constructors
 */

// implemented in package runtime

//go:noescape
func unsafe_New(*abi.Type) unsafe.Pointer

//go:noescape
func unsafe_NewArray(*abi.Type, int) unsafe.Pointer

// MakeSlice creates a new zero-initialized slice value
// for the specified slice type, length, and capacity.
func MakeSlice(typ Type, len, cap int) Value {
	if typ.Kind() != Slice {
		panic("reflect.MakeSlice of non-slice type")
	}
	if len < 0 {
		panic("reflect.MakeSlice: negative len")
	}
	if cap < 0 {
		panic("reflect.MakeSlice: negative cap")
	}
	if len > cap {
		panic("reflect.MakeSlice: len > cap")
	}

	s := unsafeheader.Slice{Data: unsafe_NewArray(&(typ.Elem().(*rtype).t), cap), Len: len, Cap: cap}
	return Value{typ_: &typ.(*rtype).t, ptr: unsafe.Pointer(&s), flag: flagIndir | flag(Slice)}
}

// SliceAt returns a [Value] representing a slice whose underlying
// data starts at p, with length and capacity equal to n.
//
// This is like [unsafe.Slice].
func SliceAt(typ Type, p unsafe.Pointer, n int) Value {
	unsafeslice(typ.common(), p, n)
	s := unsafeheader.Slice{Data: p, Len: n, Cap: n}
	return Value{typ_: SliceOf(typ).common(), ptr: unsafe.Pointer(&s), flag: flagIndir | flag(Slice)}
}

// MakeChan creates a new channel with the specified type and buffer size.
func MakeChan(typ Type, buffer int) Value {
	if typ.Kind() != Chan {
		panic("reflect.MakeChan of non-chan type")
	}
	if buffer < 0 {
		panic("reflect.MakeChan: negative buffer size")
	}
	if typ.ChanDir() != BothDir {
		panic("reflect.MakeChan: unidirectional channel type")
	}
	t := typ.common()
	ch := makechan(t, buffer)
	return Value{typ_: t, ptr: ch, flag: flag(Chan)}
}

// MakeMap creates a new map with the specified type.
func MakeMap(typ Type) Value {
	return MakeMapWithSize(typ, 0)
}

// MakeMapWithSize creates a new map with the specified type
// and initial space for approximately n elements.
func MakeMapWithSize(typ Type, n int) Value {
	if typ.Kind() != Map {
		panic("reflect.MakeMapWithSize of non-map type")
	}
	t := typ.common()
	m := makemap(t, n)
	return Value{typ_: t, ptr: m, flag: flag(Map)}
}

// Indirect returns the value that v points to.
// If v is a nil pointer, Indirect returns a zero Value.
// If v is not a pointer, Indirect returns v.
func Indirect(v Value) Value {
	if v.Kind() != Pointer {
		return v
	}
	return v.Elem()
}

// ValueOf returns a new Value initialized to the concrete value
// stored in the interface i. ValueOf(nil) returns the zero Value.
func ValueOf(i any) Value {
	if i == nil {
		return Value{}
	}
	return unpackEface(i)
}

// Zero returns a Value representing the zero value for the specified type.
// The result is different from the zero value of the Value struct,
// which represents no value at all.
// For example, Zero(TypeOf(42)) returns a Value with Kind [Int] and value 0.
// The returned value is neither addressable nor settable.
func Zero(typ Type) Value {
	if typ == nil {
		panic("reflect: Zero(nil)")
	}
	t := &typ.(*rtype).t
	fl := flag(t.Kind())
	if !t.IsDirectIface() {
		var p unsafe.Pointer
		if t.Size() <= abi.ZeroValSize {
			p = unsafe.Pointer(&zeroVal[0])
		} else {
			p = unsafe_New(t)
		}
		return Value{typ_: t, ptr: p, flag: fl | flagIndir}
	}
	return Value{typ_: t, ptr: nil, flag: fl}
}

//go:linkname zeroVal runtime.zeroVal
var zeroVal [abi.ZeroValSize]byte

// New returns a Value representing a pointer to a new zero value
// for the specified type. That is, the returned Value's Type is [PointerTo](typ).
func New(typ Type) Value {
	if typ == nil {
		panic("reflect: New(nil)")
	}
	t := &typ.(*rtype).t
	pt := ptrTo(t)
	if !pt.IsDirectIface() {
		// This is a pointer to a not-in-heap type.
		panic("reflect: New of type that may not be allocated in heap (possibly undefined cgo C type)")
	}
	ptr := unsafe_New(t)
	fl := flag(Pointer)
	return Value{typ_: pt, ptr: ptr, flag: fl}
}

// NewAt returns a Value representing a pointer to a value of the
// specified type, using p as that pointer.
func NewAt(typ Type, p unsafe.Pointer) Value {
	fl := flag(Pointer)
	t := typ.(*rtype)
	return Value{typ_: t.ptrTo(), ptr: p, flag: fl}
}

// assignTo returns a value v that can be assigned directly to dst.
// It panics if v is not assignable to dst.
// For a conversion to an interface type, target, if not nil,
// is a suggested scratch space to use.
// target must be initialized memory (or nil).
func (v Value) assignTo(context string, dst *abi.Type, target unsafe.Pointer) Value {
	if v.flag&flagMethod != 0 {
		v = makeMethodValue(context, v)
	}

	switch {
	case directlyAssignable(dst, v.typ()):
		// Overwrite type so that they match.
		// Same memory layout, so no harm done.
		fl := v.flag&(flagAddr|flagIndir|flagInline|flagSpread) | v.flag.ro()
		fl |= flag(dst.Kind())
		// Preserve the whole value (including inline / spread bytes)
		// so that flagInline / flagSpread Values remain alloc-free
		// across assignTo.
		out := v
		out.typ_ = dst
		out.flag = fl
		return out

	case implements(dst, v.typ()):
		if v.Kind() == Interface && v.IsNil() {
			// A nil ReadWriter passed to nil Reader is OK, but using
			// ifaceE2I below would panic. Allocate a zeroed iface header
			// so callers that need a stable indirect pointer (e.g. Call
			// splitting Interface across int+float registers under fat
			// iface) still have one; the bytes are all zero.
			if target == nil {
				target = unsafe_New(dst)
			}
			return Value{typ_: dst, ptr: target, flag: flagIndir | flag(Interface)}
		}
		x := valueInterface(v, false)
		if target == nil {
			target = unsafe_New(dst)
		}
		if dst.NumMethod() == 0 {
			*(*any)(target) = x
		} else {
			ifaceE2I(dst, x, target)
		}
		return Value{typ_: dst, ptr: target, flag: flagIndir | flag(Interface)}
	}

	// Failed.
	panic(context + ": value of type " + stringFor(v.typ()) + " is not assignable to type " + stringFor(dst))
}

// Convert returns the value v converted to type t.
// If the usual Go conversion rules do not allow conversion
// of the value v to type t, or if converting v to type t panics, Convert panics.
func (v Value) Convert(t Type) Value {
	if v.flag&flagMethod != 0 {
		v = makeMethodValue("Convert", v)
	}
	op := convertOp(t.common(), v.typ())
	if op == nil {
		panic("reflect.Value.Convert: value of type " + stringFor(v.typ()) + " cannot be converted to type " + t.String())
	}
	return op(v, t)
}

// CanConvert reports whether the value v can be converted to type t.
// If v.CanConvert(t) returns true then v.Convert(t) will not panic.
func (v Value) CanConvert(t Type) bool {
	vt := v.Type()
	if !vt.ConvertibleTo(t) {
		return false
	}
	// Converting from slice to array or to pointer-to-array can panic
	// depending on the value.
	switch {
	case vt.Kind() == Slice && t.Kind() == Array:
		if t.Len() > v.Len() {
			return false
		}
	case vt.Kind() == Slice && t.Kind() == Pointer && t.Elem().Kind() == Array:
		n := t.Elem().Len()
		if n > v.Len() {
			return false
		}
	}
	return true
}

// Comparable reports whether the value v is comparable.
// If the type of v is an interface, this checks the dynamic type.
// If this reports true then v.Interface() == x will not panic for any x,
// nor will v.Equal(u) for any Value u.
func (v Value) Comparable() bool {
	k := v.Kind()
	switch k {
	case Invalid:
		return false

	case Array:
		switch v.Type().Elem().Kind() {
		case Interface, Array, Struct:
			for i := 0; i < v.Type().Len(); i++ {
				if !v.Index(i).Comparable() {
					return false
				}
			}
			return true
		}
		return v.Type().Comparable()

	case Interface:
		return v.IsNil() || v.Elem().Comparable()

	case Struct:
		for _, value := range v.Fields() {
			if !value.Comparable() {
				return false
			}
		}
		return true

	default:
		return v.Type().Comparable()
	}
}

// Equal reports true if v is equal to u.
// For two invalid values, Equal will report true.
// For an interface value, Equal will compare the value within the interface.
// Otherwise, If the values have different types, Equal will report false.
// Otherwise, for arrays and structs Equal will compare each element in order,
// and report false if it finds non-equal elements.
// During all comparisons, if values of the same type are compared,
// and the type is not comparable, Equal will panic.
func (v Value) Equal(u Value) bool {
	if v.Kind() == Interface {
		v = v.Elem()
	}
	if u.Kind() == Interface {
		u = u.Elem()
	}

	if !v.IsValid() || !u.IsValid() {
		return v.IsValid() == u.IsValid()
	}

	if v.Kind() != u.Kind() || v.Type() != u.Type() {
		return false
	}

	// Handle each Kind directly rather than calling valueInterface
	// to avoid allocating.
	switch v.Kind() {
	default:
		panic("reflect.Value.Equal: invalid Kind")
	case Bool:
		return v.Bool() == u.Bool()
	case Int, Int8, Int16, Int32, Int64:
		return v.Int() == u.Int()
	case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
		return v.Uint() == u.Uint()
	case Float32, Float64:
		return v.Float() == u.Float()
	case Complex64, Complex128:
		return v.Complex() == u.Complex()
	case String:
		return v.String() == u.String()
	case Chan, Pointer, UnsafePointer:
		return v.Pointer() == u.Pointer()
	case Array:
		// u and v have the same type so they have the same length
		vl := v.Len()
		if vl == 0 {
			// panic on [0]func()
			if !v.Type().Elem().Comparable() {
				break
			}
			return true
		}
		for i := 0; i < vl; i++ {
			if !v.Index(i).Equal(u.Index(i)) {
				return false
			}
		}
		return true
	case Struct:
		// u and v have the same type so they have the same fields
		nf := v.NumField()
		for i := 0; i < nf; i++ {
			if !v.Field(i).Equal(u.Field(i)) {
				return false
			}
		}
		return true
	case Func, Map, Slice:
		break
	}
	panic("reflect.Value.Equal: values of type " + v.Type().String() + " are not comparable")
}

// convertOp returns the function to convert a value of type src
// to a value of type dst. If the conversion is illegal, convertOp returns nil.
func convertOp(dst, src *abi.Type) func(Value, Type) Value {
	switch Kind(src.Kind()) {
	case Int, Int8, Int16, Int32, Int64:
		switch Kind(dst.Kind()) {
		case Int, Int8, Int16, Int32, Int64, Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
			return cvtInt
		case Float32, Float64:
			return cvtIntFloat
		case String:
			return cvtIntString
		}

	case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
		switch Kind(dst.Kind()) {
		case Int, Int8, Int16, Int32, Int64, Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
			return cvtUint
		case Float32, Float64:
			return cvtUintFloat
		case String:
			return cvtUintString
		}

	case Float32, Float64:
		switch Kind(dst.Kind()) {
		case Int, Int8, Int16, Int32, Int64:
			return cvtFloatInt
		case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
			return cvtFloatUint
		case Float32, Float64:
			return cvtFloat
		}

	case Complex64, Complex128:
		switch Kind(dst.Kind()) {
		case Complex64, Complex128:
			return cvtComplex
		}

	case String:
		if dst.Kind() == abi.Slice && pkgPathFor(dst.Elem()) == "" {
			switch Kind(dst.Elem().Kind()) {
			case Uint8:
				return cvtStringBytes
			case Int32:
				return cvtStringRunes
			}
		}

	case Slice:
		if dst.Kind() == abi.String && pkgPathFor(src.Elem()) == "" {
			switch Kind(src.Elem().Kind()) {
			case Uint8:
				return cvtBytesString
			case Int32:
				return cvtRunesString
			}
		}
		// "x is a slice, T is a pointer-to-array type,
		// and the slice and array types have identical element types."
		if dst.Kind() == abi.Pointer && dst.Elem().Kind() == abi.Array && src.Elem() == dst.Elem().Elem() {
			return cvtSliceArrayPtr
		}
		// "x is a slice, T is an array type,
		// and the slice and array types have identical element types."
		if dst.Kind() == abi.Array && src.Elem() == dst.Elem() {
			return cvtSliceArray
		}

	case Chan:
		if dst.Kind() == abi.Chan && specialChannelAssignability(dst, src) {
			return cvtDirect
		}
	}

	// dst and src have same underlying type.
	if haveIdenticalUnderlyingType(dst, src, false) {
		return cvtDirect
	}

	// dst and src are non-defined pointer types with same underlying base type.
	if dst.Kind() == abi.Pointer && nameFor(dst) == "" &&
		src.Kind() == abi.Pointer && nameFor(src) == "" &&
		haveIdenticalUnderlyingType(elem(dst), elem(src), false) {
		return cvtDirect
	}

	if implements(dst, src) {
		if src.Kind() == abi.Interface {
			return cvtI2I
		}
		return cvtT2I
	}

	return nil
}

// makeInt returns a Value of type t equal to bits (possibly truncated),
// where t is a signed or unsigned int type.
func makeInt(f flag, bits uint64, t Type) Value {
	typ := t.common()
	ptr := unsafe_New(typ)
	switch typ.Size() {
	case 1:
		*(*uint8)(ptr) = uint8(bits)
	case 2:
		*(*uint16)(ptr) = uint16(bits)
	case 4:
		*(*uint32)(ptr) = uint32(bits)
	case 8:
		*(*uint64)(ptr) = bits
	}
	return Value{typ_: typ, ptr: ptr, flag: f | flagIndir | flag(typ.Kind())}
}

// makeFloat returns a Value of type t equal to v (possibly truncated to float32),
// where t is a float32 or float64 type.
func makeFloat(f flag, v float64, t Type) Value {
	typ := t.common()
	ptr := unsafe_New(typ)
	switch typ.Size() {
	case 4:
		*(*float32)(ptr) = float32(v)
	case 8:
		*(*float64)(ptr) = v
	}
	return Value{typ_: typ, ptr: ptr, flag: f | flagIndir | flag(typ.Kind())}
}

// makeFloat32 returns a Value of type t equal to v, where t is a float32 type.
func makeFloat32(f flag, v float32, t Type) Value {
	typ := t.common()
	ptr := unsafe_New(typ)
	*(*float32)(ptr) = v
	return Value{typ_: typ, ptr: ptr, flag: f | flagIndir | flag(typ.Kind())}
}

// makeComplex returns a Value of type t equal to v (possibly truncated to complex64),
// where t is a complex64 or complex128 type.
func makeComplex(f flag, v complex128, t Type) Value {
	typ := t.common()
	ptr := unsafe_New(typ)
	switch typ.Size() {
	case 8:
		*(*complex64)(ptr) = complex64(v)
	case 16:
		*(*complex128)(ptr) = v
	}
	return Value{typ_: typ, ptr: ptr, flag: f | flagIndir | flag(typ.Kind())}
}

func makeString(f flag, v string, t Type) Value {
	ret := New(t).Elem()
	ret.SetString(v)
	ret.flag = ret.flag&^flagAddr | f
	return ret
}

func makeBytes(f flag, v []byte, t Type) Value {
	ret := New(t).Elem()
	ret.SetBytes(v)
	ret.flag = ret.flag&^flagAddr | f
	return ret
}

func makeRunes(f flag, v []rune, t Type) Value {
	ret := New(t).Elem()
	ret.setRunes(v)
	ret.flag = ret.flag&^flagAddr | f
	return ret
}

// These conversion functions are returned by convertOp
// for classes of conversions. For example, the first function, cvtInt,
// takes any value v of signed int type and returns the value converted
// to type t, where t is any signed or unsigned int type.

// convertOp: intXX -> [u]intXX
func cvtInt(v Value, t Type) Value {
	return makeInt(v.flag.ro(), uint64(v.Int()), t)
}

// convertOp: uintXX -> [u]intXX
func cvtUint(v Value, t Type) Value {
	return makeInt(v.flag.ro(), v.Uint(), t)
}

// convertOp: floatXX -> intXX
func cvtFloatInt(v Value, t Type) Value {
	return makeInt(v.flag.ro(), uint64(int64(v.Float())), t)
}

// convertOp: floatXX -> uintXX
func cvtFloatUint(v Value, t Type) Value {
	return makeInt(v.flag.ro(), uint64(v.Float()), t)
}

// convertOp: intXX -> floatXX
func cvtIntFloat(v Value, t Type) Value {
	return makeFloat(v.flag.ro(), float64(v.Int()), t)
}

// convertOp: uintXX -> floatXX
func cvtUintFloat(v Value, t Type) Value {
	return makeFloat(v.flag.ro(), float64(v.Uint()), t)
}

// convertOp: floatXX -> floatXX
func cvtFloat(v Value, t Type) Value {
	if v.Type().Kind() == Float32 && t.Kind() == Float32 {
		// Don't do any conversion if both types have underlying type float32.
		// This avoids converting to float64 and back, which will
		// convert a signaling NaN to a quiet NaN. See issue 36400.
		return makeFloat32(v.flag.ro(), *(*float32)(v.dataPtr()), t)
	}
	return makeFloat(v.flag.ro(), v.Float(), t)
}

// convertOp: complexXX -> complexXX
func cvtComplex(v Value, t Type) Value {
	return makeComplex(v.flag.ro(), v.Complex(), t)
}

// convertOp: intXX -> string
func cvtIntString(v Value, t Type) Value {
	s := "\uFFFD"
	if x := v.Int(); int64(rune(x)) == x {
		s = string(rune(x))
	}
	return makeString(v.flag.ro(), s, t)
}

// convertOp: uintXX -> string
func cvtUintString(v Value, t Type) Value {
	s := "\uFFFD"
	if x := v.Uint(); uint64(rune(x)) == x {
		s = string(rune(x))
	}
	return makeString(v.flag.ro(), s, t)
}

// convertOp: []byte -> string
func cvtBytesString(v Value, t Type) Value {
	return makeString(v.flag.ro(), string(v.Bytes()), t)
}

// convertOp: string -> []byte
func cvtStringBytes(v Value, t Type) Value {
	return makeBytes(v.flag.ro(), []byte(v.String()), t)
}

// convertOp: []rune -> string
func cvtRunesString(v Value, t Type) Value {
	return makeString(v.flag.ro(), string(v.runes()), t)
}

// convertOp: string -> []rune
func cvtStringRunes(v Value, t Type) Value {
	return makeRunes(v.flag.ro(), []rune(v.String()), t)
}

// convertOp: []T -> *[N]T
func cvtSliceArrayPtr(v Value, t Type) Value {
	n := t.Elem().Len()
	if n > v.Len() {
		panic("reflect: cannot convert slice with length " + strconv.Itoa(v.Len()) + " to pointer to array with length " + strconv.Itoa(n))
	}
	h := (*unsafeheader.Slice)(v.dataPtr())
	// Result is Pointer kind (direct iface); clear flagSpread/flagInline
	// so ptr is interpreted as the pointer value, not as word 0 of an
	// in-Value spread/inline header.
	fl := v.flag&^(flagIndir|flagAddr|flagInline|flagSpread|flagKindMask) | flag(Pointer)
	return Value{typ_: t.common(), ptr: h.Data, flag: fl}
}

// convertOp: []T -> [N]T
func cvtSliceArray(v Value, t Type) Value {
	n := t.Len()
	if n > v.Len() {
		panic("reflect: cannot convert slice with length " + strconv.Itoa(v.Len()) + " to array with length " + strconv.Itoa(n))
	}
	h := (*unsafeheader.Slice)(v.dataPtr())
	typ := t.common()
	ptr := h.Data
	c := unsafe_New(typ)
	typedmemmove(typ, c, ptr)
	ptr = c

	// Result is a heap-backed array (flagIndir, ptr→heap). Drop
	// flagSpread/flagInline: the source layout is gone.
	fl := v.flag&^(flagAddr|flagInline|flagSpread|flagKindMask) | flag(Array)
	return Value{typ_: typ, ptr: ptr, flag: fl}
}

// convertOp: direct copy
func cvtDirect(v Value, typ Type) Value {
	t := typ.common()
	if v.flag&flagAddr != 0 {
		// indirect, mutable word - make a copy
		c := unsafe_New(t)
		typedmemmove(t, c, v.dataPtr())
		v.ptr = c
		v.flag &^= flagAddr
	}
	// Preserve inline bytes on flagInline Values by rewriting v in place
	// instead of constructing a fresh header that drops v.inline.
	v.typ_ = t
	return v
}

// convertOp: concrete -> interface
func cvtT2I(v Value, typ Type) Value {
	target := unsafe_New(typ.common())
	x := valueInterface(v, false)
	if typ.NumMethod() == 0 {
		*(*any)(target) = x
	} else {
		ifaceE2I(typ.common(), x, target)
	}
	return Value{typ_: typ.common(), ptr: target, flag: v.flag.ro() | flagIndir | flag(Interface)}
}

// convertOp: interface -> interface
func cvtI2I(v Value, typ Type) Value {
	if v.IsNil() {
		ret := Zero(typ)
		ret.flag |= v.flag.ro()
		return ret
	}
	return cvtT2I(v.Elem(), typ)
}

// implemented in ../runtime
//
//go:noescape
func chancap(ch unsafe.Pointer) int

//go:noescape
func chanclose(ch unsafe.Pointer)

//go:noescape
func chanlen(ch unsafe.Pointer) int

// Note: some of the noescape annotations below are technically a lie,
// but safe in the context of this package. Functions like chansend0
// and mapassign0 don't escape the referent, but may escape anything
// the referent points to (they do shallow copies of the referent).
// We add a 0 to their names and wrap them in functions with the
// proper escape behavior.

//go:noescape
func chanrecv(ch unsafe.Pointer, nb bool, val unsafe.Pointer) (selected, received bool)

//go:noescape
func chansend0(ch unsafe.Pointer, val unsafe.Pointer, nb bool) bool

func chansend(ch unsafe.Pointer, val unsafe.Pointer, nb bool) bool {
	contentEscapes(val)
	return chansend0(ch, val, nb)
}

func makechan(typ *abi.Type, size int) (ch unsafe.Pointer)
func makemap(t *abi.Type, cap int) (m unsafe.Pointer)

//go:noescape
func mapaccess(t *abi.Type, m unsafe.Pointer, key unsafe.Pointer) (val unsafe.Pointer)

//go:noescape
func mapaccess_faststr(t *abi.Type, m unsafe.Pointer, key string) (val unsafe.Pointer)

//go:noescape
func mapassign0(t *abi.Type, m unsafe.Pointer, key, val unsafe.Pointer)

// mapassign should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/modern-go/reflect2
//   - github.com/goccy/go-json
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname mapassign
func mapassign(t *abi.Type, m unsafe.Pointer, key, val unsafe.Pointer) {
	contentEscapes(key)
	contentEscapes(val)
	mapassign0(t, m, key, val)
}

//go:noescape
func mapassign_faststr0(t *abi.Type, m unsafe.Pointer, key string, val unsafe.Pointer)

func mapassign_faststr(t *abi.Type, m unsafe.Pointer, key string, val unsafe.Pointer) {
	contentEscapes((*unsafeheader.String)(unsafe.Pointer(&key)).Data)
	contentEscapes(val)
	mapassign_faststr0(t, m, key, val)
}

//go:noescape
func mapdelete(t *abi.Type, m unsafe.Pointer, key unsafe.Pointer)

//go:noescape
func mapdelete_faststr(t *abi.Type, m unsafe.Pointer, key string)

//go:noescape
func maplen(m unsafe.Pointer) int

func mapclear(t *abi.Type, m unsafe.Pointer)

// call calls fn with "stackArgsSize" bytes of stack arguments laid out
// at stackArgs and register arguments laid out in regArgs. frameSize is
// the total amount of stack space that will be reserved by call, so this
// should include enough space to spill register arguments to the stack in
// case of preemption.
//
// After fn returns, call copies stackArgsSize-stackRetOffset result bytes
// back into stackArgs+stackRetOffset before returning, for any return
// values passed on the stack. Register-based return values will be found
// in the same regArgs structure.
//
// regArgs must also be prepared with an appropriate ReturnIsPtr bitmap
// indicating which registers will contain pointer-valued return values. The
// purpose of this bitmap is to keep pointers visible to the GC between
// returning from reflectcall and actually using them.
//
// If copying result bytes back from the stack, the caller must pass the
// argument frame type as stackArgsType, so that call can execute appropriate
// write barriers during the copy.
//
// Arguments passed through to call do not escape. The type is used only in a
// very limited callee of call, the stackArgs are copied, and regArgs is only
// used in the call frame.
//
//go:noescape
//go:linkname call runtime.reflectcall
func call(stackArgsType *abi.Type, f, stackArgs unsafe.Pointer, stackArgsSize, stackRetOffset, frameSize uint32, regArgs *abi.RegArgs)

func ifaceE2I(t *abi.Type, src any, dst unsafe.Pointer)

// memmove copies size bytes to dst from src. No write barriers are used.
//
//go:noescape
func memmove(dst, src unsafe.Pointer, size uintptr)

// typedmemmove copies a value of type t to dst from src.
//
//go:noescape
func typedmemmove(t *abi.Type, dst, src unsafe.Pointer)

// typedmemclr zeros the value at ptr of type t.
//
//go:noescape
func typedmemclr(t *abi.Type, ptr unsafe.Pointer)

// typedmemclrpartial is like typedmemclr but assumes that
// dst points off bytes into the value and only clears size bytes.
//
//go:noescape
func typedmemclrpartial(t *abi.Type, ptr unsafe.Pointer, off, size uintptr)

// typedslicecopy copies a slice of elemType values from src to dst,
// returning the number of elements copied.
//
//go:noescape
func typedslicecopy(t *abi.Type, dst, src unsafeheader.Slice) int

// typedarrayclear zeroes the value at ptr of an array of elemType,
// only clears len elem.
//
//go:noescape
func typedarrayclear(elemType *abi.Type, ptr unsafe.Pointer, len int)

//go:noescape
func typehash(t *abi.Type, p unsafe.Pointer, h uintptr) uintptr

func verifyNotInHeapPtr(p uintptr) bool

//go:noescape
func growslice(t *abi.Type, old unsafeheader.Slice, num int) unsafeheader.Slice

//go:noescape
func unsafeslice(t *abi.Type, ptr unsafe.Pointer, len int)

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

// Dummy annotation marking that the content of value x
// escapes (i.e. modeling roughly heap=*x),
// for use in cases where the reflect code is so clever that
// the compiler cannot follow.
func contentEscapes(x unsafe.Pointer) {
	if dummy.b {
		escapes(*(*any)(x)) // the dereference may not always be safe, but never executed
	}
}
