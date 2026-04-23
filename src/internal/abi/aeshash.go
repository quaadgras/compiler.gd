// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

// aesLow64 returns the low 64 bits of a 16-B AES state, little-endian.
func aesLow64(x [16]byte) uint64 {
	return uint64(x[0]) | uint64(x[1])<<8 | uint64(x[2])<<16 | uint64(x[3])<<24 |
		uint64(x[4])<<32 | uint64(x[5])<<40 | uint64(x[6])<<48 | uint64(x[7])<<56
}

// AeskeyschedSeed is the fixed 128-byte seed the gd fork installs into
// runtime.aeskeysched in place of the stock bootstrapRand-filled key.
// With this seed plus the fork's per-map seed=0 convention, aeshashbody
// becomes a pure function of its input bytes — every run of every
// program sees the same string hash for the same string, and the
// compiler can emit matching hashes into rodata string headers.
//
// The bytes are the first 128 B of pi's fractional part in hex. This
// is a nothing-up-my-sleeve choice: the constants carry no structure
// an adversary could exploit, and they're independently derivable.
//
// Keep byte-identical with the copy installed by runtime.initAlgAES.
var AeskeyschedSeed = [128]byte{
	0x24, 0x3f, 0x6a, 0x88, 0x85, 0xa3, 0x08, 0xd3,
	0x13, 0x19, 0x8a, 0x2e, 0x03, 0x70, 0x73, 0x44,
	0xa4, 0x09, 0x38, 0x22, 0x29, 0x9f, 0x31, 0xd0,
	0x08, 0x2e, 0xfa, 0x98, 0xec, 0x4e, 0x6c, 0x89,
	0x45, 0x28, 0x21, 0xe6, 0x38, 0xd0, 0x13, 0x77,
	0xbe, 0x54, 0x66, 0xcf, 0x34, 0xe9, 0x0c, 0x6c,
	0xc0, 0xac, 0x29, 0xb7, 0xc9, 0x7c, 0x50, 0xdd,
	0x3f, 0x84, 0xd5, 0xb5, 0xb5, 0x47, 0x09, 0x17,
	0x92, 0x16, 0xd5, 0xd9, 0x89, 0x79, 0xfb, 0x1b,
	0xd1, 0x31, 0x0b, 0xa6, 0x98, 0xdf, 0xb5, 0xac,
	0x2f, 0xfd, 0x72, 0xdb, 0xd0, 0x1a, 0xdf, 0xb7,
	0xb8, 0xe1, 0xaf, 0xed, 0x6a, 0x26, 0x7e, 0x96,
	0xba, 0x7c, 0x90, 0x45, 0xf1, 0x2c, 0x7f, 0x99,
	0x24, 0xa1, 0x99, 0x47, 0xb3, 0x91, 0x6c, 0xf7,
	0x08, 0x01, 0xf2, 0xe2, 0x85, 0x8e, 0xfc, 0x16,
	0x63, 0x69, 0x20, 0xd8, 0x71, 0x57, 0x4e, 0x69,
}

// AeshashString returns the gd fork's aeshash of s, bit-identical to
// runtime.aeshashbody(AX=&s, BX=0, CX=len(s)) when aeskeysched is set
// to AeskeyschedSeed. Both compile-time (stringConstHash rewrite) and
// runtime (sealStringHash) producers call this so every cached hash
// matches what a fresh aeshashbody call would compute.
//
// The algorithm faithfully replicates runtime/asm_amd64.s aeshashbody
// with BX=0; see that file for commentary on the branching structure.
func AeshashString(s string) uint64 {
	n := len(s)
	// Initial seed: low 64 = BX (=0); high 64 = len truncated to u16
	// repeated four times. Matches PINSRW $4, CX, X0 + PSHUFHW $0.
	var x0, x1 [16]byte
	l16 := uint16(n)
	for i := 0; i < 4; i++ {
		x0[8+2*i] = byte(l16)
		x0[8+2*i+1] = byte(l16 >> 8)
	}
	x1 = x0 // unscrambled copy for derivation of X1..X7
	// X0 ^= aeskeysched[0..16]; X0 = AESENC(X0, X0)
	for i := range x0 {
		x0[i] ^= AeskeyschedSeed[i]
	}
	x0 = aesenc(x0, x0)

	switch {
	case n == 0:
		// aes0: return scramble(seed)
		r := aesenc(x0, x0)
		return aesLow64(r)
	case n < 16:
		return aesSmall(x0, s)
	case n == 16:
		return aesSmall(x0, s)
	case n <= 32:
		return aes17to32(x0, x1, s)
	case n <= 64:
		return aes33to64(x0, x1, s)
	case n <= 128:
		return aes65to128(x0, x1, s)
	default:
		return aes129plus(x0, x1, s)
	}
}

// aesSmall hashes 1..16 bytes. For len<16 the tail is zero-padded,
// matching the asm's PAND-with-masks loading.
func aesSmall(x0 [16]byte, s string) uint64 {
	var x1 [16]byte
	copy(x1[:], s)
	for i := range x1 {
		x1[i] ^= x0[i]
	}
	x1 = aesenc(x1, x1)
	x1 = aesenc(x1, x1)
	x1 = aesenc(x1, x1)
	return aesLow64(x1)
}

func aes17to32(x0, x1 [16]byte, s string) uint64 {
	n := len(s)
	// Derive second seed: X1 ^= aeskeysched[16..32]; X1 = AESENC(X1, X1)
	for i := range x1 {
		x1[i] ^= AeskeyschedSeed[16+i]
	}
	x1 = aesenc(x1, x1)

	var x2, x3 [16]byte
	copy(x2[:], s[:16])
	copy(x3[:], s[n-16:])
	for i := range x2 {
		x2[i] ^= x0[i]
		x3[i] ^= x1[i]
	}
	for k := 0; k < 3; k++ {
		x2 = aesenc(x2, x2)
		x3 = aesenc(x3, x3)
	}
	for i := range x2 {
		x2[i] ^= x3[i]
	}
	return aesLow64(x2)
}

func aes33to64(x0, x1 [16]byte, s string) uint64 {
	n := len(s)
	x2, x3 := x1, x1
	for i := range x1 {
		x1[i] ^= AeskeyschedSeed[16+i]
		x2[i] ^= AeskeyschedSeed[32+i]
		x3[i] ^= AeskeyschedSeed[48+i]
	}
	x1 = aesenc(x1, x1)
	x2 = aesenc(x2, x2)
	x3 = aesenc(x3, x3)

	var x4, x5, x6, x7 [16]byte
	copy(x4[:], s[0:16])
	copy(x5[:], s[16:32])
	copy(x6[:], s[n-32:n-16])
	copy(x7[:], s[n-16:])
	for i := range x4 {
		x4[i] ^= x0[i]
		x5[i] ^= x1[i]
		x6[i] ^= x2[i]
		x7[i] ^= x3[i]
	}
	for k := 0; k < 3; k++ {
		x4 = aesenc(x4, x4)
		x5 = aesenc(x5, x5)
		x6 = aesenc(x6, x6)
		x7 = aesenc(x7, x7)
	}
	// X4 ^= X6; X5 ^= X7; X4 ^= X5
	for i := range x4 {
		x4[i] ^= x6[i]
		x5[i] ^= x7[i]
		x4[i] ^= x5[i]
	}
	return aesLow64(x4)
}

func aes65to128(x0, x1 [16]byte, s string) uint64 {
	n := len(s)
	x2, x3, x4, x5, x6, x7 := x1, x1, x1, x1, x1, x1
	for i := range x1 {
		x1[i] ^= AeskeyschedSeed[16+i]
		x2[i] ^= AeskeyschedSeed[32+i]
		x3[i] ^= AeskeyschedSeed[48+i]
		x4[i] ^= AeskeyschedSeed[64+i]
		x5[i] ^= AeskeyschedSeed[80+i]
		x6[i] ^= AeskeyschedSeed[96+i]
		x7[i] ^= AeskeyschedSeed[112+i]
	}
	x1 = aesenc(x1, x1)
	x2 = aesenc(x2, x2)
	x3 = aesenc(x3, x3)
	x4 = aesenc(x4, x4)
	x5 = aesenc(x5, x5)
	x6 = aesenc(x6, x6)
	x7 = aesenc(x7, x7)

	var b [8][16]byte
	copy(b[0][:], s[0:16])
	copy(b[1][:], s[16:32])
	copy(b[2][:], s[32:48])
	copy(b[3][:], s[48:64])
	copy(b[4][:], s[n-64:n-48])
	copy(b[5][:], s[n-48:n-32])
	copy(b[6][:], s[n-32:n-16])
	copy(b[7][:], s[n-16:])
	seeds := [8][16]byte{x0, x1, x2, x3, x4, x5, x6, x7}
	for j := 0; j < 8; j++ {
		for i := range b[j] {
			b[j][i] ^= seeds[j][i]
		}
	}
	for k := 0; k < 3; k++ {
		for j := 0; j < 8; j++ {
			b[j] = aesenc(b[j], b[j])
		}
	}
	// PXOR pattern: b0^=b4, b1^=b5, b2^=b6, b3^=b7, b0^=b2, b1^=b3, b0^=b1
	for i := range b[0] {
		b[0][i] ^= b[4][i]
		b[1][i] ^= b[5][i]
		b[2][i] ^= b[6][i]
		b[3][i] ^= b[7][i]
		b[0][i] ^= b[2][i]
		b[1][i] ^= b[3][i]
		b[0][i] ^= b[1][i]
	}
	return aesLow64(b[0])
}

func aes129plus(x0, x1 [16]byte, s string) uint64 {
	n := len(s)
	// Derive X1..X7 as in aes65to128.
	x2, x3, x4, x5, x6, x7 := x1, x1, x1, x1, x1, x1
	for i := range x1 {
		x1[i] ^= AeskeyschedSeed[16+i]
		x2[i] ^= AeskeyschedSeed[32+i]
		x3[i] ^= AeskeyschedSeed[48+i]
		x4[i] ^= AeskeyschedSeed[64+i]
		x5[i] ^= AeskeyschedSeed[80+i]
		x6[i] ^= AeskeyschedSeed[96+i]
		x7[i] ^= AeskeyschedSeed[112+i]
	}
	x1 = aesenc(x1, x1)
	x2 = aesenc(x2, x2)
	x3 = aesenc(x3, x3)
	x4 = aesenc(x4, x4)
	x5 = aesenc(x5, x5)
	x6 = aesenc(x6, x6)
	x7 = aesenc(x7, x7)

	// Initial state: last 128 B of s XOR'd with seeds.
	// state[j] = s[n-128+16*j : n-112+16*j] XOR seeds[j]
	var state [8][16]byte
	for j := 0; j < 8; j++ {
		copy(state[j][:], s[n-128+16*j:n-112+16*j])
	}
	seeds := [8][16]byte{x0, x1, x2, x3, x4, x5, x6, x7}
	for j := 0; j < 8; j++ {
		for i := range state[j] {
			state[j][i] ^= seeds[j][i]
		}
	}

	// Number of 128-B blocks to process (from the asm: DECQ CX; SHRQ $7, CX).
	// That's (n-1)/128, which equals floor((n-1)/128). For n=129 → 1 block.
	blocks := (n - 1) >> 7
	// Each iteration: scramble state via AESENC, then AESENC(state, data_block)
	// where data_block is read from current position AX (starts at s[0]).
	p := 0
	for k := 0; k < blocks; k++ {
		// scramble
		for j := 0; j < 8; j++ {
			state[j] = aesenc(state[j], state[j])
		}
		// xor-in block
		var blk [8][16]byte
		for j := 0; j < 8; j++ {
			copy(blk[j][:], s[p+16*j:p+16*j+16])
		}
		for j := 0; j < 8; j++ {
			state[j] = aesenc(state[j], blk[j])
		}
		p += 128
	}

	// 3 more scrambles
	for k := 0; k < 3; k++ {
		for j := 0; j < 8; j++ {
			state[j] = aesenc(state[j], state[j])
		}
	}

	// Reduce: state[0] ^= state[4]; [1]^=[5]; [2]^=[6]; [3]^=[7];
	// then [0]^=[2]; [1]^=[3]; then [0]^=[1].
	for i := range state[0] {
		state[0][i] ^= state[4][i]
		state[1][i] ^= state[5][i]
		state[2][i] ^= state[6][i]
		state[3][i] ^= state[7][i]
		state[0][i] ^= state[2][i]
		state[1][i] ^= state[3][i]
		state[0][i] ^= state[1][i]
	}
	return aesLow64(state[0])
}

// aesenc performs one AES round: MixColumns(ShiftRows(SubBytes(state))) XOR key.
// The AES state is 4 columns × 4 rows stored column-major: byte[c*4+r].
func aesenc(state, key [16]byte) [16]byte {
	// SubBytes
	var s [16]byte
	for i := range state {
		s[i] = aesSbox[state[i]]
	}
	// ShiftRows: row r shifts left by r positions.
	// Destination (c, r) pulls from source ((c+r) mod 4, r).
	var sr [16]byte
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			sr[c*4+r] = s[((c+r)&3)*4+r]
		}
	}
	// MixColumns: each column [a0,a1,a2,a3] transforms to
	//   [2·a0^3·a1^a2^a3, a0^2·a1^3·a2^a3, a0^a1^2·a2^3·a3, 3·a0^a1^a2^2·a3]
	// in GF(2^8) with reduction poly 0x11b.
	var mc [16]byte
	for c := 0; c < 4; c++ {
		a0, a1, a2, a3 := sr[c*4], sr[c*4+1], sr[c*4+2], sr[c*4+3]
		mc[c*4+0] = xtime(a0) ^ xtime(a1) ^ a1 ^ a2 ^ a3
		mc[c*4+1] = a0 ^ xtime(a1) ^ xtime(a2) ^ a2 ^ a3
		mc[c*4+2] = a0 ^ a1 ^ xtime(a2) ^ xtime(a3) ^ a3
		mc[c*4+3] = xtime(a0) ^ a0 ^ a1 ^ a2 ^ xtime(a3)
	}
	// AddRoundKey
	for i := range mc {
		mc[i] ^= key[i]
	}
	return mc
}

// xtime multiplies by 2 in GF(2^8) using reduction polynomial 0x11b.
func xtime(b byte) byte {
	if b&0x80 != 0 {
		return (b << 1) ^ 0x1b
	}
	return b << 1
}

// aesSbox is the standard AES S-box (FIPS-197 Figure 7).
var aesSbox = [256]byte{
	0x63, 0x7c, 0x77, 0x7b, 0xf2, 0x6b, 0x6f, 0xc5, 0x30, 0x01, 0x67, 0x2b, 0xfe, 0xd7, 0xab, 0x76,
	0xca, 0x82, 0xc9, 0x7d, 0xfa, 0x59, 0x47, 0xf0, 0xad, 0xd4, 0xa2, 0xaf, 0x9c, 0xa4, 0x72, 0xc0,
	0xb7, 0xfd, 0x93, 0x26, 0x36, 0x3f, 0xf7, 0xcc, 0x34, 0xa5, 0xe5, 0xf1, 0x71, 0xd8, 0x31, 0x15,
	0x04, 0xc7, 0x23, 0xc3, 0x18, 0x96, 0x05, 0x9a, 0x07, 0x12, 0x80, 0xe2, 0xeb, 0x27, 0xb2, 0x75,
	0x09, 0x83, 0x2c, 0x1a, 0x1b, 0x6e, 0x5a, 0xa0, 0x52, 0x3b, 0xd6, 0xb3, 0x29, 0xe3, 0x2f, 0x84,
	0x53, 0xd1, 0x00, 0xed, 0x20, 0xfc, 0xb1, 0x5b, 0x6a, 0xcb, 0xbe, 0x39, 0x4a, 0x4c, 0x58, 0xcf,
	0xd0, 0xef, 0xaa, 0xfb, 0x43, 0x4d, 0x33, 0x85, 0x45, 0xf9, 0x02, 0x7f, 0x50, 0x3c, 0x9f, 0xa8,
	0x51, 0xa3, 0x40, 0x8f, 0x92, 0x9d, 0x38, 0xf5, 0xbc, 0xb6, 0xda, 0x21, 0x10, 0xff, 0xf3, 0xd2,
	0xcd, 0x0c, 0x13, 0xec, 0x5f, 0x97, 0x44, 0x17, 0xc4, 0xa7, 0x7e, 0x3d, 0x64, 0x5d, 0x19, 0x73,
	0x60, 0x81, 0x4f, 0xdc, 0x22, 0x2a, 0x90, 0x88, 0x46, 0xee, 0xb8, 0x14, 0xde, 0x5e, 0x0b, 0xdb,
	0xe0, 0x32, 0x3a, 0x0a, 0x49, 0x06, 0x24, 0x5c, 0xc2, 0xd3, 0xac, 0x62, 0x91, 0x95, 0xe4, 0x79,
	0xe7, 0xc8, 0x37, 0x6d, 0x8d, 0xd5, 0x4e, 0xa9, 0x6c, 0x56, 0xf4, 0xea, 0x65, 0x7a, 0xae, 0x08,
	0xba, 0x78, 0x25, 0x2e, 0x1c, 0xa6, 0xb4, 0xc6, 0xe8, 0xdd, 0x74, 0x1f, 0x4b, 0xbd, 0x8b, 0x8a,
	0x70, 0x3e, 0xb5, 0x66, 0x48, 0x03, 0xf6, 0x0e, 0x61, 0x35, 0x57, 0xb9, 0x86, 0xc1, 0x1d, 0x9e,
	0xe1, 0xf8, 0x98, 0x11, 0x69, 0xd9, 0x8e, 0x94, 0x9b, 0x1e, 0x87, 0xe9, 0xce, 0x55, 0x28, 0xdf,
	0x8c, 0xa1, 0x89, 0x0d, 0xbf, 0xe6, 0x42, 0x68, 0x41, 0x99, 0x2d, 0x0f, 0xb0, 0x54, 0xbb, 0x16,
}
