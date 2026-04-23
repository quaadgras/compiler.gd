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
