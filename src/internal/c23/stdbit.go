// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "math/bits"

// C23's <stdbit.h>: each function for each unsigned type, of its width w
// (Ul32 is unsigned long's where long has 32 bits, passed as uint64).
// cmd/cc's header chooses one by type for the type-generic macros.

func lzw(x uint64, w int) uint32 { return uint32(bits.LeadingZeros64(x) - (64 - w)) }

func tzw(x uint64, w int) uint32 {
	if x == 0 {
		return uint32(w)
	}
	return uint32(bits.TrailingZeros64(x))
}

func maskw(w int) uint64 { return 1<<w - 1 }

func Stdbit(f int, x uint64, w int) uint64 {
	x &= maskw(w)
	all := x == maskw(w)
	switch f {
	case 0: // leading_zeros
		return uint64(lzw(x, w))
	case 1: // leading_ones
		return uint64(lzw(^x&maskw(w), w))
	case 2: // trailing_zeros
		return uint64(tzw(x, w))
	case 3: // trailing_ones
		return uint64(tzw(^x&maskw(w), w))
	case 4: // first_leading_zero
		if all {
			return 0
		}
		return uint64(lzw(^x&maskw(w), w)) + 1
	case 5: // first_leading_one
		if x == 0 {
			return 0
		}
		return uint64(lzw(x, w)) + 1
	case 6: // first_trailing_zero
		if all {
			return 0
		}
		return uint64(tzw(^x&maskw(w), w)) + 1
	case 7: // first_trailing_one
		if x == 0 {
			return 0
		}
		return uint64(tzw(x, w)) + 1
	case 8: // count_zeros
		return uint64(w - bits.OnesCount64(x))
	case 9: // count_ones
		return uint64(bits.OnesCount64(x))
	case 10: // has_single_bit
		if bits.OnesCount64(x) == 1 {
			return 1
		}
		return 0
	case 11: // bit_width
		return uint64(w - int(lzw(x, w)))
	case 12: // bit_floor
		if x == 0 {
			return 0
		}
		return 1 << (w - int(lzw(x, w)) - 1)
	default: // bit_ceil: 0 if it does not fit
		if x <= 1 {
			return 1
		}
		n := w - int(lzw(x-1, w))
		if n >= w {
			return 0
		}
		return 1 << n
	}
}

func StdcLeadingZerosUl32(x uint64) uint32 { return uint32(Stdbit(0, uint64(x), 32)) }

func StdcLeadingOnesUl32(x uint64) uint32 { return uint32(Stdbit(1, uint64(x), 32)) }

func StdcTrailingZerosUl32(x uint64) uint32 { return uint32(Stdbit(2, uint64(x), 32)) }

func StdcTrailingOnesUl32(x uint64) uint32 { return uint32(Stdbit(3, uint64(x), 32)) }

func StdcFirstLeadingZeroUl32(x uint64) uint32 { return uint32(Stdbit(4, uint64(x), 32)) }

func StdcFirstLeadingOneUl32(x uint64) uint32 { return uint32(Stdbit(5, uint64(x), 32)) }

func StdcFirstTrailingZeroUl32(x uint64) uint32 { return uint32(Stdbit(6, uint64(x), 32)) }

func StdcFirstTrailingOneUl32(x uint64) uint32 { return uint32(Stdbit(7, uint64(x), 32)) }

func StdcCountZerosUl32(x uint64) uint32 { return uint32(Stdbit(8, uint64(x), 32)) }

func StdcCountOnesUl32(x uint64) uint32 { return uint32(Stdbit(9, uint64(x), 32)) }

func StdcHasSingleBitUl32(x uint64) uint8 { return uint8(Stdbit(10, uint64(x), 32)) }

func StdcBitWidthUl32(x uint64) uint32 { return uint32(Stdbit(11, uint64(x), 32)) }

func StdcBitFloorUl32(x uint64) uint64 { return uint64(Stdbit(12, uint64(x), 32)) }

func StdcBitCeilUl32(x uint64) uint64 { return uint64(Stdbit(13, uint64(x), 32)) }
