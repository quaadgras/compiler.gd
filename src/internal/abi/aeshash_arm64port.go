// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The gd compile-time aeshash port uses uint64 arithmetic that the
// 32-bit SSA lowering rules (386, arm, mips, mipsle, wasm) can't lower
// when the target itself is 32-bit and this file is pulled into that
// build of internal/abi. The runtime never calls AeshashStringARM64
// (it's purely a compile-time helper for when the emitter targets
// arm64), so restrict the file to 64-bit GOARCHes. Host compilers for
// those same 64-bit hosts continue to see it and can still emit arm64
// hashes when GOARCH=arm64.

//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

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

// armSeedKey returns aeskeysched block j (16 B) scrambled by V30, as
// the larger branches derive their per-lane keys. memHashAES has
// already post-incremented R4 past block 0 (which became V0), so lane
// keys come from blocks 1 onward.
func armSeedKey(v30 [16]byte, j int) [16]byte {
	var k [16]byte
	copy(k[:], AeskeyschedSeed[j*16:(j+1)*16])
	return armRound(k, v30)
}

// armLoad16 returns s[off:off+16].
func armLoad16(s string, off int) [16]byte {
	var b [16]byte
	copy(b[:], s[off:off+16])
	return b
}

// armFinish runs the shared tail of the 17..128 branches on each lane:
// two AESE+AESMC rounds and a final AESE, each lane with its own key.
func armFinish(lanes [][16]byte, keys [][16]byte) {
	for r := 0; r < 2; r++ {
		for j := range lanes {
			lanes[j] = armRound(lanes[j], keys[j])
		}
	}
	for j := range lanes {
		lanes[j] = armAESEonly(lanes[j], keys[j])
	}
}

func xor16(a, b [16]byte) [16]byte {
	for i := range a {
		a[i] ^= b[i]
	}
	return a
}

// The data loads below mirror memHashAES's VLD1.P (R0)(R10) idiom: a
// post-indexed load reads at the base address (the start of s) and
// only then advances R0 by R10, so the first lanes hold the leading
// bytes and the later lanes the trailing ones.

func armAes17to32(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	lanes := [][16]byte{armLoad16(s, 0), armLoad16(s, n-16)}
	armFinish(lanes, [][16]byte{v0, armSeedKey(v30, 1)})
	return aesLow64(xor16(lanes[0], lanes[1]))
}

func armAes33to64(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	lanes := [][16]byte{armLoad16(s, 0), armLoad16(s, 16), armLoad16(s, n-32), armLoad16(s, n-16)}
	keys := [][16]byte{v0, armSeedKey(v30, 1), armSeedKey(v30, 2), armSeedKey(v30, 3)}
	armFinish(lanes, keys)
	// V4^=V6; V5^=V7; V4^=V5.
	a := xor16(lanes[0], lanes[2])
	b := xor16(lanes[1], lanes[3])
	return aesLow64(xor16(a, b))
}

func armAes65to128(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	lanes := make([][16]byte, 8)
	keys := make([][16]byte, 8)
	keys[0] = v0
	for j := 0; j < 4; j++ {
		lanes[j] = armLoad16(s, j*16)
		lanes[4+j] = armLoad16(s, n-64+j*16)
	}
	for j := 1; j < 8; j++ {
		keys[j] = armSeedKey(v30, j)
	}
	armFinish(lanes, keys)
	// V8^=V12 .. V11^=V15; V8^=V10; V9^=V11; V8^=V9.
	for j := 0; j < 4; j++ {
		lanes[j] = xor16(lanes[j], lanes[4+j])
	}
	lanes[0] = xor16(lanes[0], lanes[2])
	lanes[1] = xor16(lanes[1], lanes[3])
	return aesLow64(xor16(lanes[0], lanes[1]))
}

// armAes129plus mirrors aes129plus, where the roles swap: the eight
// scrambled seeds are the running state (V0..V7) and the data blocks
// (V8..V15) are the round keys.
func armAes129plus(v0, v30 [16]byte, s string) uint64 {
	n := len(s)
	var st, d [8][16]byte
	st[0] = v0
	for j := 1; j < 8; j++ {
		st[j] = armSeedKey(v30, j)
	}
	// The last 128 bytes first.
	for j := 0; j < 8; j++ {
		d[j] = armLoad16(s, n-128+j*16)
	}
	p := 0
	for it := (n - 1) >> 7; it > 0; it-- {
		for j := 0; j < 8; j++ {
			st[j] = armRound(st[j], d[j])
		}
		for j := 0; j < 4; j++ {
			d[j] = armLoad16(s, p+j*16)
		}
		p += 64
		for j := 0; j < 4; j++ {
			st[j] = armRound(st[j], d[j])
		}
		for j := 4; j < 8; j++ {
			d[j] = armLoad16(s, p+(j-4)*16)
		}
		p += 64
		for j := 4; j < 8; j++ {
			st[j] = armRound(st[j], d[j])
		}
	}
	for r := 0; r < 2; r++ {
		for j := 0; j < 8; j++ {
			st[j] = armRound(st[j], d[j])
		}
	}
	for j := 0; j < 8; j++ {
		st[j] = armAESEonly(st[j], d[j])
	}
	// V0^=V1; V2^=V3; V4^=V5; V6^=V7; V0^=V2; V4^=V6; V0^=V4.
	a := xor16(xor16(st[0], st[1]), xor16(st[2], st[3]))
	b := xor16(xor16(st[4], st[5]), xor16(st[6], st[7]))
	return aesLow64(xor16(a, b))
}
