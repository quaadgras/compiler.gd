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
		got := abi.AeshashString(s)
		var p unsafe.Pointer
		if len(s) > 0 {
			p = unsafe.Pointer(unsafe.StringData(s))
		}
		want := uint64(runtimeMemhash(p, 0, uintptr(len(s))))
		if got != want {
			t.Errorf("len=%d: AeshashString=%016x runtime.memhash=%016x", len(s), got, want)
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
		got := abi.AeshashString(s)
		var p unsafe.Pointer
		if n > 0 {
			p = unsafe.Pointer(unsafe.StringData(s))
		}
		want := uint64(runtimeMemhash(p, 0, uintptr(n)))
		if got != want {
			t.Errorf("iter=%d len=%d: got=%016x want=%016x bytes=%x",
				i, n, got, want, b)
			return
		}
	}
}

// TestStrhashInlineParity exercises the fast inline strhash path in
// runtime/asm_amd64.s by constructing inline-rep string headers of
// every length from 1 to 15 and comparing runtime.strhash's output
// to AeshashString (which is parity-tested against aeshashbody). If
// the inline asm drifts from aeshashbody, this test catches it.
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
		want := uintptr(abi.AeshashString(s))
		if got != want {
			t.Errorf("len=%d: strhash=%016x AeshashString=%016x",
				n, got, want)
		}
	}
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
