// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/bytealg"
	"internal/runtime/sys"
	"unsafe"
)

// Error identifies a runtime error used in panic.
//
// The Go runtime triggers panics for a variety of cases, as described by the
// Go Language Spec, such as out-of-bounds slice/array access, close of nil
// channels, type assertion failures, etc.
//
// When these cases occur, the Go runtime panics with an error that implements
// Error. This can be useful when recovering from panics to distinguish between
// custom application panics and fundamental runtime panics.
//
// Packages outside of the Go standard library should not implement Error.
type Error interface {
	error

	// RuntimeError is a no-op function but
	// serves to distinguish types that are runtime
	// errors from ordinary errors: a type is a
	// runtime error if it has a RuntimeError method.
	RuntimeError()
}

// A TypeAssertionError explains a failed type assertion.
type TypeAssertionError struct {
	_interface    *_type
	concrete      *_type
	asserted      *_type
	missingMethod string // one method needed by Interface, missing from Concrete
}

func (*TypeAssertionError) RuntimeError() {}

func (e *TypeAssertionError) Error() string {
	inter := "interface"
	if e._interface != nil {
		inter = toRType(e._interface).string()
	}
	as := toRType(e.asserted).string()
	if e.concrete == nil {
		return "interface conversion: " + inter + " is nil, not " + as
	}
	cs := toRType(e.concrete).string()
	if e.missingMethod == "" {
		msg := "interface conversion: " + inter + " is " + cs + ", not " + as
		if cs == as {
			// provide slightly clearer error message
			if toRType(e.concrete).pkgpath() != toRType(e.asserted).pkgpath() {
				msg += " (types from different packages)"
			} else {
				msg += " (types from different scopes)"
			}
		}
		return msg
	}
	return "interface conversion: " + cs + " is not " + as +
		": missing method " + e.missingMethod
}

// itoa converts val to a decimal representation. The result is
// written somewhere within buf and the location of the result is returned.
// buf must be at least 20 bytes.
//
//go:nosplit
func itoa(buf []byte, val uint64) []byte {
	i := len(buf) - 1
	for val >= 10 {
		buf[i] = byte(val%10 + '0')
		i--
		val /= 10
	}
	buf[i] = byte(val + '0')
	return buf[i:]
}

// An errorString represents a runtime error described by a single string.
type errorString string

func (e errorString) RuntimeError() {}

func (e errorString) Error() string {
	return "runtime error: " + string(e)
}

type errorAddressString struct {
	msg  string  // error message
	addr uintptr // memory address where the error occurred
}

func (e errorAddressString) RuntimeError() {}

func (e errorAddressString) Error() string {
	return "runtime error: " + e.msg
}

var _ error = errorAddressString{}

// Addr returns the memory address where a fault occurred.
// The address provided is best-effort.
// The veracity of the result may depend on the platform.
// Errors providing this method will only be returned as
// a result of using [runtime/debug.SetPanicOnFault].
func (e errorAddressString) Addr() uintptr {
	return e.addr
}

// plainError represents a runtime error described a string without
// the prefix "runtime error: " after invoking errorString.Error().
// See Issue #14965.
type plainError string

func (e plainError) RuntimeError() {}

func (e plainError) Error() string {
	return string(e)
}

var _ error = plainError("")

// A boundsError represents an indexing or slicing operation gone wrong.
type boundsError struct {
	x int64
	y int
	// Values in an index or slice expression can be signed or unsigned.
	// That means we'd need 65 bits to encode all possible indexes, from -2^63 to 2^64-1.
	// Instead, we keep track of whether x should be interpreted as signed or unsigned.
	// y is known to be nonnegative and to fit in an int.
	signed bool
	code   abi.BoundsErrorCode
}

var _ error = boundsError{}

// boundsErrorFmts provide error text for various out-of-bounds panics.
// Note: if you change these strings, you should adjust the size of the buffer
// in boundsError.Error below as well.
var boundsErrorFmts = [...]string{
	abi.BoundsIndex:      "index out of range [%x] with length %y",
	abi.BoundsSliceAlen:  "slice bounds out of range [:%x] with length %y",
	abi.BoundsSliceAcap:  "slice bounds out of range [:%x] with capacity %y",
	abi.BoundsSliceB:     "slice bounds out of range [%x:%y]",
	abi.BoundsSlice3Alen: "slice bounds out of range [::%x] with length %y",
	abi.BoundsSlice3Acap: "slice bounds out of range [::%x] with capacity %y",
	abi.BoundsSlice3B:    "slice bounds out of range [:%x:%y]",
	abi.BoundsSlice3C:    "slice bounds out of range [%x:%y:]",
	abi.BoundsConvert:    "cannot convert slice with length %y to array or pointer to array with length %x",
}

// boundsNegErrorFmts are overriding formats if x is negative. In this case there's no need to report y.
var boundsNegErrorFmts = [...]string{
	abi.BoundsIndex:      "index out of range [%x]",
	abi.BoundsSliceAlen:  "slice bounds out of range [:%x]",
	abi.BoundsSliceAcap:  "slice bounds out of range [:%x]",
	abi.BoundsSliceB:     "slice bounds out of range [%x:]",
	abi.BoundsSlice3Alen: "slice bounds out of range [::%x]",
	abi.BoundsSlice3Acap: "slice bounds out of range [::%x]",
	abi.BoundsSlice3B:    "slice bounds out of range [:%x:]",
	abi.BoundsSlice3C:    "slice bounds out of range [%x::]",
}

func (e boundsError) RuntimeError() {}

func appendIntStr(b []byte, v int64, signed bool) []byte {
	if signed && v < 0 {
		b = append(b, '-')
		v = -v
	}
	var buf [20]byte
	b = append(b, itoa(buf[:], uint64(v))...)
	return b
}

func (e boundsError) Error() string {
	fmt := boundsErrorFmts[e.code]
	if e.signed && e.x < 0 {
		fmt = boundsNegErrorFmts[e.code]
	}
	// max message length is 99: "runtime error: slice bounds out of range [::%x] with capacity %y"
	// x can be at most 20 characters. y can be at most 19.
	b := make([]byte, 0, 100)
	b = append(b, "runtime error: "...)
	for i := 0; i < len(fmt); i++ {
		c := fmt[i]
		if c != '%' {
			b = append(b, c)
			continue
		}
		i++
		switch fmt[i] {
		case 'x':
			b = appendIntStr(b, e.x, e.signed)
		case 'y':
			b = appendIntStr(b, int64(e.y), true)
		}
	}
	return string(b)
}

type stringer interface {
	String() string
}

// printpanicval prints an argument passed to panic.
// If panic is called with a value that has a String or Error method,
// it has already been converted into a string by preprintpanics.
//
// To ensure that the traceback can be unambiguously parsed even when
// the panic value contains "\ngoroutine" and other stack-like
// strings, newlines in the string representation of v are replaced by
// "\n\t".
func printpanicval(v any) {
	switch v := v.(type) {
	case nil:
		print("nil")
	case bool:
		print(v)
	case int:
		print(v)
	case int8:
		print(v)
	case int16:
		print(v)
	case int32:
		print(v)
	case int64:
		print(v)
	case uint:
		print(v)
	case uint8:
		print(v)
	case uint16:
		print(v)
	case uint32:
		print(v)
	case uint64:
		print(v)
	case uintptr:
		print(v)
	case float32:
		print(v)
	case float64:
		print(v)
	case complex64:
		print(v)
	case complex128:
		print(v)
	case string:
		printindented(v)
	default:
		printanycustomtype(v)
	}
}

// Invariant: each newline in the string representation is followed by a tab.
func printanycustomtype(i any) {
	eface := efaceOf(&i)
	typestring := toRType(eface._type).string()

	// gd fat-iface: for inline-iface types the payload lives in eface.inline,
	// not behind eface.data (which is nil). Pick the right source pointer.
	p := eface.data
	if eface._type.IsInlineIface() {
		p = unsafe.Pointer(&eface.inline)
	}
	// gd Phase D: spread types (string, slice) split their 3-word value
	// across eface.data (word 0) + eface.inline (words 1+2). Reassemble
	// into a stack-local header so *(*T)(p) below reads the full value.
	var spreadBuf [3]uintptr
	if eface._type.IsSpreadIface() {
		spreadBuf[0] = uintptr(eface.data)
		*(*[16]byte)(unsafe.Pointer(&spreadBuf[1])) = *(*[16]byte)(unsafe.Pointer(&eface.inline))
		p = noescape(unsafe.Pointer(&spreadBuf))
	}

	switch eface._type.Kind() {
	case abi.String:
		print(typestring, `("`)
		printindented(*(*string)(p))
		print(`")`)
	case abi.Bool:
		print(typestring, "(", *(*bool)(p), ")")
	case abi.Int:
		print(typestring, "(", *(*int)(p), ")")
	case abi.Int8:
		print(typestring, "(", *(*int8)(p), ")")
	case abi.Int16:
		print(typestring, "(", *(*int16)(p), ")")
	case abi.Int32:
		print(typestring, "(", *(*int32)(p), ")")
	case abi.Int64:
		print(typestring, "(", *(*int64)(p), ")")
	case abi.Uint:
		print(typestring, "(", *(*uint)(p), ")")
	case abi.Uint8:
		print(typestring, "(", *(*uint8)(p), ")")
	case abi.Uint16:
		print(typestring, "(", *(*uint16)(p), ")")
	case abi.Uint32:
		print(typestring, "(", *(*uint32)(p), ")")
	case abi.Uint64:
		print(typestring, "(", *(*uint64)(p), ")")
	case abi.Uintptr:
		print(typestring, "(", *(*uintptr)(p), ")")
	case abi.Float32:
		print(typestring, "(", *(*float32)(p), ")")
	case abi.Float64:
		print(typestring, "(", *(*float64)(p), ")")
	case abi.Complex64:
		print(typestring, *(*complex64)(p))
	case abi.Complex128:
		print(typestring, *(*complex128)(p))
	default:
		print("(", typestring, ") ", eface.data)
	}
}

// printindented prints s, replacing "\n" with "\n\t".
func printindented(s string) {
	for {
		i := bytealg.IndexByteString(s, '\n')
		if i < 0 {
			break
		}
		i += len("\n")
		print(s[:i])
		print("\t")
		s = s[i:]
	}
	print(s)
}

// panicwrap generates a panic for a call to a wrapped value method
// with a nil pointer receiver.
//
// It is called from the generated wrapper code.
func panicwrap() {
	pc := sys.GetCallerPC()
	name := funcNameForPrint(funcname(findfunc(pc)))
	// name is something like "main.(*T).F".
	// We want to extract pkg ("main"), typ ("T"), and meth ("F").
	// Do it by finding the parens.
	i := bytealg.IndexByteString(name, '(')
	if i < 0 {
		throw("panicwrap: no ( in " + name)
	}
	pkg := name[:i-1]
	if i+2 >= len(name) || name[i-1:i+2] != ".(*" {
		throw("panicwrap: unexpected string after package name: " + name)
	}
	name = name[i+2:]
	i = bytealg.IndexByteString(name, ')')
	if i < 0 {
		throw("panicwrap: no ) in " + name)
	}
	if i+2 >= len(name) || name[i:i+2] != ")." {
		throw("panicwrap: unexpected string after type name: " + name)
	}
	typ := name[:i]
	meth := name[i+2:]
	panic(plainError("value method " + pkg + "." + typ + "." + meth + " called using nil *" + typ + " pointer"))
}
