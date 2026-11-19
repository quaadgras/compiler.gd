// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inet

import (
	"internal/c23"
	"internal/goarch"
	"math/bits"
)

// The classful network functions, as glibc's: their numbers are in the
// host's byte order, s_addr in the network's.

// ntohl is s_addr's value in the host's byte order.
func ntohl(a uint32) uint32 {
	if goarch.BigEndian {
		return a
	}
	return bits.ReverseBytes32(a)
}

// InetLnaof is inet_lnaof, of the address's s_addr: the host's part of
// the address of class A, B or C.
func InetLnaof(a uint32) uint32 {
	i := ntohl(a)
	switch {
	case i&0x80000000 == 0:
		return i & 0x00ffffff
	case i&0xc0000000 == 0x80000000:
		return i & 0x0000ffff
	}
	return i & 0xff
}

// InetNetof is inet_netof, of the address's s_addr: its network's part.
func InetNetof(a uint32) uint32 {
	i := ntohl(a)
	switch {
	case i&0x80000000 == 0:
		return i >> 24
	case i&0xc0000000 == 0x80000000:
		return i >> 16
	}
	return i >> 8
}

// InetMakeaddr is inet_makeaddr: it stores at sret the struct in_addr of
// host in network net.
func InetMakeaddr(sret Ptr, net, host uint32) {
	var a uint32
	switch {
	case net < 128:
		a = net<<24 | host&0x00ffffff
	case net < 65536:
		a = net<<16 | host&0x0000ffff
	case net < 16777216:
		a = net<<8 | host&0xff
	default:
		a = net | host
	}
	copy(c23.BytesAt(sret, 4), []byte{byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)})
}

// InetNetwork is inet_network, as glibc's: up to four parts of a network
// number (each decimal, octal or hex, at most 255), the last the least
// significant byte; INADDR_NONE if cp is not one.
func InetNetwork(cp Ptr) uint32 {
	return inetNetwork(c23.GoString(cp))
}

func inetNetwork(s string) uint32 {
	const none = 0xffffffff
	var parts []uint32
	for {
		val, base, digit := uint32(0), uint32(10), false
		if len(s) > 0 && s[0] == '0' {
			digit, base, s = true, 8, s[1:]
		}
		if len(s) > 0 && (s[0] == 'x' || s[0] == 'X') {
			digit, base, s = false, 16, s[1:]
		}
		for len(s) > 0 {
			c := s[0]
			switch {
			case c >= '0' && c <= '9':
				if base == 8 && c >= '8' {
					return none
				}
				val = val*base + uint32(c-'0')
			case base == 16 && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'):
				val = val<<4 + uint32(c|0x20-'a'+10)
			default:
				goto end
			}
			s = s[1:]
			digit = true
		}
	end:
		if !digit || len(parts) >= 4 || val > 0xff {
			return none
		}
		if len(s) > 0 && s[0] == '.' {
			parts = append(parts, val)
			s = s[1:]
			continue
		}
		for len(s) > 0 && (s[0] == ' ' || s[0] >= '\t' && s[0] <= '\r') {
			s = s[1:]
		}
		if s != "" {
			return none
		}
		parts = append(parts, val)
		break
	}
	var v uint32
	for _, p := range parts {
		v = v<<8 | p
	}
	return v
}
