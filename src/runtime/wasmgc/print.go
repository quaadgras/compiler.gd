// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/strconv"
	"unsafe"
)

//go:wasmimport gd putc
func putc(c uint32)

//go:wasmimport gd nanotime
func nanotime() int64

func printlock()   {}
func printunlock() {}

func printsp() { putc(' ') }
func printnl() { putc('\n') }

func printstring(s string) {
	for i := 0; i < len(s); i++ {
		putc(uint32(s[i]))
	}
}

func printbool(v bool) {
	if v {
		printstring("true")
	} else {
		printstring("false")
	}
}

func printuint(v uint64) {
	var buf [24]byte
	i := len(buf)
	for i--; i > 0; i-- {
		buf[i] = byte(v%10 + '0')
		if v < 10 {
			break
		}
		v /= 10
	}
	for ; i < len(buf); i++ {
		putc(uint32(buf[i]))
	}
}

func printint(v int64) {
	if v < 0 {
		putc('-')
		v = -v
	}
	printuint(uint64(v))
}

func gwrite(b []byte) {
	for _, c := range b {
		putc(uint32(c))
	}
}

// float64 requires 1+17+1+1+1+3 = 24 bytes max (sign+digits+decimal point+e+sign+exponent digits).
const float64Bytes = 24

func printfloat64(v float64) {
	var buf [float64Bytes]byte
	gwrite(strconv.AppendFloat(buf[:0], v, 'g', -1, 64))
}

// float32 requires 1+9+1+1+1+2 = 15 bytes max (sign+digits+decimal point+e+sign+exponent digits).
const float32Bytes = 15

func printfloat32(v float32) {
	var buf [float32Bytes]byte
	gwrite(strconv.AppendFloat(buf[:0], float64(v), 'g', -1, 32))
}

func printcomplex128(c complex128) {
	var buf [2*float64Bytes + 3]byte
	gwrite(strconv.AppendComplex(buf[:0], c, 'g', -1, 128))
}

func printcomplex64(c complex64) {
	var buf [2*float32Bytes + 3]byte
	gwrite(strconv.AppendComplex(buf[:0], complex128(c), 'g', -1, 64))
}

func printhex(v uint64) {
	const dig = "0123456789abcdef"
	var buf [100]byte
	i := len(buf)
	for i--; i > 0; i-- {
		buf[i] = dig[v%16]
		if v < 16 {
			break
		}
		v /= 16
	}
	i--
	buf[i] = 'x'
	i--
	buf[i] = '0'
	gwrite(buf[i:])
}

func printpointer(p unsafe.Pointer) {
	// TODO: the identity of an object.
	printhex(uint64(trylinaddr(p)))
}
func printuintptr(v uintptr) { printuint(uint64(v)) }
