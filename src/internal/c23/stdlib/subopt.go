// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	c23_string "internal/c23/string"
)

// Getsubopt is getsubopt, as glibc's: it matches the suboption at
// *optionp, up to a comma (which it replaces with a null) and an equals
// sign, with the tokens, and returns the index of the token, with the
// value after the equals sign (or null) at *valuep. For a suboption that
// is no token's it returns -1 and the suboption at *valuep.
func Getsubopt(optionp, tokens, valuep *c23.Void) int32 {
	opt := c23.LoadPtr(optionp)
	if c23.Load8(opt) == 0 {
		return -1
	}
	n := 0
	for c := c23.Load8(opt); c != 0 && c != ','; c = c23.Load8(c23.Add(opt, n)) {
		n++
	}
	end := c23.Add(opt, n)
	k := 0
	for k < n && c23.Load8(c23.Add(opt, k)) != '=' {
		k++
	}
	key := string(c23.BytesAt(opt, uintptr(k)))
	next := end
	if c23.Load8(end) != 0 {
		c23.Store8(end, 0)
		next = c23.Add(end, 1)
	}
	ws := int(c23.WordSize)
	for i := 0; ; i++ {
		t := c23.LoadPtr(c23.Add(tokens, i*ws))
		if c23.Addr(t) == 0 {
			break
		}
		if c23.GoString(t) == key {
			if k < n {
				c23.StorePtr(valuep, c23.Add(opt, k+1))
			} else {
				c23.StorePtr(valuep, (*c23.Void)(nil))
			}
			c23.StorePtr(optionp, next)
			return int32(i)
		}
	}
	c23.StorePtr(valuep, opt)
	c23.StorePtr(optionp, next)
	return -1
}

// Mkostemp is mkostemp: mkstemp, opening the file with flags (O_APPEND,
// O_CLOEXEC, O_SYNC) too.
func Mkostemp(template *c23.Void, flags int32) int32 { return Mkostemps(template, 0, flags) }

// Mkstemps is mkstemps: mkstemp of a template whose XXXXXX is followed
// by suffixlen bytes.
func Mkstemps(template *c23.Void, suffixlen int32) int32 { return Mkostemps(template, suffixlen, 0) }

// Mkostemps is mkostemps.
func Mkostemps(template *c23.Void, suffixlen, flags int32) int32 {
	n := int64(c23_string.Strlen(template))
	if suffixlen < 0 || n < 6+int64(suffixlen) ||
		string(c23.BytesAt(c23.Add(template, int(n)-int(suffixlen)-6), 6)) != "XXXXXX" ||
		flags&^(c23.O_APPEND|c23.O_CLOEXEC|c23.O_SYNC|c23.O_DSYNC|c23.O_LARGEFILE) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	x6 := c23.Add(template, int(n)-int(suffixlen)-6)
	const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for range 238328 { // TMP_MAX
		x := c23.RuntimeRand()
		for i := range 6 {
			c23.Store8(c23.Add(x6, i), digits[x%62])
			x /= 62
		}
		fd := c23.Open(c23.GoString(template), c23.O_RDWR|c23.O_CREAT|c23.O_EXCL|flags, 0o600)
		if fd >= 0 || c23.Errno() != c23.EEXIST {
			return fd
		}
	}
	c23.SetErrno(c23.EEXIST)
	return -1
}

// The functions of a locale (glibc's _l functions), which are the
// global locale's: the library's locales have the same radix character.

func StrtodL(s, end, _ *c23.Void) float64 { return Strtod(s, end) }

func StrtofL(s, end, _ *c23.Void) float32 { return Strtof(s, end) }

func StrtolL(s, end *c23.Void, base int32, _ *c23.Void) int64 { return Strtol(s, end, base) }

func StrtoulL(s, end *c23.Void, base int32, _ *c23.Void) uint64 { return Strtoul(s, end, base) }

// StrtolL32 and StrtoulL32 are strtol_l and strtoul_l where long is 32
// bits (cmd/cc's libcLong32).
func StrtolL32(s, end *c23.Void, base int32, _ *c23.Void) int64 { return c23.Strtol32(s, end, base) }

func StrtoulL32(s, end *c23.Void, base int32, _ *c23.Void) uint64 {
	return c23.Strtoul32(s, end, base)
}
