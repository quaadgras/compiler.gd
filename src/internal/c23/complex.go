// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import ()

// C's complex types, as Go's: float _Complex is complex64, double and
// long double _Complex complex128 (long double's in x86-64's 32 bytes).

func LoadC64(p *Void) complex64 {
	return complex(LoadF32(p), LoadF32(Add(p, 4)))
}

func LoadC128(p *Void) complex128 {
	return complex(LoadF64(p), LoadF64(Add(p, 8)))
}

func LoadCL(p *Void) complex128 {
	return complex(LoadF64(p), LoadF64(Add(p, 16)))
}

func StoreC64(p *Void, v complex64) {
	StoreF32(p, real(v))
	StoreF32(Add(p, 4), imag(v))
}

func StoreC128(p *Void, v complex128) {
	StoreF64(p, real(v))
	StoreF64(Add(p, 8), imag(v))
}

func StoreCL(p *Void, v complex128) {
	StoreF64(p, real(v))
	StoreF64(Add(p, 16), imag(v))
}

// complex.h
