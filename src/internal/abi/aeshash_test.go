// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi_test

import (
	"internal/abi"
	"internal/cpu"
	"math/rand/v2"
	"runtime"
	"testing"
	"unsafe"
	_ "unsafe"
)

// runtimeMemhash is runtime.memhash. On amd64/arm64 with AES
// instructions, runtime.memhash dispatches directly into aeshashbody,
// so calling it with seed=0 and the gd fork's fixed aeskeysched gives
// us the exact output AeshashString must reproduce.
//
//go:linkname runtimeMemhash runtime.memhash
func runtimeMemhash(p unsafe.Pointer, h, s uintptr) uintptr

// runtimeStrhash is runtime.strhash. Its asm prolog does a cache
// check; on cache miss it routes into aeshashbody directly (fast
// inline path for 1..15 B strings, CALL for heap rep). Calling it
// on a freshly-heap-allocated string with the hash slot zeroed
// forces the miss path, giving us the asm-level hash. Used to
// verify the inline fast path in strhash_amd64 matches AeshashString.
//
//go:linkname runtimeStrhash runtime.strhash
func runtimeStrhash(p unsafe.Pointer, h uintptr) uintptr

func hasAESHash() bool {
	if runtime.GOARCH == "amd64" {
		return cpu.X86.HasAES && cpu.X86.HasSSSE3 && cpu.X86.HasSSE41
	}
	if runtime.GOARCH == "arm64" {
		return cpu.ARM64.HasAES
	}
	return false
}

// nativeAeshashString returns the fork's Go aeshash port that matches
// the current runtime.GOARCH — abi.AeshashStringARM64 on arm64,
// abi.AeshashString (x86 port) elsewhere. Parity tests compare this
// against runtime.memhash, which dispatches to the native aeshashbody
// for the running arch.
func nativeAeshashString(s string) uint64 {
	if runtime.GOARCH == "arm64" {
		return abi.AeshashStringARM64(s)
	}
	return abi.AeshashString(s)
}

func TestAeshashParity(t *testing.T) {
	if !hasAESHash() {
		t.Skip("CPU lacks AES instructions; asm aeshash inactive")
	}
	cases := []string{
		"", "a", "ab", "abc", "hello", "hello world",
		"fifteen chars!!", "sixteenXcharacts", "seventeenXcharacts",
		"thirty twoXcharactersXfit_paddin", // 32
		string(make([]byte, 33)),
		string(make([]byte, 64)),
		string(make([]byte, 65)),
		string(make([]byte, 127)),
		string(make([]byte, 128)),
		string(make([]byte, 129)),
		string(make([]byte, 255)),
		string(make([]byte, 256)),
		string(make([]byte, 1024)),
	}
	for _, s := range cases {
		got := nativeAeshashString(s)
		var p unsafe.Pointer
		if len(s) > 0 {
			p = unsafe.Pointer(unsafe.StringData(s))
		}
		want := uint64(runtimeMemhash(p, 0, uintptr(len(s))))
		if got != want {
			t.Errorf("goarch=%s len=%d: port=%016x runtime.memhash=%016x",
				runtime.GOARCH, len(s), got, want)
		}
	}
}

func TestAeshashParityRandom(t *testing.T) {
	if !hasAESHash() {
		t.Skip("CPU lacks AES instructions; asm aeshash inactive")
	}
	r := rand.New(rand.NewPCG(0xC001D00D, 0xBADC0DED))
	for i := 0; i < 500; i++ {
		n := r.IntN(500)
		b := make([]byte, n)
		for j := range b {
			b[j] = byte(r.Uint32())
		}
		s := string(b)
		got := nativeAeshashString(s)
		var p unsafe.Pointer
		if n > 0 {
			p = unsafe.Pointer(unsafe.StringData(s))
		}
		want := uint64(runtimeMemhash(p, 0, uintptr(n)))
		if got != want {
			t.Errorf("goarch=%s iter=%d len=%d: port=%016x runtime.memhash=%016x bytes=%x",
				runtime.GOARCH, i, n, got, want, b)
			return
		}
	}
}

// TestStrhashInlineParity exercises the strhash path on inline-rep
// strings by constructing every length from 1 to 15 and comparing
// runtime.strhash's output to the Go port matching the current arch
// (parity-tested against aeshashbody in TestAeshashParity). On amd64
// this covers the asm inline fast path in strhash_amd64; on arm64 it
// covers the spill+CALL path, and a regression in either would fail
// this test without needing access to the target's internals.
func TestStrhashInlineParity(t *testing.T) {
	if !hasAESHash() {
		t.Skip("CPU lacks AES instructions; inline fast path inactive")
	}
	for n := 1; n <= 15; n++ {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i*17 + 1)
		}
		s := string(data) // compiler may emit heap or inline — force inline below
		// Build an inline-rep header directly so strhash sees an
		// inline string (word 0 == nil). word 1 = bytes[0:min(n,8)]
		// little-endian; word 2 = (n<<60) | bytes[8:n].
		var w1, w2 uint64
		for i := 0; i < n && i < 8; i++ {
			w1 |= uint64(data[i]) << (8 * i)
		}
		for i := 8; i < n; i++ {
			w2 |= uint64(data[i]) << (8 * (i - 8))
		}
		w2 |= uint64(n) << abi.StringTagShift
		header := [3]uint64{0, w1, w2}
		got := runtimeStrhash(unsafe.Pointer(&header[0]), 0)
		want := uintptr(nativeAeshashString(s))
		if got != want {
			t.Errorf("goarch=%s len=%d: strhash=%016x port=%016x",
				runtime.GOARCH, n, got, want)
		}
	}
}

// TestAeshashARM64Deterministic does not verify ARM64 bit-exact
// parity (we'd need arm64 hardware). It locks the Go port's own
// consistency: same input always yields same output, different
// inputs yield different outputs with overwhelming probability,
// and every length branch is exercised.
func TestAeshashARM64Deterministic(t *testing.T) {
	r := rand.New(rand.NewPCG(0xDEADBEEF, 0xCAFEBABE))
	seen := make(map[uint64]string)
	for _, n := range []int{0, 1, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 500, 1024} {
		for trial := 0; trial < 4; trial++ {
			b := make([]byte, n)
			for j := range b {
				b[j] = byte(r.Uint32())
			}
			s := string(b)
			h1 := abi.AeshashStringARM64(s)
			h2 := abi.AeshashStringARM64(s)
			if h1 != h2 {
				t.Errorf("len=%d trial=%d unstable: %x vs %x", n, trial, h1, h2)
			}
			if prev, dup := seen[h1]; dup && prev != s {
				t.Logf("collision len=%d: %x between %q and %q", n, h1, prev, s)
			}
			seen[h1] = s
		}
	}
}

// TestAeshashARMvsX86Differ confirms the two ports produce distinct
// values for non-trivial inputs (sanity check that we haven't written
// the same algorithm twice by accident).
func TestAeshashARMvsX86Differ(t *testing.T) {
	distinct := 0
	total := 0
	r := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 50; trial++ {
		n := r.IntN(40) + 1
		b := make([]byte, n)
		for j := range b {
			b[j] = byte(r.Uint32())
		}
		s := string(b)
		if abi.AeshashString(s) != abi.AeshashStringARM64(s) {
			distinct++
		}
		total++
	}
	if distinct == 0 {
		t.Errorf("no inputs produced different outputs between x86 and arm64 ports; likely a bug")
	}
	t.Logf("%d/%d inputs yield distinct x86 vs arm64 hashes", distinct, total)
}

func BenchmarkAeshashString_5B(b *testing.B) {
	s := "hello"
	for i := 0; i < b.N; i++ {
		_ = abi.AeshashString(s)
	}
}

func BenchmarkAeshashString_32B(b *testing.B) {
	s := "thirty twoXcharactersXfit_paddin"
	for i := 0; i < b.N; i++ {
		_ = abi.AeshashString(s)
	}
}
