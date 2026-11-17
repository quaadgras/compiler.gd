// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm.gc

package sysrand

// The maximum buffer size for crypto.getRandomValues is 65536 bytes.
// https://developer.mozilla.org/en-US/docs/Web/API/Crypto/getRandomValues#exceptions
const maxGetRandomRead = 64 << 10

//go:wasmimport gojs runtime.getRandomData
//go:noescape
func getRandomValues(r []byte)

// read calls the JavaScript Crypto.getRandomValues() method.
// See https://developer.mozilla.org/en-US/docs/Web/API/Crypto/getRandomValues.
//
// With GOWASM=gc the bytes of b may be an object of the engine's (one
// that new or a struct made), which the host cannot write: it writes a
// buffer of linear memory, which make gives.
func read(b []byte) error {
	buf := make([]byte, min(len(b), maxGetRandomRead))
	for len(b) > 0 {
		chunk := buf[:min(len(b), len(buf))]
		getRandomValues(chunk)
		b = b[copy(b, chunk):]
	}
	return nil
}
