// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inet is arpa/inet.h's functions for cmd/cc: addresses as text.
package inet

import (
	"internal/c23"
	"net/netip"
	"strconv"
	"strings"
)

const (
	AF_INET  = 2
	AF_INET6 = 10
)

// ParseInet4 parses inet_aton's forms: a.b.c.d, a.b.c (c 16 bits),
// a.b (b 24 bits) and a (32 bits), each part decimal, octal (0) or hex
// (0x).
func ParseInet4(s string) (netip.Addr, bool) {
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	var vals []uint64
	for _, p := range parts {
		if p == "" {
			return netip.Addr{}, false
		}
		v, err := strconv.ParseUint(p, 0, 32)
		if err != nil {
			return netip.Addr{}, false
		}
		vals = append(vals, v)
	}
	last := vals[len(vals)-1]
	if last >= 1<<(8*(5-len(vals))) && len(vals) > 1 {
		return netip.Addr{}, false
	}
	var v uint64
	for i, x := range vals[:len(vals)-1] {
		if x > 255 {
			return netip.Addr{}, false
		}
		v |= x << (24 - 8*i)
	}
	v |= last
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}

// InetAton is inet_aton.
func InetAton(cp, inp *c23.Void) int32 {
	ip, ok := ParseInet4(c23.GoString(cp))
	if !ok {
		return 0
	}
	a := ip.As4()
	copy(c23.Writable(inp, 4), a[:])
	return 1
}

// InetAddr is inet_addr: INADDR_NONE (all ones) if cp is not one.
func InetAddr(cp *c23.Void) uint32 {
	ip, ok := ParseInet4(c23.GoString(cp))
	if !ok {
		return 0xffffffff
	}
	a := ip.As4()
	return uint32(a[0]) | uint32(a[1])<<8 | uint32(a[2])<<16 | uint32(a[3])<<24
}

var ntoaBuf = c23.NewTLS(func() *c23.Void { return c23.Malloc(16) })

// InetNtoa is inet_ntoa, of the address's s_addr: the result is in the
// thread's buffer, which the next call overwrites.
func InetNtoa(a uint32) *c23.Void {
	s := netip.AddrFrom4([4]byte{byte(a), byte(a >> 8), byte(a >> 16), byte(a >> 24)}).String()
	p := ntoaBuf.Get()
	copy(c23.Writable(p, uintptr(len(s)+1)), s+"\x00")
	return p
}

// InetPton is inet_pton: AF_INET takes only a.b.c.d.
func InetPton(family int32, src, dst *c23.Void) int32 {
	s := c23.GoString(src)
	switch family {
	case AF_INET:
		ip, err := netip.ParseAddr(s)
		if err != nil || !ip.Is4() {
			return 0
		}
		a := ip.As4()
		copy(c23.Writable(dst, 4), a[:])
		return 1
	case AF_INET6:
		ip, err := netip.ParseAddr(s)
		if err != nil || ip.Zone() != "" {
			return 0
		}
		a := ip.As16()
		copy(c23.Writable(dst, 16), a[:])
		return 1
	}
	c23.SetErrno(c23.EAFNOSUPPORT)
	return -1
}

// InetNtop is inet_ntop.
func InetNtop(family int32, src, dst *c23.Void, n uint32) *c23.Void {
	var s string
	switch family {
	case AF_INET:
		s = netip.AddrFrom4([4]byte(c23.BytesAt(src, 4))).String()
	case AF_INET6:
		ip := netip.AddrFrom16([16]byte(c23.BytesAt(src, 16)))
		s = ip.String()
		if ip.Is4In6() {
			s = "::ffff:" + ip.Unmap().String()
		}
	default:
		c23.SetErrno(c23.EAFNOSUPPORT)
		return (*c23.Void)(nil)
	}
	if !putString(dst, n, s) {
		c23.SetErrno(c23.ENOSPC)
		return (*c23.Void)(nil)
	}
	return dst
}

// putString stores s and its NUL in the n bytes at p, if it fits.
func putString(p *c23.Void, n uint32, s string) bool {
	if uint32(len(s)) >= n {
		return false
	}
	copy(c23.Writable(p, uintptr(len(s)+1)), s+"\x00")
	return true
}
