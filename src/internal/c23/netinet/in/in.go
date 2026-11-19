// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package in is netinet/in.h's functions for cmd/cc: byte order, the
// target's being little-endian.
package in

import "math/bits"

func Htons(v uint16) uint16 { return bits.ReverseBytes16(v) }
func Ntohs(v uint16) uint16 { return bits.ReverseBytes16(v) }
func Htonl(v uint32) uint32 { return bits.ReverseBytes32(v) }
func Ntohl(v uint32) uint32 { return bits.ReverseBytes32(v) }
