// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package crypt is crypt.h's and unistd.h's crypt, and stdlib.h's and
// unistd.h's setkey and encrypt: the hashes of glibc's crypt, MD5-crypt
// ("$1$"), SHA-256-crypt ("$5$") and SHA-512-crypt ("$6$", both with
// "rounds="), as their specifications have them, and the traditional DES
// crypt of a salt of two characters; and DES on blocks of 64 bits, one
// bit in each byte.
package crypt

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	c23 "internal/c23"
	"strconv"
	"strings"
	"sync"
)

const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var (
	cryptMu  sync.Mutex
	cryptBuf *c23.Void // crypt's result
)

// Crypt is crypt: the hash of key that salt says, in an array the next
// call reuses; null (EINVAL) for a salt it cannot use.
func Crypt(key, salt *c23.Void) *c23.Void {
	cryptMu.Lock()
	defer cryptMu.Unlock()
	if c23.Addr(cryptBuf) == 0 {
		cryptBuf = c23.Malloc(256)
	}
	return put(cryptBuf, key, salt)
}

// CryptR is crypt_r: crypt with its result in data, a struct crypt_data
// (an int, then 256 bytes for the result).
func CryptR(key, salt, data *c23.Void) *c23.Void { return put(c23.Add(data, 4), key, salt) }

func put(buf, key, salt *c23.Void) *c23.Void {
	s, ok := Hash(c23.GoString(key), c23.GoString(salt))
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return (*c23.Void)(nil)
	}
	copy(c23.Writable(buf, uintptr(len(s)+1)), s+"\x00")
	return buf
}

// Hash is crypt's hash of key, as salt says, and whether it could.
func Hash(key, salt string) (string, bool) {
	switch {
	case strings.HasPrefix(salt, "$1$"):
		return md5Crypt(key, salt[3:]), true
	case strings.HasPrefix(salt, "$5$"):
		return shaCrypt(key, salt[3:], "$5$", sha256.New, sha256Order[:]), true
	case strings.HasPrefix(salt, "$6$"):
		return shaCrypt(key, salt[3:], "$6$", sha512.New, sha512Order[:]), true
	}
	return desCrypt(key, salt)
}

// to64 appends n characters of v's bits, the least significant first.
func to64(b []byte, v uint32, n int) []byte {
	for ; n > 0; n-- {
		b = append(b, itoa64[v&0x3f])
		v >>= 6
	}
	return b
}

// saltOf is the salt that s begins with: up to max characters, to a '$'.
func saltOf(s string, max int) string {
	if i := strings.IndexByte(s, '$'); i >= 0 {
		s = s[:i]
	}
	return s[:min(len(s), max)]
}

// md5Crypt is Poul-Henning Kamp's MD5-crypt.
func md5Crypt(key, salt string) string {
	salt = saltOf(salt, 8)
	alt := md5.Sum([]byte(key + salt + key))
	h := md5.New()
	h.Write([]byte(key + "$1$" + salt))
	for n := len(key); n > 0; n -= 16 {
		h.Write(alt[:min(n, 16)])
	}
	first := byte(0)
	if len(key) > 0 {
		first = key[0]
	}
	for i := len(key); i > 0; i >>= 1 {
		if i&1 != 0 {
			h.Write([]byte{0})
		} else {
			h.Write([]byte{first})
		}
	}
	f := h.Sum(nil)
	for i := range 1000 {
		h := md5.New()
		if i&1 != 0 {
			h.Write([]byte(key))
		} else {
			h.Write(f)
		}
		if i%3 != 0 {
			h.Write([]byte(salt))
		}
		if i%7 != 0 {
			h.Write([]byte(key))
		}
		if i&1 != 0 {
			h.Write(f)
		} else {
			h.Write([]byte(key))
		}
		f = h.Sum(nil)
	}
	b := []byte("$1$" + salt + "$")
	for _, t := range [5][3]int{{0, 6, 12}, {1, 7, 13}, {2, 8, 14}, {3, 9, 15}, {4, 10, 5}} {
		b = to64(b, uint32(f[t[0]])<<16|uint32(f[t[1]])<<8|uint32(f[t[2]]), 4)
	}
	b = to64(b, uint32(f[11]), 2)
	return string(b)
}

// The order of the bytes of SHA-crypt's hashes in its encoding: groups
// of three, and the last bytes.
var sha256Order = [...]int{
	0, 10, 20, 21, 1, 11, 12, 22, 2, 3, 13, 23, 24, 4, 14,
	15, 25, 5, 6, 16, 26, 27, 7, 17, 18, 28, 8, 9, 19, 29,
	-1, 31, 30,
}

var sha512Order = [...]int{
	0, 21, 42, 22, 43, 1, 44, 2, 23, 3, 24, 45, 25, 46, 4,
	47, 5, 26, 6, 27, 48, 28, 49, 7, 50, 8, 29, 9, 30, 51,
	31, 52, 10, 53, 11, 32, 12, 33, 54, 34, 55, 13, 56, 14, 35,
	15, 36, 57, 37, 58, 16, 59, 17, 38, 18, 39, 60, 40, 61, 19,
	62, 20, 41, -1, -1, 63,
}

// shaCrypt is Ulrich Drepper's SHA-crypt: 5000 rounds, or rounds=N
// (from 1000 to 999999999) before the salt of up to 16 characters.
func shaCrypt(key, salt, magic string, newHash func() hash.Hash, order []int) string {
	rounds, custom := 5000, false
	if rest, ok := strings.CutPrefix(salt, "rounds="); ok {
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i < len(rest) && rest[i] == '$' {
			n, err := strconv.ParseUint(rest[:i], 10, 64)
			if err != nil && i > 0 { // too large
				n = 999999999
			}
			rounds, custom = int(max(1000, min(n, 999999999))), true
			salt = rest[i+1:]
		}
	}
	salt = saltOf(salt, 16)
	sum := func(parts ...[]byte) []byte {
		h := newHash()
		for _, p := range parts {
			h.Write(p)
		}
		return h.Sum(nil)
	}
	k, s := []byte(key), []byte(salt)
	B := sum(k, s, k)
	hl := len(B)
	h := newHash()
	h.Write(k)
	h.Write(s)
	n := len(k)
	for ; n > hl; n -= hl {
		h.Write(B)
	}
	h.Write(B[:n])
	for n := len(k); n > 0; n >>= 1 {
		if n&1 != 0 {
			h.Write(B)
		} else {
			h.Write(k)
		}
	}
	A := h.Sum(nil)
	// P: the hash of the key repeated, as long as the key.
	h = newHash()
	for range len(k) {
		h.Write(k)
	}
	P := repeat(h.Sum(nil), len(k))
	// S: the hash of the salt, 16 + A[0] times, as long as the salt.
	h = newHash()
	for range 16 + int(A[0]) {
		h.Write(s)
	}
	S := repeat(h.Sum(nil), len(s))
	for r := range rounds {
		h := newHash()
		if r&1 != 0 {
			h.Write(P)
		} else {
			h.Write(A)
		}
		if r%3 != 0 {
			h.Write(S)
		}
		if r%7 != 0 {
			h.Write(P)
		}
		if r&1 != 0 {
			h.Write(A)
		} else {
			h.Write(P)
		}
		A = h.Sum(nil)
	}
	b := []byte(magic)
	if custom {
		b = append(b, "rounds="+strconv.Itoa(rounds)+"$"...)
	}
	b = append(b, salt+"$"...)
	at := func(i int) uint32 {
		if i < 0 {
			return 0
		}
		return uint32(A[i])
	}
	for i := 0; i < len(order); i += 3 {
		n := 4
		if i+3 == len(order) {
			n = (8*hl - 6*4*(len(order)/3-1) + 5) / 6
		}
		b = to64(b, at(order[i])<<16|at(order[i+1])<<8|at(order[i+2]), n)
	}
	return string(b)
}

// repeat is h repeated to n bytes.
func repeat(h []byte, n int) []byte {
	b := make([]byte, 0, n)
	for len(b) < n {
		b = append(b, h[:min(len(h), n-len(b))]...)
	}
	return b
}

// desCrypt is the traditional crypt: DES of a block of zeros 25 times,
// with the first 8 characters of key (7 bits each) as the key, and an E
// expansion the 12 bits of the salt's two characters perturb.
func desCrypt(key, salt string) (string, bool) {
	if len(salt) < 2 {
		return "", false
	}
	var saltBits uint32
	for i := range 2 {
		v := strings.IndexByte(itoa64, salt[i])
		if v < 0 {
			return "", false
		}
		saltBits |= uint32(v) << (6 * i)
	}
	var k uint64
	for i := range 8 {
		var c byte
		if i < len(key) {
			c = key[i]
		}
		k = k<<8 | uint64(c<<1)
	}
	d := newDES(k)
	var block uint64
	for range 25 {
		block = d.crypt(block, saltBits, false)
	}
	b := []byte(salt[:2])
	for i := 0; i < 11; i++ {
		sh := 58 - 6*i
		var v uint64
		if sh >= 0 {
			v = block >> uint(sh)
		} else {
			v = block << uint(-sh)
		}
		b = append(b, itoa64[v&0x3f])
	}
	return string(b), true
}

var (
	keyMu  sync.Mutex
	keyDES *des
)

// Setkey is setkey: encrypt's key is the 64 bytes at key, each a bit
// (its low bit), of which DES uses all but each eighth (the parity).
func Setkey(key *c23.Void) {
	k := bitsOf(key)
	keyMu.Lock()
	defer keyMu.Unlock()
	keyDES = newDES(k)
}

// Encrypt is encrypt: it encrypts (edflag 0) or decrypts the 64 bits,
// one in each byte, at block with DES of setkey's key.
func Encrypt(block *c23.Void, edflag int32) {
	b := bitsOf(block)
	keyMu.Lock()
	if keyDES == nil {
		keyDES = newDES(0)
	}
	d := keyDES
	keyMu.Unlock()
	b = d.crypt(b, 0, edflag != 0)
	p := c23.Writable(block, 64)
	for i := range 64 {
		p[i] = byte(b>>(63-i)) & 1
	}
}

func bitsOf(p *c23.Void) uint64 {
	var v uint64
	for _, c := range c23.BytesAt(p, 64) {
		v = v<<1 | uint64(c&1)
	}
	return v
}
