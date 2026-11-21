// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regex

import "math/bits"

// The matcher works on sets of positions in the text (0 to n): forward
// gives where a node can end from a set of starts, backward where it can
// start to reach a set of ends, both in time linear in the text for each
// node (and each repetition's iteration). The search takes the least
// start backward from every end gives and the greatest end forward from
// it gives: POSIX's leftmost-longest match. fill then splits the match
// among the tree, from the left, each node taking the longest it can
// that leaves the rest a way to the end, which gives the subexpressions'
// positions.
//
// A back-reference's sets are all the positions after (or before) the
// set, so where there is one the sets are a superset and fill
// backtracks; elsewhere they are exact and fill never does.

// A pset is a set of positions.
type pset []uint64

func (s pset) has(q int) bool { return q >= 0 && q>>6 < len(s) && s[q>>6]&(1<<(q&63)) != 0 }
func (s pset) add(q int)      { s[q>>6] |= 1 << (q & 63) }
func (s pset) del(q int)      { s[q>>6] &^= 1 << (q & 63) }

// addRange adds the positions a to b.
func (s pset) addRange(a, b int) {
	for ; a <= b && a&63 != 0; a++ {
		s.add(a)
	}
	for ; a+63 <= b; a += 64 {
		s[a>>6] = ^uint64(0)
	}
	for ; a <= b; a++ {
		s.add(a)
	}
}

func (s pset) count() int {
	n := 0
	for _, w := range s {
		n += bits.OnesCount64(w)
	}
	return n
}

func (s pset) empty() bool {
	for _, w := range s {
		if w != 0 {
			return false
		}
	}
	return true
}

// last returns the greatest position in s, or -1.
func (s pset) last() int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != 0 {
			return i<<6 + 63 - bits.LeadingZeros64(s[i])
		}
	}
	return -1
}

// first returns the least position in s, or -1.
func (s pset) first() int {
	for i, w := range s {
		if w != 0 {
			return i<<6 + bits.TrailingZeros64(w)
		}
	}
	return -1
}

// next returns the least position in s from q, or -1.
func (s pset) next(q int) int {
	i := q >> 6
	if i >= len(s) {
		return -1
	}
	if w := s[i] &^ (1<<(q&63) - 1); w != 0 {
		return i<<6 + bits.TrailingZeros64(w)
	}
	for i++; i < len(s); i++ {
		if s[i] != 0 {
			return i<<6 + bits.TrailingZeros64(s[i])
		}
	}
	return -1
}

// prev returns the greatest position in s below q, or -1.
func (s pset) prev(q int) int {
	if q <= 0 {
		return -1
	}
	q--
	i := q >> 6
	if w := s[i] & (2<<(q&63) - 1); w != 0 {
		return i<<6 + 63 - bits.LeadingZeros64(w)
	}
	for i--; i >= 0; i-- {
		if s[i] != 0 {
			return i<<6 + 63 - bits.LeadingZeros64(s[i])
		}
	}
	return -1
}

// A matcher matches a regexT against a text.
type matcher struct {
	re             *regexT
	text           []byte
	n              int
	notbol, noteol bool
	exact          bool // no back-references: the sets are exact
	caps           []int
	memo           map[memoKey]pset
	runs           map[*node][2][]int32
}

type memoKey struct {
	n           *node
	i, lo, hi   int
	end         int
	repetitions bool
}

func (m *matcher) newSet() pset { return make(pset, (m.n+1+63)/64) }

func (m *matcher) single(q int) pset {
	s := m.newSet()
	s.add(q)
	return s
}

// at reports whether the assertion o holds at q.
func (m *matcher) at(o op, q int) bool {
	before := q > 0 && isWord(m.text[q-1])
	after := q < m.n && isWord(m.text[q])
	switch o {
	case opBOL:
		return q == 0 && !m.notbol || m.re.newline && q > 0 && m.text[q-1] == '\n'
	case opEOL:
		return q == m.n && !m.noteol || m.re.newline && q < m.n && m.text[q] == '\n'
	case opWordB:
		return before != after
	case opNotWordB:
		return before == after
	case opWordStart:
		return !before && after
	case opWordEnd:
		return before && !after
	case opBufStart:
		return q == 0
	case opBufEnd:
		return q == m.n
	}
	return false
}

// forward returns where n can end from the positions s.
func (m *matcher) forward(n *node, s pset) pset {
	switch n.op {
	case opChar:
		r := m.newSet()
		for q := s.first(); q >= 0 && q < m.n; q = s.next(q + 1) {
			if n.set.has(m.text[q]) {
				r.add(q + 1)
			}
		}
		return r
	case opEmpty:
		return s
	case opCat:
		for _, sub := range n.sub {
			if s = m.forward(sub, s); s.empty() {
				break
			}
		}
		return s
	case opAlt:
		r := m.newSet()
		for _, sub := range n.sub {
			for i, w := range m.forward(sub, s) {
				r[i] |= w
			}
		}
		return r
	case opGroup:
		return m.forward(n.sub[0], s)
	case opBackref:
		r := m.newSet()
		if q := s.first(); q >= 0 {
			for ; q <= m.n; q++ {
				r.add(q)
			}
		}
		return r
	case opRep:
		if n.sub[0].op == opChar {
			return m.repChar(n.sub[0], n.min, n.max, s, false)
		}
		return m.rep(n.sub[0], n.min, n.max, s, m.forward)
	}
	return m.assert(n.op, s)
}

// backward returns where n can start to end at the positions s.
func (m *matcher) backward(n *node, s pset) pset {
	switch n.op {
	case opChar:
		r := m.newSet()
		for q := s.first(); q >= 0; q = s.next(q + 1) {
			if q > 0 && n.set.has(m.text[q-1]) {
				r.add(q - 1)
			}
		}
		return r
	case opEmpty:
		return s
	case opCat:
		for i := len(n.sub) - 1; i >= 0; i-- {
			if s = m.backward(n.sub[i], s); s.empty() {
				break
			}
		}
		return s
	case opAlt:
		r := m.newSet()
		for _, sub := range n.sub {
			for i, w := range m.backward(sub, s) {
				r[i] |= w
			}
		}
		return r
	case opGroup:
		return m.backward(n.sub[0], s)
	case opBackref:
		r := m.newSet()
		for q := s.last(); q >= 0; q-- {
			r.add(q)
		}
		return r
	case opRep:
		if n.sub[0].op == opChar {
			return m.repChar(n.sub[0], n.min, n.max, s, true)
		}
		return m.rep(n.sub[0], n.min, n.max, s, m.backward)
	}
	return m.assert(n.op, s)
}

func (m *matcher) assert(o op, s pset) pset {
	r := m.newSet()
	for q := s.first(); q >= 0; q = s.next(q + 1) {
		if m.at(o, q) {
			r.add(q)
		}
	}
	return r
}

// rep applies step (forward or backward) of sub lo to hi times (hi -1
// for no bound) to s: breadth first, each position from its fewest
// iterations, which is where it allows the most more.
func (m *matcher) rep(sub *node, lo, hi int, s pset, step func(*node, pset) pset) pset {
	for i := 0; i < lo; i++ {
		if s = step(sub, s); s.empty() {
			return s
		}
	}
	if hi == lo {
		return s
	}
	u := append(pset(nil), s...)
	frontier := s
	for i := lo; hi < 0 || i < hi; i++ {
		next := step(sub, frontier)
		if &next[0] == &frontier[0] {
			// step gave its argument back (an empty sub), which is
			// the caller's.
			break
		}
		grew := false
		for j, w := range next {
			w &^= u[j]
			next[j] = w
			u[j] |= w
			grew = grew || w != 0
		}
		if !grew {
			break
		}
		frontier = next
	}
	return u
}

// repChar is rep of a byte: in linear time, with the run of the bytes
// the set has at (or, backward, before) each position.
func (m *matcher) repChar(sub *node, lo, hi int, s pset, back bool) pset {
	if m.runs == nil {
		m.runs = map[*node][2][]int32{}
	}
	runs, ok := m.runs[sub]
	if !ok {
		fw, bw := make([]int32, m.n+1), make([]int32, m.n+1)
		for q := m.n - 1; q >= 0; q-- {
			if sub.set.has(m.text[q]) {
				fw[q] = fw[q+1] + 1
			}
		}
		for q := 1; q <= m.n; q++ {
			if sub.set.has(m.text[q-1]) {
				bw[q] = bw[q-1] + 1
			}
		}
		runs = [2][]int32{fw, bw}
		m.runs[sub] = runs
	}
	// From a few positions, the runs themselves; from more, the sum
	// of a difference array.
	r := m.newSet()
	few := s.count() <= 8
	var d []int32
	if !few {
		d = make([]int32, m.n+2)
	}
	for q := s.first(); q >= 0; q = s.next(q + 1) {
		run := int(runs[btoi(back)][q])
		if hi >= 0 {
			run = min(run, hi)
		}
		if run < lo {
			continue
		}
		a, b := q+lo, q+run
		if back {
			a, b = q-run, q-lo
		}
		if few {
			r.addRange(a, b)
		} else {
			d[a]++
			d[b+1]--
		}
	}
	if !few {
		c := int32(0)
		for q := 0; q <= m.n; q++ {
			if c += d[q]; c > 0 {
				r.add(q)
			}
		}
	}
	return r
}

// backSeq returns where n's sub[i:] can start to end at end.
func (m *matcher) backSeq(n *node, i, end int) pset {
	if i == len(n.sub) {
		return m.single(end)
	}
	k := memoKey{n: n, i: i, end: end}
	if s, ok := m.memo[k]; ok {
		return s
	}
	s := m.backward(n.sub[i], m.backSeq(n, i+1, end))
	m.memoize(k, s)
	return s
}

// backRep returns where lo to hi iterations of n's sub[0] can start to
// end at end.
func (m *matcher) backRep(n *node, lo, hi, end int) pset {
	k := memoKey{n: n, lo: lo, hi: hi, end: end, repetitions: true}
	if s, ok := m.memo[k]; ok {
		return s
	}
	r := mk(opRep, n.sub[0])
	r.min, r.max = lo, hi
	s := m.backward(r, m.single(end))
	m.memoize(k, s)
	return s
}

func (m *matcher) memoize(k memoKey, s pset) {
	if m.memo == nil {
		m.memo = map[memoKey]pset{}
	}
	m.memo[k] = s
}

// search finds the leftmost-longest match from start, and its
// subexpressions' positions (in pairs, the whole first) if subs.
func (m *matcher) search(start int, subs bool) ([]int, bool) {
	root := m.re.root
	all := m.newSet()
	for q := start; q <= m.n; q++ {
		all.add(q)
	}
	starts := m.backward(root, all)
	m.caps = make([]int, 2*(m.re.nsub+1))
	for s := starts.next(start); s >= 0; s = starts.next(s + 1) {
		ends := m.forward(root, m.single(s))
		for e := ends.last(); e >= 0; e = ends.prev(e) {
			for i := range m.caps {
				m.caps[i] = -1
			}
			m.caps[0], m.caps[1] = s, e
			if m.exact && !subs || m.fill(root, s, e, func() bool { return true }) {
				return m.caps, true
			}
		}
	}
	return nil, false
}

// fill matches n to text[p:end], setting the subexpressions' positions,
// and calls k: it returns k's result, trying the next way n matches if k
// fails (and none if n does not match).
func (m *matcher) fill(n *node, p, end int, k func() bool) bool {
	if !n.groups && !n.backrefs {
		if m.exact {
			return k()
		}
		return m.forward(n, m.single(p)).has(end) && k()
	}
	switch n.op {
	case opBackref:
		so, eo := m.caps[2*n.n], m.caps[2*n.n+1]
		if so < 0 || end-p != eo-so {
			return false
		}
		for i := 0; i < eo-so; i++ {
			a, b := m.text[so+i], m.text[p+i]
			if a != b && !(m.re.icase && lower(a) == lower(b)) {
				return false
			}
		}
		return k()
	case opGroup:
		i := 2 * n.n
		so, eo := m.caps[i], m.caps[i+1]
		m.caps[i], m.caps[i+1] = p, end
		if m.fill(n.sub[0], p, end, k) {
			return true
		}
		m.caps[i], m.caps[i+1] = so, eo
		return false
	case opAlt:
		for _, sub := range n.sub {
			if m.forward(sub, m.single(p)).has(end) && m.fill(sub, p, end, k) {
				return true
			}
		}
		return false
	case opCat:
		return m.fillSeq(n, 0, p, end, k)
	case opRep:
		return m.fillRep(n, 0, p, end, k)
	}
	return false
}

// fillSeq fills n's sub[i:] to text[p:end].
func (m *matcher) fillSeq(n *node, i, p, end int, k func() bool) bool {
	if m.exact {
		// The first way is the way: no recursion through k.
		for ; i < len(n.sub)-1; i++ {
			ends := m.forward(n.sub[i], m.single(p))
			rest := m.backSeq(n, i+1, end)
			for j := range ends {
				ends[j] &= rest[j]
			}
			e := ends.last()
			if e < 0 {
				return false
			}
			m.fill(n.sub[i], p, e, func() bool { return true })
			p = e
		}
		m.fill(n.sub[i], p, end, func() bool { return true })
		return k()
	}
	if i == len(n.sub)-1 {
		return m.fill(n.sub[i], p, end, k)
	}
	ends := m.forward(n.sub[i], m.single(p))
	rest := m.backSeq(n, i+1, end)
	for j := range ends {
		ends[j] &= rest[j]
	}
	for e := ends.last(); e >= 0; e = ends.prev(e) {
		if m.fill(n.sub[i], p, e, func() bool { return m.fillSeq(n, i+1, e, end, k) }) {
			return true
		}
	}
	return false
}

// fillRep fills the repetition n, which has matched c times, to
// text[p:end]: each iteration the longest it can, none empty unless the
// least count needs it.
func (m *matcher) fillRep(n *node, c, p, end int, k func() bool) bool {
	sub := n.sub[0]
	if m.exact {
		for ; p < end; c++ {
			ends := m.forward(sub, m.single(p))
			ends.del(p)
			hi := -1
			if n.max >= 0 {
				hi = n.max - c - 1
			}
			rest := m.backRep(n, max(n.min-c-1, 0), hi, end)
			for j := range ends {
				ends[j] &= rest[j]
			}
			e := ends.last()
			if e < 0 {
				return false
			}
			m.fill(sub, p, e, func() bool { return true })
			p = e
		}
		if c < n.min {
			m.fill(sub, p, p, func() bool { return true })
		}
		return k()
	}
	if p == end {
		if c >= n.min {
			return k()
		}
		return m.fill(sub, p, p, k)
	}
	if n.max >= 0 && c >= n.max {
		return false
	}
	ends := m.forward(sub, m.single(p))
	ends.del(p)
	hi := -1
	if n.max >= 0 {
		hi = n.max - c - 1
	}
	rest := m.backRep(n, max(n.min-c-1, 0), hi, end)
	for j := range ends {
		ends[j] &= rest[j]
	}
	for e := ends.last(); e >= 0; e = ends.prev(e) {
		if m.fill(sub, p, e, func() bool { return m.fillRep(n, c+1, e, end, k) }) {
			return true
		}
	}
	return false
}
