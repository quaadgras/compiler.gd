// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package netdb is netdb.h's functions for cmd/cc: names and services,
// with Go's resolver.
package netdb

import (
	"context"
	"errors"
	"internal/c23"
	inet "internal/c23/arpa/inet"
	in "internal/c23/netinet/in"
	socket "internal/c23/sys/socket"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
)

type Ptr = c23.Ptr

var ws = int(c23.WordSize)

func Addr(p Ptr) uintptr { return c23.Addr(p) }

// The values of sys/socket.h and netinet/in.h that netdb.h's use.
const (
	AF_UNSPEC   = socket.AF_UNSPEC
	AF_INET     = socket.AF_INET
	AF_INET6    = socket.AF_INET6
	SOCK_STREAM = socket.SOCK_STREAM
	SOCK_DGRAM  = socket.SOCK_DGRAM
	SOCK_RAW    = socket.SOCK_RAW
	IPPROTO_TCP = socket.IPPROTO_TCP
	IPPROTO_UDP = socket.IPPROTO_UDP
)

// netdb.h's values: glibc's (and musl's).
const (
	AI_PASSIVE     = 0x1
	AI_CANONNAME   = 0x2
	AI_NUMERICHOST = 0x4
	AI_V4MAPPED    = 0x8
	AI_ALL         = 0x10
	AI_ADDRCONFIG  = 0x20
	AI_NUMERICSERV = 0x400

	NI_NUMERICHOST = 1
	NI_NUMERICSERV = 2
	NI_NOFQDN      = 4
	NI_NAMEREQD    = 8
	NI_DGRAM       = 16

	EAI_BADFLAGS = -1
	EAI_NONAME   = -2
	EAI_AGAIN    = -3
	EAI_FAIL     = -4
	EAI_FAMILY   = -6
	EAI_SOCKTYPE = -7
	EAI_SERVICE  = -8
	EAI_MEMORY   = -10
	EAI_SYSTEM   = -11
	EAI_OVERFLOW = -12

	HOST_NOT_FOUND = 1
	TRY_AGAIN      = 2
	NO_RECOVERY    = 3
	NO_DATA        = 4
)

// struct addrinfo {int ai_flags, ai_family, ai_socktype, ai_protocol;
// socklen_t ai_addrlen; struct sockaddr *ai_addr; char *ai_canonname;
// struct addrinfo *ai_next;}: the pointers from 20 rounded up to a word.
var (
	aiAddr  = (20 + ws - 1) / ws * ws
	aiCanon = aiAddr + ws
	aiNext  = aiCanon + ws
	aiSize  = aiNext + ws
)

// Getaddrinfo is getaddrinfo, with Go's resolver (the hosts file, then
// DNS). Each result is one allocation, its address in it.
func Getaddrinfo(node, service, hints, res Ptr) int32 {
	var flags, family, socktype, proto int32
	if Addr(hints) != 0 {
		flags = int32(c23.Load32(hints))
		family = int32(c23.Load32(c23.Add(hints, 4)))
		socktype = int32(c23.Load32(c23.Add(hints, 8)))
		proto = int32(c23.Load32(c23.Add(hints, 12)))
	}
	if flags&^(AI_PASSIVE|AI_CANONNAME|AI_NUMERICHOST|AI_V4MAPPED|AI_ALL|AI_ADDRCONFIG|AI_NUMERICSERV) != 0 {
		return EAI_BADFLAGS
	}
	if family != AF_UNSPEC && family != AF_INET && family != AF_INET6 {
		return EAI_FAMILY
	}
	if socktype != 0 && socktype != SOCK_STREAM && socktype != SOCK_DGRAM && socktype != SOCK_RAW {
		return EAI_SOCKTYPE
	}
	if Addr(node) == 0 && Addr(service) == 0 {
		return EAI_NONAME
	}

	// The kinds of socket: one result of each per address.
	type kind struct{ typ, proto int32 }
	var kinds []kind
	for _, k := range []kind{{SOCK_STREAM, IPPROTO_TCP}, {SOCK_DGRAM, IPPROTO_UDP}, {SOCK_RAW, 0}} {
		if (socktype == 0 && k.typ != SOCK_RAW || socktype == k.typ) && (proto == 0 || proto == k.proto) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return EAI_SOCKTYPE
	}

	port := 0
	if Addr(service) != 0 {
		s := c23.GoString(service)
		if n, err := strconv.ParseUint(s, 10, 16); err == nil {
			port = int(n)
		} else if flags&AI_NUMERICSERV != 0 {
			return EAI_NONAME
		} else {
			network := "tcp"
			if socktype == SOCK_DGRAM {
				network = "udp"
			}
			n, err := net.LookupPort(network, s)
			if err != nil {
				return EAI_SERVICE
			}
			port = n
		}
	}

	var ips []netip.Addr
	var canon string
	if Addr(node) == 0 {
		if flags&AI_PASSIVE != 0 {
			ips = []netip.Addr{netip.IPv6Unspecified(), netip.IPv4Unspecified()}
		} else {
			ips = []netip.Addr{netip.IPv6Loopback(), netip.AddrFrom4([4]byte{127, 0, 0, 1})}
		}
	} else {
		host := c23.GoString(node)
		canon = host
		if ip, err := netip.ParseAddr(host); err == nil {
			ips = []netip.Addr{ip}
		} else if ip, ok := inet.ParseInet4(host); ok {
			ips = []netip.Addr{ip}
		} else if flags&AI_NUMERICHOST != 0 {
			return EAI_NONAME
		} else {
			addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
			if err != nil {
				return gaiErr(err)
			}
			ips = addrs
			if flags&AI_CANONNAME != 0 {
				if c, err := net.LookupCNAME(host); err == nil && c != "" {
					canon = strings.TrimSuffix(c, ".")
				}
			}
		}
	}

	var head, tail Ptr
	n := 0
	for _, ip := range ips {
		ip = ip.Unmap()
		fam := int32(AF_INET)
		if ip.Is6() {
			fam = AF_INET6
		}
		if family != AF_UNSPEC && fam != family {
			if family == AF_INET6 && fam == AF_INET && flags&AI_V4MAPPED != 0 {
				ip = netip.AddrFrom16(ip.As16())
				fam = AF_INET6
			} else {
				continue
			}
		}
		sa := socket.Encode(net.TCPAddrFromAddrPort(netip.AddrPortFrom(ip, uint16(port))), fam)
		for _, k := range kinds {
			ai := c23.Malloc(uint64(aiSize + 28))
			c23.Memset(ai, 0, uint64(aiSize+28))
			c23.Store32(ai, uint32(flags))
			c23.Store32(c23.Add(ai, 4), uint32(fam))
			c23.Store32(c23.Add(ai, 8), uint32(k.typ))
			c23.Store32(c23.Add(ai, 12), uint32(k.proto))
			c23.Store32(c23.Add(ai, 16), uint32(len(sa)))
			addr := c23.Add(ai, aiSize)
			copy(c23.BytesAt(addr, uintptr(len(sa))), sa)
			c23.StorePtr(c23.Add(ai, aiAddr), addr)
			if n == 0 && flags&AI_CANONNAME != 0 && canon != "" {
				c23.StorePtr(c23.Add(ai, aiCanon), c23.CString(canon))
			}
			if n == 0 {
				head = ai
			} else {
				c23.StorePtr(c23.Add(tail, aiNext), ai)
			}
			tail = ai
			n++
		}
	}
	if n == 0 {
		return EAI_NONAME
	}
	c23.StorePtr(res, head)
	return 0
}

func gaiErr(err error) int32 {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		switch {
		case dns.IsNotFound:
			return EAI_NONAME
		case dns.IsTemporary || dns.IsTimeout:
			return EAI_AGAIN
		}
		return EAI_FAIL
	}
	return EAI_NONAME
}

// Freeaddrinfo is freeaddrinfo.
func Freeaddrinfo(ai Ptr) {
	for Addr(ai) != 0 {
		next := c23.LoadPtr(c23.Add(ai, aiNext))
		if c := c23.LoadPtr(c23.Add(ai, aiCanon)); Addr(c) != 0 {
			c23.Free(c)
		}
		c23.Free(ai)
		ai = next
	}
}

var gaiText = map[int32]string{
	EAI_BADFLAGS: "Bad value for ai_flags",
	EAI_NONAME:   "Name or service not known",
	EAI_AGAIN:    "Temporary failure in name resolution",
	EAI_FAIL:     "Non-recoverable failure in name resolution",
	EAI_FAMILY:   "ai_family not supported",
	EAI_SOCKTYPE: "ai_socktype not supported",
	EAI_SERVICE:  "Servname not supported for ai_socktype",
	EAI_MEMORY:   "Memory allocation failure",
	EAI_SYSTEM:   "System error",
	EAI_OVERFLOW: "Argument buffer overflow",
}

var (
	textMu  sync.Mutex
	textMap = map[string]Ptr{}
)

// static returns a C string of s that lives as long as the program, one
// per string.
func static(s string) Ptr {
	textMu.Lock()
	defer textMu.Unlock()
	p, ok := textMap[s]
	if !ok {
		p = c23.CString(s)
		textMap[s] = p
	}
	return p
}

// GaiStrerror is gai_strerror: glibc's messages.
func GaiStrerror(e int32) Ptr {
	if s, ok := gaiText[e]; ok {
		return static(s)
	}
	return static("Unknown error")
}

// Getnameinfo is getnameinfo. The service is the port's number: Go
// cannot find a port's name.
func Getnameinfo(addr Ptr, alen uint32, host Ptr, hlen uint32, serv Ptr, slen uint32, flags int32) int32 {
	if alen < 2 {
		return EAI_FAMILY
	}
	fam := int32(c23.Load16(addr))
	if fam != AF_INET && fam != AF_INET6 {
		return EAI_FAMILY
	}
	a, e := socket.Decode(fam, SOCK_STREAM, addr, alen)
	if e != 0 {
		return EAI_FAMILY
	}
	ap := a.(*net.TCPAddr).AddrPort()
	if Addr(host) != 0 && hlen > 0 {
		h := ap.Addr().Unmap().String()
		if flags&NI_NUMERICHOST == 0 {
			if names, err := net.LookupAddr(h); err == nil && len(names) > 0 {
				h = strings.TrimSuffix(names[0], ".")
				if flags&NI_NOFQDN != 0 {
					h, _, _ = strings.Cut(h, ".")
				}
			} else if flags&NI_NAMEREQD != 0 {
				return EAI_NONAME
			}
		}
		if !putString(host, hlen, h) {
			return EAI_OVERFLOW
		}
	}
	if Addr(serv) != 0 && slen > 0 {
		if !putString(serv, slen, strconv.Itoa(int(ap.Port()))) {
			return EAI_OVERFLOW
		}
	}
	return 0
}

// putString stores s and its NUL in the n bytes at p, if it fits.
func putString(p Ptr, n uint32, s string) bool {
	if uint32(len(s)) >= n {
		return false
	}
	copy(c23.BytesAt(p, uintptr(len(s)+1)), s+"\x00")
	return true
}

// gethostbyname and its relatives: struct hostent {char *h_name; char
// **h_aliases; int h_addrtype; int h_length; char **h_addr_list;}, in
// storage of the library's, as C has it. h_errno is the thread's.

var (
	hostMu  sync.Mutex
	hostent Ptr
	hErrno  = c23.NewTLS(func() Ptr { return c23.Malloc(4) })
)

// HErrnoLocation is where h_errno is.
func HErrnoLocation() Ptr { return hErrno.Get() }

// SetHErrno sets h_errno (resolv.h's functions' too).
func SetHErrno(e int32) { c23.Store32(hErrno.Get(), uint32(e)) }

// Gethostbyname is gethostbyname: IPv4 addresses.
func Gethostbyname(name Ptr) Ptr { return Gethostbyname2(name, AF_INET) }

// Gethostbyname2 is gethostbyname2.
func Gethostbyname2(name Ptr, family int32) Ptr {
	host := c23.GoString(name)
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else if ip, ok := inet.ParseInet4(host); ok {
		ips = []netip.Addr{ip}
	} else {
		addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
		if err != nil {
			if gaiErr(err) == EAI_AGAIN {
				SetHErrno(TRY_AGAIN)
			} else {
				SetHErrno(HOST_NOT_FOUND)
			}
			return Ptr{}
		}
		ips = addrs
	}
	var bs [][]byte
	for _, ip := range ips {
		ip = ip.Unmap()
		if family == AF_INET && ip.Is4() {
			a := ip.As4()
			bs = append(bs, a[:])
		} else if family == AF_INET6 && ip.Is6() {
			a := ip.As16()
			bs = append(bs, a[:])
		}
	}
	if len(bs) == 0 {
		SetHErrno(NO_DATA)
		return Ptr{}
	}
	return newHostent(host, family, bs)
}

// Gethostbyaddr is gethostbyaddr.
func Gethostbyaddr(addr Ptr, n uint32, family int32) Ptr {
	var ip netip.Addr
	switch {
	case family == AF_INET && n == 4:
		ip = netip.AddrFrom4([4]byte(c23.BytesAt(addr, 4)))
	case family == AF_INET6 && n == 16:
		ip = netip.AddrFrom16([16]byte(c23.BytesAt(addr, 16)))
	default:
		SetHErrno(NO_RECOVERY)
		return Ptr{}
	}
	names, err := net.LookupAddr(ip.String())
	if err != nil || len(names) == 0 {
		SetHErrno(HOST_NOT_FOUND)
		return Ptr{}
	}
	return newHostent(strings.TrimSuffix(names[0], "."), family, [][]byte{c23.BytesAt(addr, uintptr(n))})
}

func newHostent(name string, family int32, addrs [][]byte) Ptr {
	hostMu.Lock()
	defer hostMu.Unlock()
	if Addr(hostent) == 0 {
		hostent = c23.Malloc(uint64(3*ws + 8 + ws))
	}
	h := hostent
	c23.StorePtr(h, c23.CString(name))
	aliases := c23.Malloc(uint64(ws))
	c23.StorePtr(aliases, Ptr{})
	c23.StorePtr(c23.Add(h, ws), aliases)
	c23.Store32(c23.Add(h, 2*ws), uint32(family))
	c23.Store32(c23.Add(h, 2*ws+4), uint32(len(addrs[0])))
	list := c23.Malloc(uint64((len(addrs) + 1) * ws))
	for i, a := range addrs {
		p := c23.Malloc(uint64(len(a)))
		copy(c23.BytesAt(p, uintptr(len(a))), a)
		c23.StorePtr(c23.Add(list, i*ws), p)
	}
	c23.StorePtr(c23.Add(list, len(addrs)*ws), Ptr{})
	c23.StorePtr(c23.Add(h, 2*ws+8), list)
	return h
}

// HstrError is hstrerror.
func HstrError(e int32) Ptr {
	switch e {
	case HOST_NOT_FOUND:
		return static("Unknown host")
	case TRY_AGAIN:
		return static("Host name lookup failure")
	case NO_RECOVERY:
		return static("Unknown server error")
	case NO_DATA:
		return static("No address associated with name")
	}
	return static("Unknown resolver error")
}

// getservbyname: struct servent {char *s_name; char **s_aliases; int
// s_port; char *s_proto;}, the port in network order.

var servent Ptr

// Getservbyname is getservbyname.
func Getservbyname(name, proto Ptr) Ptr {
	s := c23.GoString(name)
	p := "tcp"
	if Addr(proto) != 0 {
		p = c23.GoString(proto)
	}
	port, err := net.LookupPort(p, s)
	if err != nil {
		return Ptr{}
	}
	hostMu.Lock()
	defer hostMu.Unlock()
	if Addr(servent) == 0 {
		servent = c23.Malloc(uint64(4 * ws))
		aliases := c23.Malloc(uint64(ws))
		c23.StorePtr(aliases, Ptr{})
		c23.StorePtr(c23.Add(servent, ws), aliases)
	}
	c23.StorePtr(servent, static(s))
	c23.Store32(c23.Add(servent, 2*ws), uint32(in.Htons(uint16(port))))
	c23.StorePtr(c23.Add(servent, 3*ws), static(p))
	return servent
}

// getprotobyname: struct protoent {char *p_name; char **p_aliases; int
// p_proto;}, of the protocols Go's sockets have.

var protoent Ptr

var protocols = []struct {
	name  string
	proto int32
}{{"ip", 0}, {"icmp", 1}, {"tcp", 6}, {"udp", 17}, {"ipv6", 41}, {"ipv6-icmp", 58}}

func protoResult(name string, proto int32) Ptr {
	hostMu.Lock()
	defer hostMu.Unlock()
	if Addr(protoent) == 0 {
		protoent = c23.Malloc(uint64(3 * ws))
		aliases := c23.Malloc(uint64(ws))
		c23.StorePtr(aliases, Ptr{})
		c23.StorePtr(c23.Add(protoent, ws), aliases)
	}
	c23.StorePtr(protoent, static(name))
	c23.Store32(c23.Add(protoent, 2*ws), uint32(proto))
	return protoent
}

// Getprotobyname is getprotobyname.
func Getprotobyname(name Ptr) Ptr {
	n := c23.GoString(name)
	for _, p := range protocols {
		if p.name == n {
			return protoResult(p.name, p.proto)
		}
	}
	return Ptr{}
}

// Getprotobynumber is getprotobynumber.
func Getprotobynumber(proto int32) Ptr {
	for _, p := range protocols {
		if p.proto == proto {
			return protoResult(p.name, p.proto)
		}
	}
	return Ptr{}
}
