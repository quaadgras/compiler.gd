// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package netdb

import (
	"bufio"
	"internal/c23"
	in "internal/c23/netinet/in"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
)

// The databases of hosts, networks, protocols and services, read entry
// by entry (gethostent and the others) from /etc/hosts, /etc/networks,
// /etc/protocols and /etc/services. Each is read when first asked for
// after its set function, which rewinds it; its end function does too.
// Where there is no /etc/protocols, the protocols are the table of
// getprotobyname; where there is no /etc/services, the services are the
// few that Go's net package knows without one.

// table is the lines of a database's file, as their fields (with no
// comment), or nil and false if there is no file.
func table(name string) ([][]string, bool) {
	f, err := os.Open(name)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var t [][]string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if fs := strings.Fields(line); len(fs) >= 2 {
			t = append(t, fs)
		}
	}
	return t, true
}

// A db is a database read entry by entry.
type db struct {
	mu   sync.Mutex
	ents [][]string
	read bool
	next int
	load func() [][]string
	ent  *c23.Void // the struct its function returns
}

func (d *db) rewind() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.read, d.next, d.ents = false, 0, nil
}

// each calls f with the entries from the next on, until it returns true;
// the next is then the one after.
func (d *db) each(f func(e []string) bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.read {
		d.ents, d.read, d.next = d.load(), true, 0
	}
	for d.next < len(d.ents) {
		e := d.ents[d.next]
		d.next++
		if f(e) {
			return true
		}
	}
	return false
}

// search calls f with every entry, from the first, until it returns
// true.
func (d *db) search(f func(e []string) bool) bool {
	d.mu.Lock()
	ents := d.ents
	if !d.read {
		ents = d.load()
	}
	d.mu.Unlock()
	for _, e := range ents {
		if f(e) {
			return true
		}
	}
	return false
}

// strArray is a null-terminated array of C strings.
func strArray(ss []string) *c23.Void {
	a := c23.Malloc(uint64((len(ss) + 1) * ws))
	for i, s := range ss {
		c23.StorePtr(c23.Add(a, i*ws), c23.CString(s))
	}
	c23.StorePtr(c23.Add(a, len(ss)*ws), (*c23.Void)(nil))
	return a
}

// hosts: lines of an address, a name and its aliases.

var hostDB = db{load: func() [][]string {
	var t [][]string
	lines, _ := table("/etc/hosts")
	for _, l := range lines {
		if _, err := netip.ParseAddr(l[0]); err == nil {
			t = append(t, l)
		}
	}
	return t
}}

// Gethostent is gethostent: the next host of /etc/hosts (one per line,
// with its one address), or null at the end.
func Gethostent() *c23.Void {
	var r *c23.Void
	hostDB.each(func(e []string) bool {
		ip, _ := netip.ParseAddr(e[0])
		ip = ip.Unmap()
		family, a := int32(AF_INET6), ip.AsSlice()
		if ip.Is4() {
			family = AF_INET
		}
		r = newHostent(e[1], family, [][]byte{a})
		c23.StorePtr(c23.Add(r, ws), strArray(e[2:]))
		return true
	})
	return r
}

// Sethostent is sethostent.
func Sethostent(stayopen int32) { hostDB.rewind() }

// Endhostent is endhostent.
func Endhostent() { hostDB.rewind() }

// networks: lines of a name, a number and aliases. struct netent {char
// *n_name; char **n_aliases; int n_addrtype; uint32_t n_net;}.

var netDB = db{load: func() [][]string {
	var t [][]string
	lines, _ := table("/etc/networks")
	for _, l := range lines {
		if _, ok := netNumber(l[1]); ok {
			t = append(t, l)
		}
	}
	return t
}}

// netNumber is inet_network of s: up to four numbers of a byte, the
// first the most significant.
func netNumber(s string) (uint32, bool) {
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return 0, false
	}
	var n uint32
	for _, p := range parts {
		v, err := strconv.ParseUint(p, 0, 8)
		if err != nil {
			return 0, false
		}
		n = n<<8 | uint32(v)
	}
	return n, true
}

func netResult(e []string) *c23.Void {
	if Addr(netDB.ent) == 0 {
		netDB.ent = c23.Malloc(uint64(2*ws + 8))
	}
	p := netDB.ent
	n, _ := netNumber(e[1])
	c23.StorePtr(p, c23.CString(e[0]))
	c23.StorePtr(c23.Add(p, ws), strArray(e[2:]))
	c23.Store32(c23.Add(p, 2*ws), AF_INET)
	c23.Store32(c23.Add(p, 2*ws+4), n)
	return p
}

// Getnetent is getnetent: the next network of /etc/networks, or null.
func Getnetent() *c23.Void {
	var r *c23.Void
	netDB.each(func(e []string) bool { r = netResult(e); return true })
	return r
}

// Getnetbyname is getnetbyname: the network of the name or alias.
func Getnetbyname(name *c23.Void) *c23.Void {
	n := c23.GoString(name)
	var r *c23.Void
	netDB.search(func(e []string) bool {
		if e[0] == n || contains(e[2:], n) {
			r = netResult(e)
			return true
		}
		return false
	})
	return r
}

// Getnetbyaddr is getnetbyaddr: the network of the number, of AF_INET.
func Getnetbyaddr(net uint32, typ int32) *c23.Void {
	if typ != AF_INET {
		return (*c23.Void)(nil)
	}
	var r *c23.Void
	netDB.search(func(e []string) bool {
		if n, _ := netNumber(e[1]); n == net {
			r = netResult(e)
			return true
		}
		return false
	})
	return r
}

// Setnetent is setnetent.
func Setnetent(stayopen int32) { netDB.rewind() }

// Endnetent is endnetent.
func Endnetent() { netDB.rewind() }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// protocols: lines of a name, a number and aliases.

var protoDB = db{load: func() [][]string {
	lines, ok := table("/etc/protocols")
	if !ok {
		for _, p := range protocols {
			lines = append(lines, []string{p.name, strconv.Itoa(int(p.proto))})
		}
	}
	var t [][]string
	for _, l := range lines {
		if _, err := strconv.ParseUint(l[1], 10, 8); err == nil {
			t = append(t, l)
		}
	}
	return t
}}

// Getprotoent is getprotoent: the next protocol of /etc/protocols (or
// getprotobyname's table), or null.
func Getprotoent() *c23.Void {
	var r *c23.Void
	protoDB.each(func(e []string) bool {
		n, _ := strconv.Atoi(e[1])
		r = protoResult(e[0], int32(n))
		c23.StorePtr(c23.Add(r, ws), strArray(e[2:]))
		return true
	})
	return r
}

// Setprotoent is setprotoent.
func Setprotoent(stayopen int32) { protoDB.rewind() }

// Endprotoent is endprotoent.
func Endprotoent() { protoDB.rewind() }

// services: lines of a name, a port/protocol and aliases. struct servent
// {char *s_name; char **s_aliases; int s_port; char *s_proto;}, the port
// in network order.

// goServices are the services Go's net package knows where there is no
// /etc/services.
var goServices = [][]string{
	{"domain", "53/tcp"}, {"domain", "53/udp"}, {"ftp", "21/tcp"}, {"ftps", "990/tcp"},
	{"gopher", "70/tcp"}, {"http", "80/tcp"}, {"https", "443/tcp"}, {"imap2", "143/tcp"},
	{"imap3", "220/tcp"}, {"imaps", "993/tcp"}, {"pop3", "110/tcp"}, {"pop3s", "995/tcp"},
	{"smtp", "25/tcp"}, {"submissions", "465/tcp"}, {"ssh", "22/tcp"}, {"telnet", "23/tcp"},
}

var servDB = db{load: func() [][]string {
	lines, ok := table("/etc/services")
	if !ok {
		lines = goServices
	}
	var t [][]string
	for _, l := range lines {
		if _, _, ok := servPort(l[1]); ok {
			t = append(t, l)
		}
	}
	return t
}}

// servPort is the port and protocol of a service's "port/protocol".
func servPort(s string) (uint16, string, bool) {
	port, proto, ok := strings.Cut(s, "/")
	n, err := strconv.ParseUint(port, 10, 16)
	return uint16(n), proto, ok && err == nil && proto != ""
}

func servResult(e []string) *c23.Void {
	if Addr(servDB.ent) == 0 {
		servDB.ent = c23.Malloc(uint64(4 * ws))
	}
	p := servDB.ent
	port, proto, _ := servPort(e[1])
	c23.StorePtr(p, c23.CString(e[0]))
	c23.StorePtr(c23.Add(p, ws), strArray(e[2:]))
	c23.Store32(c23.Add(p, 2*ws), uint32(in.Htons(port)))
	c23.StorePtr(c23.Add(p, 3*ws), c23.CString(proto))
	return p
}

// Getservent is getservent: the next service of /etc/services, or null.
func Getservent() *c23.Void {
	var r *c23.Void
	servDB.each(func(e []string) bool { r = servResult(e); return true })
	return r
}

// Getservbyport is getservbyport: the service of the port (in network
// order) and the protocol (any if null).
func Getservbyport(port int32, proto *c23.Void) *c23.Void {
	want := in.Ntohs(uint16(port))
	p := ""
	if Addr(proto) != 0 {
		p = c23.GoString(proto)
	}
	var r *c23.Void
	servDB.search(func(e []string) bool {
		n, pr, _ := servPort(e[1])
		if n == want && (p == "" || pr == p) {
			r = servResult(e)
			return true
		}
		return false
	})
	return r
}

// Setservent is setservent.
func Setservent(stayopen int32) { servDB.rewind() }

// Endservent is endservent.
func Endservent() { servDB.rewind() }
