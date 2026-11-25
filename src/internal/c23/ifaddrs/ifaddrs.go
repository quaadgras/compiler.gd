// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ifaddrs is ifaddrs.h's functions for cmd/cc: net's interfaces
// and their addresses.
package ifaddrs

import (
	c23 "internal/c23"
	"internal/c23/sys/socket"
	"net"
	"net/netip"
	"runtime"
)

// net/if.h's flags, Linux's.
const (
	IFF_UP          = 0x1
	IFF_BROADCAST   = 0x2
	IFF_LOOPBACK    = 0x8
	IFF_POINTOPOINT = 0x10
	IFF_RUNNING     = 0x40
	IFF_MULTICAST   = 0x1000
)

const afPacket = 17

func flagsOf(f net.Flags) uint32 {
	var r uint32
	for _, m := range []struct {
		g net.Flags
		c uint32
	}{
		{net.FlagUp, IFF_UP}, {net.FlagBroadcast, IFF_BROADCAST}, {net.FlagLoopback, IFF_LOOPBACK},
		{net.FlagPointToPoint, IFF_POINTOPOINT}, {net.FlagMulticast, IFF_MULTICAST}, {net.FlagRunning, IFF_RUNNING},
	} {
		if f&m.g != 0 {
			r |= m.c
		}
	}
	return r
}

// An entry is one struct ifaddrs: the interface, and its address,
// netmask and broadcast address (sockaddrs), if any.
type entry struct {
	ifi               net.Interface
	addr, mask, broad []byte
}

// linkAddr is Linux's AF_PACKET sockaddr_ll of an interface.
func linkAddr(ifi net.Interface) []byte {
	b := make([]byte, 20)
	b[0] = afPacket
	i := uint32(ifi.Index)
	b[4], b[5], b[6], b[7] = byte(i), byte(i>>8), byte(i>>16), byte(i>>24)
	hatype := uint16(1) // ARPHRD_ETHER
	if ifi.Flags&net.FlagLoopback != 0 {
		hatype = 772 // ARPHRD_LOOPBACK
	} else if len(ifi.HardwareAddr) == 0 {
		hatype = 65534 // ARPHRD_NONE
	}
	b[8], b[9] = byte(hatype), byte(hatype>>8)
	n := min(len(ifi.HardwareAddr), 8)
	b[11] = byte(len(ifi.HardwareAddr))
	if ifi.Flags&net.FlagLoopback != 0 {
		b[11] = 6
	}
	copy(b[12:12+n], ifi.HardwareAddr)
	return b
}

func sockaddr(ip netip.Addr, zone string) []byte {
	a := &net.TCPAddr{IP: ip.AsSlice(), Zone: zone}
	fam := int32(socket.AF_INET)
	if !ip.Is4() {
		fam = socket.AF_INET6
	}
	return socket.Encode(a, fam)
}

// Getifaddrs is getifaddrs: on Linux, as glibc's, first an AF_PACKET
// entry of each interface (with no statistics in ifa_data), then the
// IPv4 addresses, then the IPv6 ones; elsewhere only the addresses.
// ifa_flags has the flags Go knows: IFF_UP, IFF_BROADCAST,
// IFF_LOOPBACK, IFF_POINTOPOINT, IFF_MULTICAST and IFF_RUNNING.
func Getifaddrs(ifap *c23.Void) int32 {
	ifs, err := net.Interfaces()
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	var links, v4, v6 []entry
	for _, ifi := range ifs {
		if runtime.GOOS == "linux" {
			links = append(links, entry{ifi: ifi, addr: linkAddr(ifi)})
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			ones, bits := ipn.Mask.Size()
			if ip.Is4() {
				if bits != 32 {
					ones = 32
				}
				pfx := netip.PrefixFrom(ip, ones).Masked()
				mask := netip.PrefixFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), ones).Masked().Addr()
				e := entry{ifi: ifi, addr: sockaddr(ip, ""), mask: sockaddr(mask, "")}
				if ifi.Flags&net.FlagBroadcast != 0 && ones < 32 {
					b := pfx.Addr().As4()
					m := mask.As4()
					for i := range b {
						b[i] |= ^m[i]
					}
					e.broad = sockaddr(netip.AddrFrom4(b), "")
				}
				v4 = append(v4, e)
				continue
			}
			if bits != 128 {
				ones = 128
			}
			var all [16]byte
			for i := range all {
				all[i] = 0xff
			}
			mask := netip.PrefixFrom(netip.AddrFrom16(all), ones).Masked().Addr()
			zone := ""
			if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				zone = ifi.Name
			}
			v6 = append(v6, entry{ifi: ifi, addr: sockaddr(ip, zone), mask: sockaddr(mask, "")})
		}
	}
	ws := int(c23.WordSize)
	// struct ifaddrs: ifa_next, ifa_name, ifa_flags, ifa_addr,
	// ifa_netmask, ifa_ifu, ifa_data; the sockaddrs follow it, 28 bytes
	// each.
	size := 7 * ws
	var head, tail *c23.Void
	for _, e := range append(append(links, v4...), v6...) {
		p := c23.Malloc(uint64(size + 3*28))
		c23.Memset(p, 0, uint64(size+3*28))
		c23.StorePtr(c23.Add(p, ws), c23.CString(e.ifi.Name))
		c23.Store32(c23.Add(p, 2*ws), flagsOf(e.ifi.Flags))
		for i, sa := range [][]byte{e.addr, e.mask, e.broad} {
			if sa == nil {
				continue
			}
			q := c23.Add(p, size+28*i)
			copy(c23.Writable(q, uintptr(len(sa))), sa)
			c23.StorePtr(c23.Add(p, (3+i)*ws), q)
		}
		if head == nil {
			head = p
		} else {
			c23.StorePtr(tail, p)
		}
		tail = p
	}
	c23.StorePtr(ifap, head)
	return 0
}

// Freeifaddrs is freeifaddrs: the collector frees the list.
func Freeifaddrs(ifa *c23.Void) {}
