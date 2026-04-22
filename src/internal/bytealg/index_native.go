// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64 || loong64 || s390x || ppc64le || ppc64

// gd SSO: non-amd64 entries here (arm64, loong64, s390x, ppc64{,le})
// have asm written for the stock 16 B string ABI and no inline-rep
// prolog — IndexString on an inline-rep string reads the hash word as
// the second string's len. Follow-up: per-arch asm rewrite. Kept on
// the native list for now to avoid breaking index_{arch}.go's
// MaxBruteForce / Cutover definitions; the asm being wrong for
// 24 B headers is a pre-existing latent bug independent of inline.

package bytealg

// Index returns the index of the first instance of b in a, or -1 if b is not present in a.
// Requires 2 <= len(b) <= MaxLen.
//
//go:noescape
func Index(a, b []byte) int

// IndexString returns the index of the first instance of b in a, or -1 if b is not present in a.
// Requires 2 <= len(b) <= MaxLen.
//
//go:noescape
func IndexString(a, b string) int
