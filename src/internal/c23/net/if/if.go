// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package netif is net/if.h's functions for cmd/cc: interfaces.
package netif

import (
	"internal/c23"
	"net"
)

// IfNametoindex is if_nametoindex.
func IfNametoindex(name *c23.Void) uint32 {
	ifi, err := net.InterfaceByName(c23.GoString(name))
	if err != nil {
		c23.SetErrno(c23.ENODEV)
		return 0
	}
	return uint32(ifi.Index)
}

// IfIndextoname is if_indextoname: name has IF_NAMESIZE (16) bytes.
func IfIndextoname(index uint32, name *c23.Void) *c23.Void {
	ifi, err := net.InterfaceByIndex(int(index))
	if err != nil || !putString(name, 16, ifi.Name) {
		c23.SetErrno(c23.ENXIO)
		return (*c23.Void)(nil)
	}
	return name
}

// putString stores s and its NUL in the n bytes at p, if it fits.
func putString(p *c23.Void, n uint32, s string) bool {
	if uint32(len(s)) >= n {
		return false
	}
	copy(c23.Writable(p, uintptr(len(s)+1)), s+"\x00")
	return true
}
