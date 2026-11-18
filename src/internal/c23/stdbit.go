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

func stdbit(f int, x uint64, w int) uint64 {
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

func StdcLeadingZerosUc(x uint8) uint32         { return uint32(stdbit(0, uint64(x), 8)) }
func StdcLeadingZerosUs(x uint16) uint32        { return uint32(stdbit(0, uint64(x), 16)) }
func StdcLeadingZerosUi(x uint32) uint32        { return uint32(stdbit(0, uint64(x), 32)) }
func StdcLeadingZerosUl(x uint64) uint32        { return uint32(stdbit(0, uint64(x), 64)) }
func StdcLeadingZerosUl32(x uint64) uint32      { return uint32(stdbit(0, uint64(x), 32)) }
func StdcLeadingOnesUc(x uint8) uint32          { return uint32(stdbit(1, uint64(x), 8)) }
func StdcLeadingOnesUs(x uint16) uint32         { return uint32(stdbit(1, uint64(x), 16)) }
func StdcLeadingOnesUi(x uint32) uint32         { return uint32(stdbit(1, uint64(x), 32)) }
func StdcLeadingOnesUl(x uint64) uint32         { return uint32(stdbit(1, uint64(x), 64)) }
func StdcLeadingOnesUl32(x uint64) uint32       { return uint32(stdbit(1, uint64(x), 32)) }
func StdcTrailingZerosUc(x uint8) uint32        { return uint32(stdbit(2, uint64(x), 8)) }
func StdcTrailingZerosUs(x uint16) uint32       { return uint32(stdbit(2, uint64(x), 16)) }
func StdcTrailingZerosUi(x uint32) uint32       { return uint32(stdbit(2, uint64(x), 32)) }
func StdcTrailingZerosUl(x uint64) uint32       { return uint32(stdbit(2, uint64(x), 64)) }
func StdcTrailingZerosUl32(x uint64) uint32     { return uint32(stdbit(2, uint64(x), 32)) }
func StdcTrailingOnesUc(x uint8) uint32         { return uint32(stdbit(3, uint64(x), 8)) }
func StdcTrailingOnesUs(x uint16) uint32        { return uint32(stdbit(3, uint64(x), 16)) }
func StdcTrailingOnesUi(x uint32) uint32        { return uint32(stdbit(3, uint64(x), 32)) }
func StdcTrailingOnesUl(x uint64) uint32        { return uint32(stdbit(3, uint64(x), 64)) }
func StdcTrailingOnesUl32(x uint64) uint32      { return uint32(stdbit(3, uint64(x), 32)) }
func StdcFirstLeadingZeroUc(x uint8) uint32     { return uint32(stdbit(4, uint64(x), 8)) }
func StdcFirstLeadingZeroUs(x uint16) uint32    { return uint32(stdbit(4, uint64(x), 16)) }
func StdcFirstLeadingZeroUi(x uint32) uint32    { return uint32(stdbit(4, uint64(x), 32)) }
func StdcFirstLeadingZeroUl(x uint64) uint32    { return uint32(stdbit(4, uint64(x), 64)) }
func StdcFirstLeadingZeroUl32(x uint64) uint32  { return uint32(stdbit(4, uint64(x), 32)) }
func StdcFirstLeadingOneUc(x uint8) uint32      { return uint32(stdbit(5, uint64(x), 8)) }
func StdcFirstLeadingOneUs(x uint16) uint32     { return uint32(stdbit(5, uint64(x), 16)) }
func StdcFirstLeadingOneUi(x uint32) uint32     { return uint32(stdbit(5, uint64(x), 32)) }
func StdcFirstLeadingOneUl(x uint64) uint32     { return uint32(stdbit(5, uint64(x), 64)) }
func StdcFirstLeadingOneUl32(x uint64) uint32   { return uint32(stdbit(5, uint64(x), 32)) }
func StdcFirstTrailingZeroUc(x uint8) uint32    { return uint32(stdbit(6, uint64(x), 8)) }
func StdcFirstTrailingZeroUs(x uint16) uint32   { return uint32(stdbit(6, uint64(x), 16)) }
func StdcFirstTrailingZeroUi(x uint32) uint32   { return uint32(stdbit(6, uint64(x), 32)) }
func StdcFirstTrailingZeroUl(x uint64) uint32   { return uint32(stdbit(6, uint64(x), 64)) }
func StdcFirstTrailingZeroUl32(x uint64) uint32 { return uint32(stdbit(6, uint64(x), 32)) }
func StdcFirstTrailingOneUc(x uint8) uint32     { return uint32(stdbit(7, uint64(x), 8)) }
func StdcFirstTrailingOneUs(x uint16) uint32    { return uint32(stdbit(7, uint64(x), 16)) }
func StdcFirstTrailingOneUi(x uint32) uint32    { return uint32(stdbit(7, uint64(x), 32)) }
func StdcFirstTrailingOneUl(x uint64) uint32    { return uint32(stdbit(7, uint64(x), 64)) }
func StdcFirstTrailingOneUl32(x uint64) uint32  { return uint32(stdbit(7, uint64(x), 32)) }
func StdcCountZerosUc(x uint8) uint32           { return uint32(stdbit(8, uint64(x), 8)) }
func StdcCountZerosUs(x uint16) uint32          { return uint32(stdbit(8, uint64(x), 16)) }
func StdcCountZerosUi(x uint32) uint32          { return uint32(stdbit(8, uint64(x), 32)) }
func StdcCountZerosUl(x uint64) uint32          { return uint32(stdbit(8, uint64(x), 64)) }
func StdcCountZerosUl32(x uint64) uint32        { return uint32(stdbit(8, uint64(x), 32)) }
func StdcCountOnesUc(x uint8) uint32            { return uint32(stdbit(9, uint64(x), 8)) }
func StdcCountOnesUs(x uint16) uint32           { return uint32(stdbit(9, uint64(x), 16)) }
func StdcCountOnesUi(x uint32) uint32           { return uint32(stdbit(9, uint64(x), 32)) }
func StdcCountOnesUl(x uint64) uint32           { return uint32(stdbit(9, uint64(x), 64)) }
func StdcCountOnesUl32(x uint64) uint32         { return uint32(stdbit(9, uint64(x), 32)) }
func StdcHasSingleBitUc(x uint8) uint8          { return uint8(stdbit(10, uint64(x), 8)) }
func StdcHasSingleBitUs(x uint16) uint8         { return uint8(stdbit(10, uint64(x), 16)) }
func StdcHasSingleBitUi(x uint32) uint8         { return uint8(stdbit(10, uint64(x), 32)) }
func StdcHasSingleBitUl(x uint64) uint8         { return uint8(stdbit(10, uint64(x), 64)) }
func StdcHasSingleBitUl32(x uint64) uint8       { return uint8(stdbit(10, uint64(x), 32)) }
func StdcBitWidthUc(x uint8) uint32             { return uint32(stdbit(11, uint64(x), 8)) }
func StdcBitWidthUs(x uint16) uint32            { return uint32(stdbit(11, uint64(x), 16)) }
func StdcBitWidthUi(x uint32) uint32            { return uint32(stdbit(11, uint64(x), 32)) }
func StdcBitWidthUl(x uint64) uint32            { return uint32(stdbit(11, uint64(x), 64)) }
func StdcBitWidthUl32(x uint64) uint32          { return uint32(stdbit(11, uint64(x), 32)) }
func StdcBitFloorUc(x uint8) uint8              { return uint8(stdbit(12, uint64(x), 8)) }
func StdcBitFloorUs(x uint16) uint16            { return uint16(stdbit(12, uint64(x), 16)) }
func StdcBitFloorUi(x uint32) uint32            { return uint32(stdbit(12, uint64(x), 32)) }
func StdcBitFloorUl(x uint64) uint64            { return uint64(stdbit(12, uint64(x), 64)) }
func StdcBitFloorUl32(x uint64) uint64          { return uint64(stdbit(12, uint64(x), 32)) }
func StdcBitCeilUc(x uint8) uint8               { return uint8(stdbit(13, uint64(x), 8)) }
func StdcBitCeilUs(x uint16) uint16             { return uint16(stdbit(13, uint64(x), 16)) }
func StdcBitCeilUi(x uint32) uint32             { return uint32(stdbit(13, uint64(x), 32)) }
func StdcBitCeilUl(x uint64) uint64             { return uint64(stdbit(13, uint64(x), 64)) }
func StdcBitCeilUl32(x uint64) uint64           { return uint64(stdbit(13, uint64(x), 32)) }
