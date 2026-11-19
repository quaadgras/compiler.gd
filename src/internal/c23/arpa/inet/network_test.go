// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inet

import "testing"

// glibc's values (musl's inet_network is inet_addr's, and its inet_lnaof
// and inet_makeaddr do not convert the byte order).
func TestNetwork(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want uint32
	}{
		{"10.1.2.3", 0x0a010203}, {"0x7f.1", 0x7f01}, {"192.168", 0xc0a8}, {"127", 0x7f},
		{"010.0x10.3", 0x081003}, {"1.2.3.4.5", 0xffffffff}, {"", 0xffffffff}, {"256.1", 0xffffffff},
		{"09", 0xffffffff}, {"1.2.3.", 0xffffffff}, {"1.2 ", 0x0102}, {"0", 0},
	} {
		if got := inetNetwork(tt.s); got != tt.want {
			t.Errorf("inet_network(%q) = %#x, want %#x", tt.s, got, tt.want)
		}
	}
	net := func(a, b, c, d byte) uint32 { // s_addr of a.b.c.d
		return ntohl(uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d))
	}
	for _, tt := range []struct {
		a          uint32
		lna, netof uint32
	}{
		{net(10, 1, 2, 3), 0x010203, 10},
		{net(172, 16, 5, 6), 0x0506, 0xac10},
		{net(192, 168, 1, 2), 2, 0xc0a801},
	} {
		if got := InetLnaof(tt.a); got != tt.lna {
			t.Errorf("inet_lnaof(%#x) = %#x, want %#x", tt.a, got, tt.lna)
		}
		if got := InetNetof(tt.a); got != tt.netof {
			t.Errorf("inet_netof(%#x) = %#x, want %#x", tt.a, got, tt.netof)
		}
	}
}
