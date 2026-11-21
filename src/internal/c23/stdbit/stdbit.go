// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdbit

import (
	c23 "internal/c23"
)

func StdcLeadingZerosUc(x uint8) uint32 { return uint32(c23.Stdbit(0, uint64(x), 8)) }

func StdcLeadingZerosUs(x uint16) uint32 { return uint32(c23.Stdbit(0, uint64(x), 16)) }

func StdcLeadingZerosUi(x uint32) uint32 { return uint32(c23.Stdbit(0, uint64(x), 32)) }

func StdcLeadingZerosUl(x uint64) uint32 { return uint32(c23.Stdbit(0, uint64(x), 64)) }

func StdcLeadingOnesUc(x uint8) uint32 { return uint32(c23.Stdbit(1, uint64(x), 8)) }

func StdcLeadingOnesUs(x uint16) uint32 { return uint32(c23.Stdbit(1, uint64(x), 16)) }

func StdcLeadingOnesUi(x uint32) uint32 { return uint32(c23.Stdbit(1, uint64(x), 32)) }

func StdcLeadingOnesUl(x uint64) uint32 { return uint32(c23.Stdbit(1, uint64(x), 64)) }

func StdcTrailingZerosUc(x uint8) uint32 { return uint32(c23.Stdbit(2, uint64(x), 8)) }

func StdcTrailingZerosUs(x uint16) uint32 { return uint32(c23.Stdbit(2, uint64(x), 16)) }

func StdcTrailingZerosUi(x uint32) uint32 { return uint32(c23.Stdbit(2, uint64(x), 32)) }

func StdcTrailingZerosUl(x uint64) uint32 { return uint32(c23.Stdbit(2, uint64(x), 64)) }

func StdcTrailingOnesUc(x uint8) uint32 { return uint32(c23.Stdbit(3, uint64(x), 8)) }

func StdcTrailingOnesUs(x uint16) uint32 { return uint32(c23.Stdbit(3, uint64(x), 16)) }

func StdcTrailingOnesUi(x uint32) uint32 { return uint32(c23.Stdbit(3, uint64(x), 32)) }

func StdcTrailingOnesUl(x uint64) uint32 { return uint32(c23.Stdbit(3, uint64(x), 64)) }

func StdcFirstLeadingZeroUc(x uint8) uint32 { return uint32(c23.Stdbit(4, uint64(x), 8)) }

func StdcFirstLeadingZeroUs(x uint16) uint32 { return uint32(c23.Stdbit(4, uint64(x), 16)) }

func StdcFirstLeadingZeroUi(x uint32) uint32 { return uint32(c23.Stdbit(4, uint64(x), 32)) }

func StdcFirstLeadingZeroUl(x uint64) uint32 { return uint32(c23.Stdbit(4, uint64(x), 64)) }

func StdcFirstLeadingOneUc(x uint8) uint32 { return uint32(c23.Stdbit(5, uint64(x), 8)) }

func StdcFirstLeadingOneUs(x uint16) uint32 { return uint32(c23.Stdbit(5, uint64(x), 16)) }

func StdcFirstLeadingOneUi(x uint32) uint32 { return uint32(c23.Stdbit(5, uint64(x), 32)) }

func StdcFirstLeadingOneUl(x uint64) uint32 { return uint32(c23.Stdbit(5, uint64(x), 64)) }

func StdcFirstTrailingZeroUc(x uint8) uint32 { return uint32(c23.Stdbit(6, uint64(x), 8)) }

func StdcFirstTrailingZeroUs(x uint16) uint32 { return uint32(c23.Stdbit(6, uint64(x), 16)) }

func StdcFirstTrailingZeroUi(x uint32) uint32 { return uint32(c23.Stdbit(6, uint64(x), 32)) }

func StdcFirstTrailingZeroUl(x uint64) uint32 { return uint32(c23.Stdbit(6, uint64(x), 64)) }

func StdcFirstTrailingOneUc(x uint8) uint32 { return uint32(c23.Stdbit(7, uint64(x), 8)) }

func StdcFirstTrailingOneUs(x uint16) uint32 { return uint32(c23.Stdbit(7, uint64(x), 16)) }

func StdcFirstTrailingOneUi(x uint32) uint32 { return uint32(c23.Stdbit(7, uint64(x), 32)) }

func StdcFirstTrailingOneUl(x uint64) uint32 { return uint32(c23.Stdbit(7, uint64(x), 64)) }

func StdcCountZerosUc(x uint8) uint32 { return uint32(c23.Stdbit(8, uint64(x), 8)) }

func StdcCountZerosUs(x uint16) uint32 { return uint32(c23.Stdbit(8, uint64(x), 16)) }

func StdcCountZerosUi(x uint32) uint32 { return uint32(c23.Stdbit(8, uint64(x), 32)) }

func StdcCountZerosUl(x uint64) uint32 { return uint32(c23.Stdbit(8, uint64(x), 64)) }

func StdcCountOnesUc(x uint8) uint32 { return uint32(c23.Stdbit(9, uint64(x), 8)) }

func StdcCountOnesUs(x uint16) uint32 { return uint32(c23.Stdbit(9, uint64(x), 16)) }

func StdcCountOnesUi(x uint32) uint32 { return uint32(c23.Stdbit(9, uint64(x), 32)) }

func StdcCountOnesUl(x uint64) uint32 { return uint32(c23.Stdbit(9, uint64(x), 64)) }

func StdcHasSingleBitUc(x uint8) uint8 { return uint8(c23.Stdbit(10, uint64(x), 8)) }

func StdcHasSingleBitUs(x uint16) uint8 { return uint8(c23.Stdbit(10, uint64(x), 16)) }

func StdcHasSingleBitUi(x uint32) uint8 { return uint8(c23.Stdbit(10, uint64(x), 32)) }

func StdcHasSingleBitUl(x uint64) uint8 { return uint8(c23.Stdbit(10, uint64(x), 64)) }

func StdcBitWidthUc(x uint8) uint32 { return uint32(c23.Stdbit(11, uint64(x), 8)) }

func StdcBitWidthUs(x uint16) uint32 { return uint32(c23.Stdbit(11, uint64(x), 16)) }

func StdcBitWidthUi(x uint32) uint32 { return uint32(c23.Stdbit(11, uint64(x), 32)) }

func StdcBitWidthUl(x uint64) uint32 { return uint32(c23.Stdbit(11, uint64(x), 64)) }

func StdcBitFloorUc(x uint8) uint8 { return uint8(c23.Stdbit(12, uint64(x), 8)) }

func StdcBitFloorUs(x uint16) uint16 { return uint16(c23.Stdbit(12, uint64(x), 16)) }

func StdcBitFloorUi(x uint32) uint32 { return uint32(c23.Stdbit(12, uint64(x), 32)) }

func StdcBitFloorUl(x uint64) uint64 { return uint64(c23.Stdbit(12, uint64(x), 64)) }

func StdcBitCeilUc(x uint8) uint8 { return uint8(c23.Stdbit(13, uint64(x), 8)) }

func StdcBitCeilUs(x uint16) uint16 { return uint16(c23.Stdbit(13, uint64(x), 16)) }

func StdcBitCeilUi(x uint32) uint32 { return uint32(c23.Stdbit(13, uint64(x), 32)) }

func StdcBitCeilUl(x uint64) uint64 { return uint64(c23.Stdbit(13, uint64(x), 64)) }
