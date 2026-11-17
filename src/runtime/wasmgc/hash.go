// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"unsafe"
)

// Hashes of values through pointers, for the type descriptors and the
// functions the compiler generates.

const (
	hashMul = 0x9E3779B97F4A7C15
)

func mix(h, v uint64) uint64 {
	h = (h ^ v) * hashMul
	return h ^ h>>29
}

// memhash hashes the n bytes at p.
func memhash(p unsafe.Pointer, seed, n uintptr) uintptr {
	h := uint64(seed) ^ uint64(n)*hashMul
	if a := trylinaddr(p); a != 0 {
		i := uintptr(0)
		for ; i+8 <= n; i += 8 {
			h = mix(h, linload64(a+i))
		}
		// The rest as one word, as below: the hash of a value must
		// not depend on where it is.
		if i < n {
			var v uint64
			for j := i; j < n; j++ {
				v |= uint64(linload8(a+j)) << (8 * (j - i))
			}
			h = mix(h, v)
		}
		return uintptr(mix(h, 0))
	}
	// Not memory: a byte at a time, through the accessors.
	var v uint64
	for i := uintptr(0); i < n; i++ {
		v |= uint64(*(*byte)(add(p, i))) << (8 * (i & 7))
		if i&7 == 7 || i == n-1 {
			h = mix(h, v)
			v = 0
		}
	}
	return uintptr(mix(h, 0))
}

func memhash0(p unsafe.Pointer, h uintptr) uintptr   { return h }
func memhash8(p unsafe.Pointer, h uintptr) uintptr   { return memhash(p, h, 1) }
func memhash16(p unsafe.Pointer, h uintptr) uintptr  { return memhash(p, h, 2) }
func memhash32(p unsafe.Pointer, h uintptr) uintptr  { return memhash(p, h, 4) }
func memhash64(p unsafe.Pointer, h uintptr) uintptr  { return memhash(p, h, 8) }
func memhash128(p unsafe.Pointer, h uintptr) uintptr { return memhash(p, h, 16) }

func strhash(p unsafe.Pointer, seed uintptr) uintptr {
	s := *(*string)(p)
	h := uint64(seed) ^ uint64(len(s))*hashMul
	for i := 0; i < len(s); i++ {
		h = mix(h, uint64(s[i]))
	}
	return uintptr(mix(h, 0))
}

func f32hash(p unsafe.Pointer, h uintptr) uintptr {
	f := *(*float32)(p)
	if f == 0 {
		return uintptr(mix(uint64(h), 0)) // +0, -0
	}
	if f != f {
		return uintptr(mix(uint64(h), rand())) // any hash: a NaN is equal to nothing
	}
	return uintptr(mix(uint64(h), float64bits(float64(f))))
}

func f64hash(p unsafe.Pointer, h uintptr) uintptr {
	f := *(*float64)(p)
	if f == 0 {
		return uintptr(mix(uint64(h), 0)) // +0, -0
	}
	if f != f {
		return uintptr(mix(uint64(h), rand())) // any hash: a NaN is equal to nothing
	}
	return uintptr(mix(uint64(h), float64bits(f)))
}

func c64hash(p unsafe.Pointer, h uintptr) uintptr {
	return f32hash(add(p, 4), f32hash(p, h))
}

func c128hash(p unsafe.Pointer, h uintptr) uintptr {
	return f64hash(add(p, 8), f64hash(p, h))
}

func interhash(p unsafe.Pointer, h uintptr) uintptr {
	x := (*iface)(p)
	if x.tab == 0 {
		return h
	}
	return ifaceHash((*itab)(unsafe.Pointer(x.tab)).Type, (*eface)(p), h)
}

func nilinterhash(p unsafe.Pointer, h uintptr) uintptr {
	x := (*eface)(p)
	if x._type == 0 {
		return h
	}
	return ifaceHash((*_type)(unsafe.Pointer(x._type)), x, h)
}

// ifaceHash hashes an interface value of dynamic type t.
func ifaceHash(t *_type, x *eface, h uintptr) uintptr {
	if t.Equal == nil && !structComparable(t) {
		panic(errorString("hash of unhashable type " + toRType(t).string()))
	}
	switch {
	case t.IsDirectIface():
		return typehash(t, unsafe.Pointer(&spread{p: x.data}), h)
	case t.IsInlineIface():
		xb := [2]uint64{float64bits(x.w0), float64bits(x.w1)}
		return typehash(t, noescape(unsafe.Pointer(&xb)), h)
	case t.IsSpreadIface():
		if goarch.PtrSize == 4 {
			// (A string, held as word 0 and its 16 bytes.)
			var s string
			abi.UnspreadString((*string)(noescape(unsafe.Pointer(&s))), x.data, &[2]uint64{float64bits(x.w0), float64bits(x.w1)})
			return typehash(t, noescape(unsafe.Pointer(&s)), h)
		}
		return typehash(t, unsafe.Pointer(&spread{x.data, float64bits(x.w0), float64bits(x.w1)}), h)
	}
	return typehash(t, x.data, h)
}

// typehash hashes the value of type t at p.
//
// TODO: by the kind of t, as the compiler's generated functions do: a
// struct with a string or a float is not its bytes.
func typehash(t *_type, p unsafe.Pointer, h uintptr) uintptr {
	if t.TFlag&abi.TFlagRegularMemory != 0 {
		return memhash(p, h, t.Size_)
	}
	switch t.Kind() {
	case abi.Float32:
		return f32hash(p, h)
	case abi.Float64:
		return f64hash(p, h)
	case abi.Complex64:
		return c64hash(p, h)
	case abi.Complex128:
		return c128hash(p, h)
	case abi.String:
		return strhash(p, h)
	case abi.Interface:
		if len((*interfacetype)(unsafe.Pointer(t)).Methods) == 0 {
			return nilinterhash(p, h)
		}
		return interhash(p, h)
	case abi.Array:
		a := (*abi.ArrayType)(unsafe.Pointer(t))
		for i := uintptr(0); i < a.Len; i++ {
			h = typehash(a.Elem, add(p, i*a.Elem.Size_), h)
		}
		return h
	case abi.Struct:
		st := (*abi.StructType)(unsafe.Pointer(t))
		for i := range st.Fields {
			f := &st.Fields[i]
			if f.Name.IsBlank() {
				continue
			}
			h = typehash(f.Typ, add(p, f.Offset), h)
		}
		return h
	}
	panic(errorString("hash of unhashable type " + toRType(t).string()))
}
