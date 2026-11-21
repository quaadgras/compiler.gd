// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	"sync"
)

// POSIX's pseudo-random numbers: the drand48 family's 48-bit linear
// congruential generator, which every C library has alike, and random's
// additive feedback generator as glibc has it (its state in the caller's
// array, its seeding and its first numbers), and glibc's rand_r.

// The drand48 family: X(n+1) = (a X(n) + c) mod 2^48.

const (
	lcgA = 0x5deece66d
	lcgC = 0xb
)

var (
	r48Mu   sync.Mutex
	r48X    uint64  = 0x1234abcd330e // glibc's and musl's X before srand48
	r48A    uint64  = lcgA
	r48C    uint64  = lcgC
	seed48P c23.Ptr // seed48's array
)

func step48(x uint64) uint64 { return (r48A*x + r48C) & (1<<48 - 1) }

// next48 steps the global X.
func next48() uint64 {
	r48Mu.Lock()
	defer r48Mu.Unlock()
	r48X = step48(r48X)
	return r48X
}

// loadX and storeX read and write an X kept as unsigned short[3], the
// least significant first.
func loadX(p c23.Ptr) uint64 {
	return uint64(c23.Load16(p)) | uint64(c23.Load16(c23.Add(p, 2)))<<16 | uint64(c23.Load16(c23.Add(p, 4)))<<32
}

func storeX(p c23.Ptr, x uint64) {
	c23.Store16(p, uint16(x))
	c23.Store16(c23.Add(p, 2), uint16(x>>16))
	c23.Store16(c23.Add(p, 4), uint16(x>>32))
}

// nextX steps the X at xsubi, with the global a and c.
func nextX(xsubi c23.Ptr) uint64 {
	r48Mu.Lock()
	x := step48(loadX(xsubi))
	r48Mu.Unlock()
	storeX(xsubi, x)
	return x
}

func unit48(x uint64) float64 { return float64(x) / (1 << 48) }

func Drand48() float64 { return unit48(next48()) }

func Erand48(xsubi c23.Ptr) float64 { return unit48(nextX(xsubi)) }

func Lrand48() int64 { return int64(next48() >> 17) }

func Nrand48(xsubi c23.Ptr) int64 { return int64(nextX(xsubi) >> 17) }

func Mrand48() int64 { return int64(int32(next48() >> 16)) }

func Jrand48(xsubi c23.Ptr) int64 { return int64(int32(nextX(xsubi) >> 16)) }

// Srand48 is srand48: X is the seed's low 32 bits and 0x330e, and a and
// c are the standard ones again.
func Srand48(seed int64) {
	r48Mu.Lock()
	defer r48Mu.Unlock()
	r48X = uint64(uint32(seed))<<16 | 0x330e
	r48A, r48C = lcgA, lcgC
}

// Seed48 is seed48: X is the seed's, a and c the standard ones again; it
// returns an array of the X before.
func Seed48(seed c23.Ptr) c23.Ptr {
	x := loadX(seed)
	r48Mu.Lock()
	defer r48Mu.Unlock()
	if c23.Addr(seed48P) == 0 {
		seed48P = c23.Malloc(6)
	}
	storeX(seed48P, r48X)
	r48X = x
	r48A, r48C = lcgA, lcgC
	return seed48P
}

// Lcong48 is lcong48: X, a and c are param's.
func Lcong48(param c23.Ptr) {
	x, a, c := loadX(param), loadX(c23.Add(param, 6)), uint64(c23.Load16(c23.Add(param, 12)))
	r48Mu.Lock()
	defer r48Mu.Unlock()
	r48X, r48A, r48C = x, a, c
}

// random, as glibc's random_r.c: a state of deg words (int32_t), in the
// array of initstate or setstate after the word that says its type and
// where rptr is (which initstate and setstate write when they leave a
// state); TYPE_0 (deg 0) is rand's LCG of 31 bits.

const maxTypes = 5

var (
	randDegrees = [maxTypes]int{0, 7, 15, 31, 63}
	randSeps    = [maxTypes]int{0, 3, 1, 3, 1}
)

type randomGen struct {
	state      c23.Ptr // the first word of the state, after the type's
	typ        int
	deg, sep   int
	fptr, rptr int // indexes in state
}

var (
	rndMu sync.Mutex
	rnd   randomGen
	l64aP c23.Ptr
)

func (g *randomGen) at(i int) int32     { return int32(c23.Load32(c23.Add(g.state, 4*i))) }
func (g *randomGen) set(i int, v int32) { c23.Store32(c23.Add(g.state, 4*i), uint32(v)) }

// init makes the state glibc's first: initstate(1, randtbl, 128).
func (g *randomGen) init() {
	if g.state.Obj != nil {
		return
	}
	buf := c23.Malloc(128)
	g.start(buf, 3)
	g.seed(1)
	g.save()
}

// start makes the type typ's state the one in buf.
func (g *randomGen) start(buf c23.Ptr, typ int) {
	g.state = c23.Add(buf, 4)
	g.typ, g.deg, g.sep = typ, randDegrees[typ], randSeps[typ]
}

// save writes the type and rptr in the word before the state.
func (g *randomGen) save() {
	v := int32(0)
	if g.typ != 0 {
		v = int32(maxTypes*g.rptr + g.typ)
	}
	c23.Store32(c23.Add(g.state, -4), uint32(v))
}

// seed is srandom_r: the state's words from the seed by Park and Miller's
// "minimal standard" generator, then 10 deg numbers discarded.
func (g *randomGen) seed(seed uint32) {
	if seed == 0 {
		seed = 1
	}
	g.set(0, int32(seed))
	if g.typ == 0 {
		return
	}
	word := int32(seed)
	for i := 1; i < g.deg; i++ {
		hi, lo := int64(word)/127773, int64(word)%127773
		w := 16807*lo - 2836*hi
		if w < 0 {
			w += 2147483647
		}
		word = int32(w)
		g.set(i, word)
	}
	g.fptr, g.rptr = g.sep, 0
	for range 10 * g.deg {
		g.next()
	}
}

func (g *randomGen) next() int32 {
	if g.typ == 0 {
		v := int32((uint32(g.at(0))*1103515245 + 12345) & 0x7fffffff)
		g.set(0, v)
		return v
	}
	v := uint32(g.at(g.fptr)) + uint32(g.at(g.rptr))
	g.set(g.fptr, int32(v))
	g.fptr++
	if g.fptr >= g.deg {
		g.fptr = 0
		g.rptr++
	} else {
		g.rptr++
		if g.rptr >= g.deg {
			g.rptr = 0
		}
	}
	return int32(v >> 1)
}

// Random is random: glibc's numbers, 1804289383 first.
func Random() int64 {
	rndMu.Lock()
	defer rndMu.Unlock()
	rnd.init()
	return int64(rnd.next())
}

// Srandom is srandom.
func Srandom(seed uint32) {
	rndMu.Lock()
	defer rndMu.Unlock()
	rnd.init()
	rnd.seed(seed)
}

// Initstate is initstate: random's state is the n bytes at buf (at
// least 8: 8 for TYPE_0, 32 for TYPE_1, 64, 128 and 256 for TYPE_4),
// seeded with seed. It returns the state random had.
func Initstate(seed uint32, buf c23.Ptr, n uint64) c23.Ptr {
	var typ int
	switch {
	case n < 8:
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr{}
	case n < 32:
		typ = 0
	case n < 64:
		typ = 1
	case n < 128:
		typ = 2
	case n < 256:
		typ = 3
	default:
		typ = 4
	}
	rndMu.Lock()
	defer rndMu.Unlock()
	rnd.init()
	rnd.save()
	old := c23.Add(rnd.state, -4)
	rnd.start(buf, typ)
	rnd.seed(seed)
	rnd.save()
	return old
}

// Setstate is setstate: random's state is the one initstate made at buf
// (which random has used since), and it returns the state random had.
func Setstate(buf c23.Ptr) c23.Ptr {
	rndMu.Lock()
	defer rndMu.Unlock()
	rnd.init()
	info := int32(c23.Load32(buf))
	typ := int(info % maxTypes)
	if info < 0 || typ < 0 {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr{}
	}
	rnd.save()
	old := c23.Add(rnd.state, -4)
	rnd.start(buf, typ)
	if typ != 0 {
		rear := int(info / maxTypes)
		rnd.rptr = rear
		rnd.fptr = (rear + rnd.sep) % rnd.deg
	}
	return old
}

// RandR is glibc's rand_r: three steps of rand's LCG, 11, 10 and 10 bits
// of them.
func RandR(seed c23.Ptr) int32 {
	next := c23.Load32(seed)
	next = next*1103515245 + 12345
	result := (next / 65536) % 2048
	next = next*1103515245 + 12345
	result = result<<10 ^ (next/65536)%1024
	next = next*1103515245 + 12345
	result = result<<10 ^ (next/65536)%1024
	c23.Store32(seed, next)
	return int32(result)
}

// a64l's and l64a's digits, the least significant first.
const radix64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// A64l is a64l: the value of up to 6 digits of base 64, sign-extended
// from 32 bits as POSIX says (and musl does; glibc's is not).
func A64l(s c23.Ptr) int64 {
	var x uint32
	for i := 0; i < 6; i++ {
		c := c23.Load8(c23.Add(s, i))
		d := -1
		for j := 0; j < len(radix64); j++ {
			if radix64[j] == c && c != 0 {
				d = j
			}
		}
		if d < 0 {
			break
		}
		x |= uint32(d) << (6 * i)
	}
	return int64(int32(x))
}

// L64a is l64a: v's low 32 bits in base 64, in an array the next call
// reuses ("" for 0).
func L64a(v int64) c23.Ptr {
	rndMu.Lock()
	defer rndMu.Unlock()
	if c23.Addr(l64aP) == 0 {
		l64aP = c23.Malloc(7)
	}
	m := uint32(v)
	i := 0
	for ; m > 0; m >>= 6 {
		c23.Store8(c23.Add(l64aP, i), radix64[m&63])
		i++
	}
	c23.Store8(c23.Add(l64aP, i), 0)
	return l64aP
}
