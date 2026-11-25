// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	"math"
	"os"
	"strconv"
	"strings"
)

// ExitNow is _Exit: exit without the functions atexit registered.
func ExitNow(status int32) {
	c23.Fflush((*c23.Void)(nil))
	os.Exit(int(status))
}

// qsorter sorts n elements of size bytes at base: it swaps them through
// a temporary object, so that the pointers they hold keep their
// capabilities.
type qsorter struct {
	base *c23.Void
	size int
	tmp  *c23.Void
	cmp  func(*c23.Void, *c23.Void) int32
}

// newQsorter returns the qsorter of the n elements of size bytes at
// base, all of which are in what base points into (c23.Array): its
// steps and its swaps are in the array, and are not checked each.
func newQsorter(base *c23.Void, n, size uint64, cmp func(*c23.Void, *c23.Void) int32) *qsorter {
	return &qsorter{base: c23.Array(base, n, size), size: int(size), tmp: c23.Malloc(size), cmp: cmp}
}

func (q *qsorter) at(i int) *c23.Void { return c23.Within(q.base, i*q.size) }

func (q *qsorter) less(i, j int) bool { return q.cmp(q.at(i), q.at(j)) < 0 }

func (q *qsorter) swap(i, j int) {
	sz := uintptr(q.size)
	c23.Move(q.tmp, q.at(i), sz)
	c23.Move(q.at(i), q.at(j), sz)
	c23.Move(q.at(j), q.tmp, sz)
}

// siftDown restores the heap of the first n elements from element i.
func (q *qsorter) siftDown(i, n int) {
	for {
		c := 2*i + 1
		if c >= n {
			return
		}
		if c+1 < n && q.less(c, c+1) {
			c++
		}
		if !q.less(i, c) {
			return
		}
		q.swap(i, c)
		i = c
	}
}

// Qsort is qsort: a heapsort, in place and in O(n log n).
func Qsort(base *c23.Void, n, size uint64, cmp *c23.Void) {
	if n < 2 || size == 0 {
		return
	}
	q := newQsorter(base, n, size, c23.CmpFunc(cmp))
	k := int(n)
	for i := k/2 - 1; i >= 0; i-- {
		q.siftDown(i, k)
	}
	for end := k - 1; end > 0; end-- {
		q.swap(0, end)
		q.siftDown(0, end)
	}
}

func Bsearch(key, base *c23.Void, n, size uint64, cmp *c23.Void) *c23.Void {
	f := c23.CmpFunc(cmp)
	base = c23.Array(base, n, size)
	lo, hi := uint64(0), n
	for lo < hi {
		m := lo + (hi-lo)/2
		e := c23.Within(base, int(m*size))
		switch c := f(key, e); {
		case c == 0:
			return e
		case c < 0:
			hi = m
		default:
			lo = m + 1
		}
	}
	return (*c23.Void)(nil)
}

// Atexit is atexit: Exit calls f, after the functions registered later.
func Atexit(f *c23.Void) int32 {
	fn := c23.FuncOf[func()](f)
	c23.AtexitMu.Lock()
	c23.AtexitFn = append(c23.AtexitFn, fn)
	c23.AtexitMu.Unlock()
	return 0
}

func Abs(j int32) int32 {
	if j < 0 {
		return -j
	}
	return j
}

func Labs(j int64) int64 {
	if j < 0 {
		return -j
	}
	return j
}

// Div is div: it stores the div_t {quot, rem} of n/d at sret.
func Div(sret *c23.Void, n, d int32) {
	c23.Store32(sret, uint32(n/d))
	c23.Store32(c23.Add(sret, 4), uint32(n%d))
}

// Ldiv is ldiv, lldiv and imaxdiv: it stores the ldiv_t {quot, rem} of
// n/d at sret.
func Ldiv(sret *c23.Void, n, d int64) {
	c23.Store64(sret, uint64(n/d))
	c23.Store64(c23.Add(sret, 8), uint64(n%d))
}

func Atoi(s *c23.Void) int32 { return int32(Strtol(s, (*c23.Void)(nil), 10)) }

func Atol(s *c23.Void) int64 { return Strtol(s, (*c23.Void)(nil), 10) }

func Atof(s *c23.Void) float64 { return Strtod(s, (*c23.Void)(nil)) }

func Strtol(s, end *c23.Void, base int32) int64 {
	v, neg, of, n := c23.StrtoParse(c23.GoString(s), int(base))
	c23.SetEnd(end, s, n)
	switch {
	case neg && (of || v > 1<<63):
		c23.SetErrno(c23.ERANGE)
		return math.MinInt64
	case !neg && (of || v > math.MaxInt64):
		c23.SetErrno(c23.ERANGE)
		return math.MaxInt64
	case neg:
		return -int64(v)
	}
	return int64(v)
}

func Strtoul(s, end *c23.Void, base int32) uint64 {
	v, neg, of, n := c23.StrtoParse(c23.GoString(s), int(base))
	c23.SetEnd(end, s, n)
	if of {
		c23.SetErrno(c23.ERANGE)
		return math.MaxUint64
	}
	if neg {
		return -v
	}
	return v
}

func Strtod(s, end *c23.Void) float64 { return strtod(s, end, 64) }

func Strtof(s, end *c23.Void) float32 { return float32(strtod(s, end, 32)) }

func Strtold(s, end *c23.Void) float64 { return strtod(s, end, 64) }

// strtod is strtod of a float of the given bits.
func strtod(s, end *c23.Void, bits int) float64 {
	str := c23.GoString(s)
	i := 0
	for i < len(str) && strings.IndexByte(" \t\n\v\f\r", str[i]) >= 0 {
		i++
	}
	// The longest prefix that parses.
	for j := len(str); j > i; j-- {
		t := str[i:j]
		if strings.Contains(t, "_") {
			continue
		}
		if k := strings.IndexAny(t, "xX"); k >= 0 && !strings.ContainsAny(t, "pP") {
			// C's hexadecimal floats need no exponent; Go's do.
			t += "p0"
		}
		f, err := c23.ParseFloat(t, bits)
		if err == nil || err.(*strconv.NumError).Err == strconv.ErrRange {
			if err != nil {
				c23.SetErrno(c23.ERANGE)
			}
			c23.SetEnd(end, s, j)
			return f
		}
	}
	c23.SetEnd(end, s, 0)
	return 0
}

var randState uint64 = 1

func Rand() int32 {
	randState = randState*6364136223846793005 + 1442695040888963407
	return int32(randState >> 33)
}

func Srand(seed uint32) { randState = uint64(seed) }

func Getenv(name *c23.Void) *c23.Void {
	v, ok := os.LookupEnv(c23.GoString(name))
	if !ok {
		return (*c23.Void)(nil)
	}
	return c23.CString(v)
}
