// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package sha3

// With GOWASM=gc the state is an array of bytes in an object, which
// keccakF1600Generic would read and write as words one access at a
// time through the runtime's accessors: the permutation runs on a copy
// in linear memory, where those are plain loads and stores.
func keccakF1600(a *[200]byte) {
	b := *a
	keccakF1600Generic(&b)
	*a = b
}

func (d *Digest) write(p []byte) (n int, err error) {
	return d.writeGeneric(p)
}
func (d *Digest) read(out []byte) (n int, err error) {
	return d.readGeneric(out)
}
func (d *Digest) sum(b []byte) []byte {
	return d.sumGeneric(b)
}
