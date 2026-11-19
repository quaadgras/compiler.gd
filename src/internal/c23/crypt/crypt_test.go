// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crypt

import "testing"

func TestHash(t *testing.T) {
	for _, tt := range []struct{ key, salt, want string }{
		// The specifications' examples.
		{"Hello world!", "$5$saltstring", "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
		{"Hello world!", "$5$rounds=10000$saltstringsaltstring", "$5$rounds=10000$saltstringsaltst$3xv.VbSHBb41AL9AvLeujZkZRBAwqFMz2.opqey6IcA"},
		{"Hello world!", "$6$saltstring", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"we have a short salt string but not a short password", "$6$rounds=77777$short", "$6$rounds=77777$short$WuQyW2YR.hBNpjjRhpYD/ifIw05xdfeEyQoMxIXbkvr0gge1a1x3yRULJ5CCaUeOxFmtlcGZelFl5CxtgfiAc0"},
		{"the minimum number is still observed", "$5$rounds=10$roundstoolow", "$5$rounds=1000$roundstoolow$yfvwcWrQ8l/K0DAWyuPMDNHpIVlTQebY9l/gL972bIC"},
		// musl's.
		{"password", "$1$saltsalt$", "$1$saltsalt$qjXMvbEw8oaL.CzflDtaK/"},
		{"password", "ab", "abJnggxhB/yWI"},
	} {
		got, ok := Hash(tt.key, tt.salt)
		if !ok || got != tt.want {
			t.Errorf("Hash(%q, %q) = %q, %v; want %q", tt.key, tt.salt, got, ok, tt.want)
		}
	}
}

func TestDES(t *testing.T) {
	d := newDES(0x133457799BBCDFF1)
	c := d.crypt(0x0123456789ABCDEF, 0, false)
	if c != 0x85E813540F0AB405 {
		t.Errorf("DES = %016x, want 85e813540f0ab405", c)
	}
	if p := d.crypt(c, 0, true); p != 0x0123456789ABCDEF {
		t.Errorf("DES decrypts to %016x", p)
	}
}
