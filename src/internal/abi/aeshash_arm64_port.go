// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

// AeshashStringARM64 returns the hash that runtime.aeshashbody would
// compute for s when the program is built for GOARCH=arm64, given
// the fork's fixed aeskeysched (AeskeyschedSeed). The compiler calls
// this at build time (instead of AeshashString) when the target arch
// is arm64, so rodata-embedded string hashes match what the arm64
// runtime will compute via the AESE/AESMC instruction pair.
//
// Algorithmic differences from AeshashString (x86):
//
//   - Round function. arm64's one-round primitive is AESE+AESMC:
//     state = MixColumns(ShiftRows(SubBytes(state XOR key))).
//     x86's AESENC is: MixColumns(ShiftRows(SubBytes(state))) XOR key.
//     The key XOR happens at opposite ends of the round, so the two
//     produce different outputs for the same (state, key) pair.
//
//   - Seed construction. arm64 packs the full 64-bit length into the
//     high half of V30; x86 packs a 16-bit truncated length four
//     times via PSHUFHW.
//
//   - aes0to15 data placement. arm64 loads data with a bit-test chain
//     (if len bit 3: 8 B into V2.D[0]; bit 2: next 4 B into V2.S[2]
//     at offset 8; bit 1: next 2 B into V2.H[6] at offset 12; bit 0:
//     next 1 B into V2.B[14]). For non-power-of-two lengths this
//     scatters the source bytes across V2 in a way a contiguous pack
//     can't reproduce. We replicate the positional load here.
//
//   - The larger-block branches (aes17to32, aes33to64, aes65to128,
//     aes129plus) each have their own per-branch round counts and
//     reduction patterns faithfully mirrored below.
func AeshashStringARM64(s string) uint64 {
	n := len(s)

	// V30 = { seed_low_64: 0, length_high_64: n (full 64 bits) }.
	var v30 [16]byte
	armPutUint64(v30[8:], uint64(n))

	// V0 = aeskeysched[0..16]; V0 = AESE+AESMC(V30, V0).
	var v0 [16]byte
	copy(v0[:], AeskeyschedSeed[:16])
	v0 = armRound(v0, v30)

	switch {
	case n == 0:
		return aesLow64(v0)
	case n < 16:
		return armAes0to15(v0, s)
	case n == 16:
		return armAes16(v0, s)
	case n <= 32:
		return armAes17to32(v0, v30, s)
	case n <= 64:
		return armAes33to64(v0, v30, s)
	case n <= 128:
		return armAes65to128(v0, v30, s)
	default:
		return armAes129plus(v0, v30, s)
	}
}

// armRound: MC(SR(SB(state XOR key))) — AESE followed by AESMC.
func armRound(state, key [16]byte) [16]byte {
	var t [16]byte
	for i := range state {
		t[i] = state[i] ^ key[i]
	}
	for i := range t {
		t[i] = aesSbox[t[i]]
	}
	var sr [16]byte
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			sr[c*4+r] = t[((c+r)&3)*4+r]
		}
	}
	var mc [16]byte
	for c := 0; c < 4; c++ {
		a0, a1, a2, a3 := sr[c*4], sr[c*4+1], sr[c*4+2], sr[c*4+3]
		mc[c*4+0] = xtime(a0) ^ xtime(a1) ^ a1 ^ a2 ^ a3
		mc[c*4+1] = a0 ^ xtime(a1) ^ xtime(a2) ^ a2 ^ a3
		mc[c*4+2] = a0 ^ a1 ^ xtime(a2) ^ xtime(a3) ^ a3
		mc[c*4+3] = xtime(a0) ^ a0 ^ a1 ^ a2 ^ xtime(a3)
	}
	return mc
}

// armAESEonly: SR(SB(state XOR key)). Used by the larger-block
// branches whose final round is AESE without a trailing AESMC.
func armAESEonly(state, key [16]byte) [16]byte {
	var t [16]byte
	for i := range state {
		t[i] = state[i] ^ key[i]
	}
	for i := range t {
		t[i] = aesSbox[t[i]]
	}
	var sr [16]byte
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			sr[c*4+r] = t[((c+r)&3)*4+r]
		}
	}
	return sr
}

func armPutUint64(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

func armAes0to15(v0 [16]byte, s string) uint64 {
	var v2 [16]byte
	n := len(s)
	cursor := 0
	if n&8 != 0 {
		copy(v2[0:8], s[cursor:cursor+8])
		cursor += 8
	}
	if n&4 != 0 {
		copy(v2[8:12], s[cursor:cursor+4])
		cursor += 4
	}
	if n&2 != 0 {
		copy(v2[12:14], s[cursor:cursor+2])
		cursor += 2
	}
	if n&1 != 0 {
		v2[14] = s[cursor]
	}
	v2 = armRound(v2, v0)
	v2 = armRound(v2, v0)
	v2 = armRound(v2, v0)
	return aesLow64(v2)
}

func armAes16(v0 [16]byte, s string) uint64 {
	var v2 [16]byte
	copy(v2[:], s[:16])
	v2 = armRound(v2, v0)
	v2 = armRound(v2, v0)
	v2 = armRound(v2, v0)
	return aesLow64(v2)
}

func armAes17to32(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	// V1 from aeskeysched[0..16] XOR V30 (second seed).
	var v1 [16]byte
	copy(v1[:], AeskeyschedSeed[:16])
	v1 = armRound(v1, v30)

	var v2, v3 [16]byte
	copy(v2[:], s[n-16:])
	copy(v3[:], s[:16])

	v2 = armRound(v2, v0)
	v3 = armRound(v3, v1)
	v2 = armRound(v2, v0)
	v3 = armRound(v3, v1)
	v2 = armAESEonly(v2, v0)
	v3 = armAESEonly(v3, v1)

	for i := range v2 {
		v2[i] ^= v3[i]
	}
	return aesLow64(v2)
}

func armAes33to64(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	// V1, V2, V3 loaded from aeskeysched[0..48] XOR V30.
	var k1, k2, k3 [16]byte
	copy(k1[:], AeskeyschedSeed[0:16])
	copy(k2[:], AeskeyschedSeed[16:32])
	copy(k3[:], AeskeyschedSeed[32:48])
	k1 = armRound(k1, v30)
	k2 = armRound(k2, v30)
	k3 = armRound(k3, v30)

	// Four data blocks: last two (overlapping with first two if len<48)
	// then first two.
	var v4, v5, v6, v7 [16]byte
	copy(v4[:], s[n-32:n-16])
	copy(v5[:], s[n-16:])
	copy(v6[:], s[0:16])
	copy(v7[:], s[16:32])

	for i := 0; i < 2; i++ {
		v4 = armRound(v4, v0)
		v5 = armRound(v5, k1)
		v6 = armRound(v6, k2)
		v7 = armRound(v7, k3)
	}
	v4 = armAESEonly(v4, v0)
	v5 = armAESEonly(v5, k1)
	v6 = armAESEonly(v6, k2)
	v7 = armAESEonly(v7, k3)

	for i := range v4 {
		v4[i] ^= v6[i]
		v5[i] ^= v7[i]
		v4[i] ^= v5[i]
	}
	return aesLow64(v4)
}

func armAes65to128(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	// Seven more seed keys from aeskeysched[0..112] XOR V30.
	var k [7][16]byte
	for j := 0; j < 7; j++ {
		copy(k[j][:], AeskeyschedSeed[j*16:(j+1)*16])
		k[j] = armRound(k[j], v30)
	}
	// Eight data blocks: last four then first four.
	var blk [8][16]byte
	copy(blk[0][:], s[n-64:n-48])
	copy(blk[1][:], s[n-48:n-32])
	copy(blk[2][:], s[n-32:n-16])
	copy(blk[3][:], s[n-16:])
	copy(blk[4][:], s[0:16])
	copy(blk[5][:], s[16:32])
	copy(blk[6][:], s[32:48])
	copy(blk[7][:], s[48:64])

	keys := [8][16]byte{v0, k[0], k[1], k[2], k[3], k[4], k[5], k[6]}

	for r := 0; r < 2; r++ {
		for j := 0; j < 8; j++ {
			blk[j] = armRound(blk[j], keys[j])
		}
	}
	for j := 0; j < 8; j++ {
		blk[j] = armAESEonly(blk[j], keys[j])
	}

	// XOR reduce: [0]^=[4]; [1]^=[5]; [2]^=[6]; [3]^=[7];
	// then [0]^=[2]; [1]^=[3]; [0]^=[1].
	for i := range blk[0] {
		blk[0][i] ^= blk[4][i]
		blk[1][i] ^= blk[5][i]
		blk[2][i] ^= blk[6][i]
		blk[3][i] ^= blk[7][i]
		blk[0][i] ^= blk[2][i]
		blk[1][i] ^= blk[3][i]
		blk[0][i] ^= blk[1][i]
	}
	return aesLow64(blk[0])
}

func armAes129plus(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	// Same seven derived keys as aes65to128.
	var k [7][16]byte
	for j := 0; j < 7; j++ {
		copy(k[j][:], AeskeyschedSeed[j*16:(j+1)*16])
		k[j] = armRound(k[j], v30)
	}

	// Initial state: last 128 B of s XOR'd with seeds via first round.
	var state [8][16]byte
	for j := 0; j < 8; j++ {
		copy(state[j][:], s[n-128+j*16:n-112+j*16])
	}
	keys := [8][16]byte{v0, k[0], k[1], k[2], k[3], k[4], k[5], k[6]}
	for j := 0; j < 8; j++ {
		state[j] = armRound(state[j], keys[j])
	}

	// Process blocks from the start, as many full 128-B chunks as fit
	// before the already-processed tail.
	blocks := (n - 1) >> 7
	p := 0
	for b := 0; b < blocks; b++ {
		for j := 0; j < 8; j++ {
			var blk [16]byte
			copy(blk[:], s[p+j*16:p+j*16+16])
			state[j] = armRound(state[j], blk)
		}
		p += 128
	}

	// Two more full rounds then one AESE-only.
	for r := 0; r < 2; r++ {
		for j := 0; j < 8; j++ {
			state[j] = armRound(state[j], keys[j])
		}
	}
	for j := 0; j < 8; j++ {
		state[j] = armAESEonly(state[j], keys[j])
	}

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
