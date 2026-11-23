// asmcheck

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codegen

// A new object filled with plain stores is sealed by one store barrier
// after the last of them, before anything can publish it, on the targets
// whose stores may be seen out of order (doc/gd/publication-point.md).
// Where stores are in order there is none.

type pubT struct {
	S string
	N int
	I any
}

var pubP, pubQ *pubT

func pubFill(s string, x any) {
	p := new(pubT) // arm64:-"DMB" ppc64x:-"LWSYNC" riscv64:-"FENCE" loong64:-"DBAR"
	p.S = s        // arm64:-"DMB" ppc64x:-"LWSYNC" riscv64:-"FENCE" loong64:-"DBAR"
	p.N = 1        // arm64:-"DMB" ppc64x:-"LWSYNC" riscv64:-"FENCE" loong64:-"DBAR"
	// The last store of the fill has the barrier, and only it.
	p.I = x // arm64:"DMB" ppc64x:"LWSYNC" riscv64:"FENCE" loong64:"DBAR" mips64:"SYNC" amd64:-"MFENCE" s390x:-"SYNC"
	// The publishing store comes after it.
	pubP = p // arm64:-"DMB" ppc64x:-"LWSYNC" riscv64:-"FENCE" loong64:-"DBAR" amd64:-"MFENCE"
}

// Two objects: the second allocation ends the first fill.
func pubTwo(s string, x any) {
	p := new(pubT)
	p.S = s // arm64:"DMB"
	q := new(pubT)
	q.I = x // arm64:"DMB"
	pubP, pubQ = p, q
}

// In a function with a defer, a fault in a fill could run a deferred
// function that publishes it: nothing is filled with plain stores, so
// nothing is sealed.
func pubDefer(s string, q *pubT) {
	defer func() { recover() }()
	p := new(pubT)
	p.S = s // arm64:-"DMB .14"
	p.N = q.N
	pubP = p
}
