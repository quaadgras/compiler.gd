// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

import (
	"math/bits"
)

// StringHash is the fork's canonical string hash — a wyhash-inspired
// mixer with hardcoded keys so every producer (runtime and compile
// time alike) computes the same value for the same bytes. The gd
// fork caches this value in stringStruct word 1 at construction and
// reads it back on map lookup, trading per-map DoS scrambling for
// cache consistency across map instances (see doc/gd/sso-string.md).
//
// The algorithm mirrors runtime.memhashFallback (hash64.go) but with
// the [4]uintptr key baked in as compile-time constants instead of
// being sourced from runtime.hashkey. Inputs with fewer than 16
// bytes have inline-rep handled elsewhere, so this function is only
// used for heap-rep strings on 64-bit targets.
//
// Keep this identical to runtime.sealStringHash and
// runtime.strhashFallback — any drift breaks the cache.
const (
	StringHashM5 uint64 = 0x1d8e4e27c47d124f
	StringHashK0 uint64 = 0x243f6a8885a308d3
	StringHashK1 uint64 = 0x13198a2e03707344
	StringHashK2 uint64 = 0xa4093822299f31d0
	StringHashK3 uint64 = 0x082efa98ec4e6c89
)

// StringHashBytes returns the cached hash for the byte sequence b.
// The returned value is never zero — 0 is reserved as the "not
// populated" sentinel in stringStruct.hash; true-zero outputs are
// bumped to 1.
func StringHashBytes(b []byte) uint64 {
	s := uint64(len(b))
	seed := StringHashK0
	var a, v uint64
	switch {
	case s == 0:
		if seed == 0 {
			return 1
		}
		return seed
	case s < 4:
		a = uint64(b[0])
		a |= uint64(b[s>>1]) << 8
		a |= uint64(b[s-1]) << 16
	case s == 4:
		a = stringHashR4(b)
		v = a
	case s < 8:
		a = stringHashR4(b)
		v = stringHashR4(b[s-4:])
	case s == 8:
		a = stringHashR8(b)
		v = a
	case s <= 16:
		a = stringHashR8(b)
		v = stringHashR8(b[s-8:])
	default:
		// s > 16. Track offset p into the original slice so we can
		// reference b[s-16:] / b[s-8:] at the end — matching
		// runtime.memhashFallback's pointer-wrap semantics in a slice-
		// safe way.
		l := s
		p := uint64(0)
		if l > 48 {
			seed1 := seed
			seed2 := seed
			for ; l > 48; l -= 48 {
				seed = stringHashMix(stringHashR8(b[p:])^StringHashK1, stringHashR8(b[p+8:])^seed)
				seed1 = stringHashMix(stringHashR8(b[p+16:])^StringHashK2, stringHashR8(b[p+24:])^seed1)
				seed2 = stringHashMix(stringHashR8(b[p+32:])^StringHashK3, stringHashR8(b[p+40:])^seed2)
				p += 48
			}
			seed ^= seed1 ^ seed2
		}
		for ; l > 16; l -= 16 {
			seed = stringHashMix(stringHashR8(b[p:])^StringHashK1, stringHashR8(b[p+8:])^seed)
			p += 16
		}
		a = stringHashR8(b[s-16:])
		v = stringHashR8(b[s-8:])
	}
	h := stringHashMix(StringHashM5^s, stringHashMix(a^StringHashK1, v^seed))
	if h == 0 {
		return 1
	}
	return h
}

func stringHashMix(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

func stringHashR4(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
}

func stringHashR8(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}
