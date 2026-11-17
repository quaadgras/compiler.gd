// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.gc

// Package js gives access to the WebAssembly host environment when using the js/wasm architecture.
// Its API is based on JavaScript semantics.
//
// This package is EXPERIMENTAL. Its current scope is only to allow tests to run, but not yet to provide a
// comprehensive API for users. It is exempt from the Go compatibility promise.
package js

import "unsafe"

// This is syscall/js for GOWASM=gc (doc/gd/wasm-gc.md, stage 18): a
// JavaScript value is held by the engine as a reference (an externref),
// in an object that Go has as a *externref. It lives as long as Go
// holds that pointer, and the engine's collector frees it: there is no
// table of values in the host and nothing to release. The functions of
// module gojs take and return the JavaScript value itself where Go
// passes or gets a *externref (the compiler's wrapper of each import
// takes it out of its object, or puts it in a new one: see
// obj.WasmExtern), and their other arguments as WebAssembly values.
//
// Numbers, booleans, null and undefined are not held by the engine: a
// Value has its type and, for a number or a boolean, its value, so
// that Type, Float, Int, Bool and Equal on them ask nothing of the
// host. A function of the host that returns a value returns it as a
// reference only if it is a string, a symbol, an object or a function
// (else null), and writes its type and number in ret.

// externref is what the engine holds a JavaScript value in. The
// compiler knows a *externref by its name; Go code never dereferences
// one.
type externref struct{}

// Value represents a JavaScript value. The zero value is the JavaScript value "undefined".
// Values can be checked for equality with the Equal method.
type Value struct {
	_   [0]func() // uncomparable; to make == not compile
	typ Type
	num float64    // a number, or 1 or 0 for a boolean
	ext *externref // a string, a symbol, an object or a function
}

// ret is where the host writes the type of the value it returns (plus
// retThrew if that is an exception the call threw) and, for a number
// or a boolean, its value.
var ret [2]float64

const retThrew = 8

var (
	// The values that are references when passed to the host (see
	// arg), and the global object and the Go object of the host.
	extUndefined = predef(0)
	extTrue      = predef(1)
	extFalse     = predef(2)
	extNaN       = predef(3)
	valueGlobal  = makeValue(predef(4))
	jsGo         = makeValue(predef(5)) // the host's object for package js: _makeFuncWrapper, _pendingEvent

	valueUndefined = Value{}
	valueNull      = Value{typ: TypeNull}
	valueTrue      = Value{typ: TypeBoolean, num: 1}
	valueFalse     = Value{typ: TypeBoolean}

	objectConstructor = valueGlobal.Get("Object")
	arrayConstructor  = valueGlobal.Get("Array")
)

// predef returns the value the host has numbered n (see above). The
// first call tells the host where ret is.
//
//go:wasmimport gojs syscall/js.predef
//go:noescape
func predef0(n int32, ret unsafe.Pointer) *externref

func predef(n int32) *externref {
	return predef0(n, unsafe.Pointer(&ret))
}

// countalloc counts an allocation in runtime.MemStats: each value the
// host returns as a reference is a new object of Go's.
//
//go:linkname countalloc runtime.countalloc
func countalloc()

// makeValue makes the Value the host just returned.
func makeValue(x *externref) Value {
	if x != nil {
		countalloc()
	}
	return Value{typ: Type(ret[0]) &^ retThrew, num: ret[1], ext: x}
}

// yield lets other goroutines run now and then after calls into
// JavaScript, which may have called into Go (in the runtime: a
// workaround for node 24).
func yield()

// threw reports whether the value the host just returned is an
// exception that the call threw.
func threw() bool {
	return int(ret[0])&retThrew != 0
}

// arg returns v as the host takes it: a reference, or, if that is nil,
// a number (or, if that is NaN, null).
func (v Value) arg() (*externref, float64) {
	switch {
	case v.ext != nil:
		return v.ext, 0
	case v.typ == TypeNumber:
		if v.num != v.num {
			return extNaN, 0
		}
		return nil, v.num
	case v.typ == TypeNull:
		return nil, nan()
	case v.typ == TypeBoolean:
		if v.num != 0 {
			return extTrue, 0
		}
		return extFalse, 0
	}
	return extUndefined, 0
}

func nan() float64 {
	n := uint64(0x7FF8000000000001)
	return *(*float64)(unsafe.Pointer(&n))
}

func floatValue(f float64) Value {
	return Value{typ: TypeNumber, num: f}
}

// Error wraps a JavaScript error.
type Error struct {
	// Value is the underlying JavaScript error value.
	Value
}

// Error implements the error interface.
func (e Error) Error() string {
	return "JavaScript error: " + e.Get("message").String()
}

// Equal reports whether v and w are equal according to JavaScript's === operator.
func (v Value) Equal(w Value) bool {
	if v.ext != nil || w.ext != nil {
		return v.ext != nil && w.ext != nil && valueEqual(v.ext, w.ext)
	}
	if v.typ != w.typ {
		return false
	}
	return v.num == w.num // (NaN is not NaN)
}

//go:wasmimport gojs syscall/js.valueEqual
func valueEqual(v, w *externref) bool

// Undefined returns the JavaScript value "undefined".
func Undefined() Value {
	return valueUndefined
}

// IsUndefined reports whether v is the JavaScript value "undefined".
func (v Value) IsUndefined() bool {
	return v.typ == TypeUndefined
}

// Null returns the JavaScript value "null".
func Null() Value {
	return valueNull
}

// IsNull reports whether v is the JavaScript value "null".
func (v Value) IsNull() bool {
	return v.typ == TypeNull
}

// IsNaN reports whether v is the JavaScript value "NaN".
func (v Value) IsNaN() bool {
	return v.typ == TypeNumber && v.num != v.num
}

// Global returns the JavaScript global object, usually "window" or "global".
func Global() Value {
	return valueGlobal
}

// ValueOf returns x as a JavaScript value:
//
//	| Go                     | JavaScript             |
//	| ---------------------- | ---------------------- |
//	| js.Value               | [its value]            |
//	| js.Func                | function               |
//	| nil                    | null                   |
//	| bool                   | boolean                |
//	| integers and floats    | number                 |
//	| string                 | string                 |
//	| []interface{}          | new array              |
//	| map[string]interface{} | new object             |
//
// Panics if x is not one of the expected types.
func ValueOf(x any) Value {
	switch x := x.(type) {
	case Value:
		return x
	case Func:
		return x.Value
	case nil:
		return valueNull
	case bool:
		if x {
			return valueTrue
		} else {
			return valueFalse
		}
	case int:
		return floatValue(float64(x))
	case int8:
		return floatValue(float64(x))
	case int16:
		return floatValue(float64(x))
	case int32:
		return floatValue(float64(x))
	case int64:
		return floatValue(float64(x))
	case uint:
		return floatValue(float64(x))
	case uint8:
		return floatValue(float64(x))
	case uint16:
		return floatValue(float64(x))
	case uint32:
		return floatValue(float64(x))
	case uint64:
		return floatValue(float64(x))
	case uintptr:
		return floatValue(float64(x))
	case unsafe.Pointer:
		return floatValue(float64(uintptr(x)))
	case float32:
		return floatValue(float64(x))
	case float64:
		return floatValue(x)
	case string:
		return makeValue(stringVal(str(x)))
	case []any:
		a := arrayConstructor.New(len(x))
		for i, s := range x {
			a.SetIndex(i, s)
		}
		return a
	case map[string]any:
		o := objectConstructor.New()
		for k, v := range x {
			o.Set(k, v)
		}
		return o
	default:
		panic("ValueOf: invalid value")
	}
}

//go:wasmimport gojs syscall/js.stringVal
//go:noescape
func stringVal(p unsafe.Pointer, n int) *externref

// strbuf holds the bytes of a short string for the host: those of a
// string of 15 bytes or fewer are in the words of the string itself
// (see doc/gd/sso-string.md), which are not in linear memory.
var strbuf [16]byte

// str returns where the bytes of s are in linear memory, and how many.
func str(s string) (unsafe.Pointer, int) {
	if len(s) < len(strbuf) {
		return unsafe.Pointer(&strbuf), copy(strbuf[:], s)
	}
	p := unsafe.Pointer(unsafe.StringData(s))
	if !isLinear(p) {
		p = unsafe.Pointer(&[]byte(s)[0])
	}
	return p, len(s)
}

// isLinear reports whether p points into linear memory, which the host
// reads and writes: with GOWASM=gc, a []byte that new or a composite
// literal made is an object of the engine's, which the host cannot.
// Such bytes are copied. (In the runtime.)
func isLinear(p unsafe.Pointer) bool

// linear returns b, or a copy of it in linear memory (see isLinear).
func linear(b []byte) []byte {
	if len(b) == 0 || isLinear(unsafe.Pointer(&b[0])) {
		return b
	}
	return append(make([]byte, 0, len(b)), b...)
}

// Type represents the JavaScript type of a Value.
type Type int

const (
	TypeUndefined Type = iota
	TypeNull
	TypeBoolean
	TypeNumber
	TypeString
	TypeSymbol
	TypeObject
	TypeFunction
)

func (t Type) String() string {
	switch t {
	case TypeUndefined:
		return "undefined"
	case TypeNull:
		return "null"
	case TypeBoolean:
		return "boolean"
	case TypeNumber:
		return "number"
	case TypeString:
		return "string"
	case TypeSymbol:
		return "symbol"
	case TypeObject:
		return "object"
	case TypeFunction:
		return "function"
	default:
		panic("bad type")
	}
}

func (t Type) isObject() bool {
	return t == TypeObject || t == TypeFunction
}

// Type returns the JavaScript type of the value v. It is similar to JavaScript's typeof operator,
// except that it returns TypeNull instead of TypeObject for null.
func (v Value) Type() Type {
	return v.typ
}

// Get returns the JavaScript property p of value v.
// It panics if v is not a JavaScript object.
func (v Value) Get(p string) Value {
	if vType := v.Type(); !vType.isObject() {
		panic(&ValueError{"Value.Get", vType})
	}
	pp, pn := str(p)
	return makeValue(valueGet(v.ext, pp, pn))
}

//go:wasmimport gojs syscall/js.valueGet
//go:noescape
func valueGet(v *externref, p unsafe.Pointer, n int) *externref

// Set sets the JavaScript property p of value v to ValueOf(x).
// It panics if v is not a JavaScript object.
func (v Value) Set(p string, x any) {
	if vType := v.Type(); !vType.isObject() {
		panic(&ValueError{"Value.Set", vType})
	}
	xe, xn := ValueOf(x).arg()
	pp, pn := str(p)
	valueSet(v.ext, pp, pn, xe, xn)
}

//go:wasmimport gojs syscall/js.valueSet
//go:noescape
func valueSet(v *externref, p unsafe.Pointer, n int, xe *externref, xn float64)

// Delete deletes the JavaScript property p of value v.
// It panics if v is not a JavaScript object.
func (v Value) Delete(p string) {
	if vType := v.Type(); !vType.isObject() {
		panic(&ValueError{"Value.Delete", vType})
	}
	pp, pn := str(p)
	valueDelete(v.ext, pp, pn)
}

//go:wasmimport gojs syscall/js.valueDelete
//go:noescape
func valueDelete(v *externref, p unsafe.Pointer, n int)

// Index returns JavaScript index i of value v.
// It panics if v is not a JavaScript object.
func (v Value) Index(i int) Value {
	if vType := v.Type(); !vType.isObject() {
		panic(&ValueError{"Value.Index", vType})
	}
	return makeValue(valueIndex(v.ext, i))
}

//go:wasmimport gojs syscall/js.valueIndex
func valueIndex(v *externref, i int) *externref

// SetIndex sets the JavaScript index i of value v to ValueOf(x).
// It panics if v is not a JavaScript object.
func (v Value) SetIndex(i int, x any) {
	if vType := v.Type(); !vType.isObject() {
		panic(&ValueError{"Value.SetIndex", vType})
	}
	xe, xn := ValueOf(x).arg()
	valueSetIndex(v.ext, i, xe, xn)
}

//go:wasmimport gojs syscall/js.valueSetIndex
func valueSetIndex(v *externref, i int, xe *externref, xn float64)

// makeArgSlice makes the slice that holds the numbers of the arguments
// of a call (see storeArgs). It is inlined, so that the slice is on the
// stack for small (<=16) numbers of args. (Unlike wasm_exec.js's
// syscall/js, there is no slice of the Values to keep alive during the
// call: the host has them from storeArgs on.)
func makeArgSlice(size int) (argNums []float64) {
	// value chosen for being power of two, and enough to handle all web APIs
	// in particular, note that WebGL2's texImage2D takes up to 10 arguments
	const maxStackArgs = 16
	if size <= maxStackArgs {
		// as long as makeArgSlice is inlined, this is stack-allocated
		argNums = make([]float64, size, maxStackArgs)
	} else {
		// allocates on the heap, but exceeding maxStackArgs should be rare
		argNums = make([]float64, size)
	}
	return
}

// storeArgs maps the arguments to Values and gives them to the host:
// a number in argNums, any other value as a reference, to the host,
// with NaN in argNums (in the order of the arguments).
func storeArgs(args []any, argNumsDst []float64) {
	for i, arg := range args {
		xe, xn := ValueOf(arg).arg()
		if xe != nil || xn != xn {
			valueArg(xe)
			xn = nan()
		}
		argNumsDst[i] = xn
	}
}

// valueArg gives the host the next argument of a call that is not a
// number (null for null).
//
//go:wasmimport gojs syscall/js.valueArg
func valueArg(x *externref)

// Length returns the JavaScript property "length" of v.
// It panics if v is not a JavaScript object.
func (v Value) Length() int {
	if vType := v.Type(); !vType.isObject() {
		panic(&ValueError{"Value.Length", vType})
	}
	return valueLength(v.ext)
}

//go:wasmimport gojs syscall/js.valueLength
func valueLength(v *externref) int

// Call does a JavaScript call to the method m of value v with the given arguments.
// It panics if v has no method m.
// The arguments get mapped to JavaScript values according to the ValueOf function.
func (v Value) Call(m string, args ...any) Value {
	argNums := makeArgSlice(len(args))
	storeArgs(args, argNums)
	mp, mn := str(m)
	res := makeValue(valueCall(v.ext, mp, mn, argNums))
	bad := threw() // (before yield, after which ret is another call's)
	yield()
	if bad {
		if vType := v.Type(); !vType.isObject() { // check here to avoid overhead in success case
			panic(&ValueError{"Value.Call", vType})
		}
		if propType := v.Get(m).Type(); propType != TypeFunction {
			panic("syscall/js: Value.Call: property " + m + " is not a function, got " + propType.String())
		}
		panic(Error{res})
	}
	return res
}

//go:wasmimport gojs syscall/js.valueCall
//go:noescape
func valueCall(v *externref, m unsafe.Pointer, n int, args []float64) *externref

// Invoke does a JavaScript call of the value v with the given arguments.
// It panics if v is not a JavaScript function.
// The arguments get mapped to JavaScript values according to the ValueOf function.
func (v Value) Invoke(args ...any) Value {
	argNums := makeArgSlice(len(args))
	storeArgs(args, argNums)
	xe, xn := v.arg()
	res := makeValue(valueInvoke(xe, xn, argNums))
	bad := threw() // (before yield, after which ret is another call's)
	yield()
	if bad {
		if vType := v.Type(); vType != TypeFunction { // check here to avoid overhead in success case
			panic(&ValueError{"Value.Invoke", vType})
		}
		panic(Error{res})
	}
	return res
}

//go:wasmimport gojs syscall/js.valueInvoke
//go:noescape
func valueInvoke(ve *externref, vn float64, args []float64) *externref

// New uses JavaScript's "new" operator with value v as constructor and the given arguments.
// It panics if v is not a JavaScript function.
// The arguments get mapped to JavaScript values according to the ValueOf function.
func (v Value) New(args ...any) Value {
	argNums := makeArgSlice(len(args))
	storeArgs(args, argNums)
	xe, xn := v.arg()
	res := makeValue(valueNew(xe, xn, argNums))
	bad := threw() // (before yield, after which ret is another call's)
	yield()
	if bad {
		if vType := v.Type(); vType != TypeFunction { // check here to avoid overhead in success case
			panic(&ValueError{"Value.New", vType})
		}
		panic(Error{res})
	}
	return res
}

//go:wasmimport gojs syscall/js.valueNew
//go:noescape
func valueNew(ve *externref, vn float64, args []float64) *externref

func (v Value) float(method string) float64 {
	if v.typ != TypeNumber {
		panic(&ValueError{method, v.Type()})
	}
	return v.num
}

// Float returns the value v as a float64.
// It panics if v is not a JavaScript number.
func (v Value) Float() float64 {
	return v.float("Value.Float")
}

// Int returns the value v truncated to an int.
// It panics if v is not a JavaScript number.
func (v Value) Int() int {
	return int(v.float("Value.Int"))
}

// Bool returns the value v as a bool.
// It panics if v is not a JavaScript boolean.
func (v Value) Bool() bool {
	if v.typ != TypeBoolean {
		panic(&ValueError{"Value.Bool", v.Type()})
	}
	return v.num != 0
}

// Truthy returns the JavaScript "truthiness" of the value v. In JavaScript,
// false, 0, "", null, undefined, and NaN are "falsy", and everything else is
// "truthy". See https://developer.mozilla.org/en-US/docs/Glossary/Truthy.
func (v Value) Truthy() bool {
	switch v.Type() {
	case TypeUndefined, TypeNull:
		return false
	case TypeBoolean:
		return v.Bool()
	case TypeNumber:
		return v.num == v.num && v.num != 0
	case TypeString:
		return v.String() != ""
	case TypeSymbol, TypeFunction, TypeObject:
		return true
	default:
		panic("bad type")
	}
}

// String returns the value v as a string.
// String is a special case because of Go's String method convention. Unlike the other getters,
// it does not panic if v's Type is not TypeString. Instead, it returns a string of the form "<T>"
// or "<T: V>" where T is v's type and V is a string representation of v's value.
func (v Value) String() string {
	switch v.Type() {
	case TypeString:
		return jsString(v)
	case TypeUndefined:
		return "<undefined>"
	case TypeNull:
		return "<null>"
	case TypeBoolean:
		return "<boolean: " + jsString(v) + ">"
	case TypeNumber:
		return "<number: " + jsString(v) + ">"
	case TypeSymbol:
		return "<symbol>"
	case TypeObject:
		return "<object>"
	case TypeFunction:
		return "<function>"
	default:
		panic("bad type")
	}
}

func jsString(v Value) string {
	xe, xn := v.arg()
	b := make([]byte, valuePrepareString(xe, xn))
	valueLoadString(b)
	return string(b)
}

// valuePrepareString converts the value to a string, which the host
// keeps for valueLoadString, and returns its length in bytes (UTF-8).
//
//go:wasmimport gojs syscall/js.valuePrepareString
func valuePrepareString(xe *externref, xn float64) int

//go:wasmimport gojs syscall/js.valueLoadString
//go:noescape
func valueLoadString(b []byte)

// InstanceOf reports whether v is an instance of type t according to JavaScript's instanceof operator.
func (v Value) InstanceOf(t Value) bool {
	ve, vn := v.arg()
	te, tn := t.arg()
	return valueInstanceOf(ve, vn, te, tn)
}

//go:wasmimport gojs syscall/js.valueInstanceOf
func valueInstanceOf(ve *externref, vn float64, te *externref, tn float64) bool

// A ValueError occurs when a Value method is invoked on
// a Value that does not support it. Such cases are documented
// in the description of each method.
type ValueError struct {
	Method string
	Type   Type
}

func (e *ValueError) Error() string {
	return "syscall/js: call of " + e.Method + " on " + e.Type.String()
}

// CopyBytesToGo copies bytes from src to dst.
// It panics if src is not a Uint8Array or Uint8ClampedArray.
// It returns the number of bytes copied, which will be the minimum of the lengths of src and dst.
func CopyBytesToGo(dst []byte, src Value) int {
	buf := linear(dst)
	n := copyBytesToGo(buf, src.ext)
	if n > 0 && &buf[0] != &dst[0] {
		copy(dst, buf[:n])
	}
	if n < 0 {
		panic("syscall/js: CopyBytesToGo: expected src to be a Uint8Array or Uint8ClampedArray")
	}
	return n
}

// copyBytesToGo returns the number of bytes copied, or -1 if src is
// not a Uint8Array or a Uint8ClampedArray.
//
//go:wasmimport gojs syscall/js.copyBytesToGo
//go:noescape
func copyBytesToGo(dst []byte, src *externref) int

// CopyBytesToJS copies bytes from src to dst.
// It panics if dst is not a Uint8Array or Uint8ClampedArray.
// It returns the number of bytes copied, which will be the minimum of the lengths of src and dst.
func CopyBytesToJS(dst Value, src []byte) int {
	n := copyBytesToJS(dst.ext, linear(src))
	if n < 0 {
		panic("syscall/js: CopyBytesToJS: expected dst to be a Uint8Array or Uint8ClampedArray")
	}
	return n
}

//go:wasmimport gojs syscall/js.copyBytesToJS
//go:noescape
func copyBytesToJS(dst *externref, src []byte) int
