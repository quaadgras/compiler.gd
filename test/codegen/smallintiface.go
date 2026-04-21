// asmcheck

package codegen

// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gd fat-interface: bool/int8/uint8 are inline-eligible (pointer-free,
// size <= 16, align <= 8), so the compiler stores the raw value into the
// iface's inline slot instead of emitting a LEAQ into runtime.staticuint64s.
// The old staticuint64s checks don't apply under the fat-iface layout;
// verify instead that no allocation call is emitted.

func booliface() interface{} {
	// amd64:-`CALL runtime\.conv`
	return true
}

func smallint8iface() interface{} {
	// amd64:-`CALL runtime\.conv`
	return int8(-3)
}

func smalluint8iface() interface{} {
	// amd64:-`CALL runtime\.conv`
	return uint8(3)
}
