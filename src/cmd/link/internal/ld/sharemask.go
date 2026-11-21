// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"encoding/binary"
	"strconv"
	"strings"

	"cmd/link/internal/loader"
)

// What the functions that are only known when the program runs do
// (cmd/compile/internal/ir/sharelink.go, doc/gd/sharing.md).
//
// Compiled code reads, for the argument of a call of a function value or
// of a method of an interface, a word that says whether a function that
// may be called there shares the argument: an escape mask, the masks of
// all the functions of that class in the program, ORed. The compiler
// wrote the words with all bits set, and a record for each function that
// may be called so: its class, its mask, and the classes that it passes
// its parameters on to. Here the words are computed, from the records of
// the functions that the program has.
//
// The words stay as they are where functions may come from elsewhere: a
// plugin, a shared library. A function value that reflect.MakeFunc made
// may do anything, so the words of the function values stay if the
// program has it; one that reflect makes of a method does what the
// method does, so the methods count as function values if the program
// has that.

const (
	shareWordPrefix = "go:sharemask."
	shareRecPrefix  = "go:sharerec."

	// The bits of an escape mask (cmd/compile/internal/ir/escmask.go).
	shareMaskArgs    = 15
	shareMaskShared  = 1 + shareMaskArgs
	shareMaskViaRecv = 1 + 2*shareMaskArgs
	shareMaskHeld    = 1 + 3*shareMaskArgs

	// The bits of a word that say in which arguments' structures a
	// function of the class may store (ir.ShareWordStores).
	shareWordStores     = 17
	shareWordMixDstRecv = 16
	shareWordMixDst     = 49
	shareWordMix        = 32 // ir.ShareWordMix
	shareWordMixRecv    = 33
	shareWordMixArg     = 34
	shareMixRecv        = 255 // ir.MixRecv
	shareStoresAll      = 1<<shareMaskArgs - 1
)

type shareDep struct {
	param, arg int
	key        string
}

type shareRec struct {
	key  string
	sig  *shareSig
	mask uint64
	deps []shareDep
	// stores has bit k if the function stores in what its argument
	// k reaches; it also does if a function of a class that it
	// passes the argument on to (sdeps) does.
	stores uint16
	sdeps  []shareDep
	// mix: the function may store one argument in what another
	// reaches; it also does if a function of a class of mdeps does.
	// src: the function may store in what another of its arguments
	// reaches its receiver (bit 0) or its argument k (bit k+1); it
	// also does if a function of a class of mdeps does.
	src   uint16
	mdeps []shareDep
	// dst: the function may store another argument in what its
	// receiver (bit 0) or its argument k (bit k+1) reaches; it also
	// does, for a parameter of ddeps, if a function of that class
	// stores any argument in what another reaches.
	dst   uint16
	ddeps []shareDep
	name  string // of the record's symbol, for -v
}

// A shareSig is a key, taken apart.
type shareSig struct {
	kind            byte
	name            string
	params, results []string
}

func parseShareKey(key string) *shareSig {
	if len(key) < 3 || key[1] != '.' {
		return nil
	}
	sig := &shareSig{kind: key[0]}
	rest := key[2:]
	i := strings.IndexByte(rest, '(')
	if i < 0 {
		return nil
	}
	sig.name, rest = rest[:i], rest[i:]
	list := func() ([]string, bool) {
		if rest == "" || rest[0] != '(' {
			return nil, false
		}
		rest = rest[1:]
		var out []string
		for {
			if rest == "" {
				return nil, false
			}
			if rest[0] == ')' {
				rest = rest[1:]
				return out, true
			}
			j := strings.IndexByte(rest, ':')
			if j < 0 {
				return nil, false
			}
			n, err := strconv.Atoi(rest[:j])
			if err != nil || n < 0 || j+1+n > len(rest) {
				return nil, false
			}
			out = append(out, rest[j+1:j+1+n])
			rest = rest[j+1+n:]
		}
	}
	var ok bool
	if sig.params, ok = list(); !ok {
		return nil
	}
	if sig.results, ok = list(); !ok || rest != "" {
		return nil
	}
	return sig
}

func (p *shareSig) wild() bool {
	for _, s := range p.params {
		if s == "?" {
			return true
		}
	}
	for _, s := range p.results {
		if s == "?" {
			return true
		}
	}
	return false
}

// unify reports whether the key k, of a word, and the key r, of a
// function's record, may be of one class: they are the same but where
// one of them has a type with a shape, which stands for any type. With
// anyKind, a method is also a function value, the one that is made of
// it.
func (k *shareSig) unify(r *shareSig, anyKind bool) bool {
	if k.kind != r.kind {
		if !anyKind || k.kind != 'F' || r.kind != 'M' {
			return false
		}
	} else if k.name != r.name {
		return false
	}
	if len(k.params) != len(r.params) || len(k.results) != len(r.results) {
		return false
	}
	for i, s := range k.params {
		if s != "?" && r.params[i] != "?" && s != r.params[i] {
			return false
		}
	}
	for i, s := range k.results {
		if s != "?" && r.results[i] != "?" && s != r.results[i] {
			return false
		}
	}
	return true
}

func parseShareRec(data []byte) *shareRec {
	if len(data) < 8 {
		return nil
	}
	r := &shareRec{mask: binary.LittleEndian.Uint64(data)}
	data = data[8:]
	str := func() (string, bool) {
		n, w := binary.Uvarint(data)
		if w <= 0 || uint64(len(data)-w) < n {
			return "", false
		}
		s := string(data[w : w+int(n)])
		data = data[w+int(n):]
		return s, true
	}
	var ok bool
	if r.key, ok = str(); !ok {
		return nil
	}
	n, w := binary.Uvarint(data)
	if w <= 0 {
		return nil
	}
	data = data[w:]
	for ; n > 0; n-- {
		if len(data) < 2 {
			return nil
		}
		d := shareDep{param: int(data[0]), arg: int(data[1])}
		data = data[2:]
		if d.key, ok = str(); !ok {
			return nil
		}
		r.deps = append(r.deps, d)
	}
	st, w := binary.Uvarint(data)
	if w <= 0 {
		return nil
	}
	r.stores = uint16(st)
	data = data[w:]
	n, w = binary.Uvarint(data)
	if w <= 0 {
		return nil
	}
	data = data[w:]
	for ; n > 0; n-- {
		if len(data) < 2 {
			return nil
		}
		d := shareDep{param: int(data[0]), arg: int(data[1])}
		data = data[2:]
		if d.key, ok = str(); !ok {
			return nil
		}
		r.sdeps = append(r.sdeps, d)
	}
	for i := 0; i < 2; i++ {
		m, w := binary.Uvarint(data)
		if w <= 0 {
			return nil
		}
		data = data[w:]
		n, w = binary.Uvarint(data)
		if w <= 0 {
			return nil
		}
		data = data[w:]
		var deps []shareDep
		for ; n > 0; n-- {
			if len(data) < 2 {
				return nil
			}
			d := shareDep{param: int(data[0]), arg: int(data[1])}
			data = data[2:]
			if d.key, ok = str(); !ok {
				return nil
			}
			deps = append(deps, d)
		}
		if i == 0 {
			r.src, r.mdeps = uint16(m), deps
		} else {
			r.dst, r.ddeps = uint16(m), deps
		}
	}
	return r
}

// shareMaskShares reports whether a function with the mask w may share
// its argument arg, or keep it where the caller does not know.
func shareMaskShares(w uint64, arg int) bool {
	if w&1 != 0 || arg >= shareMaskArgs {
		return true
	}
	if w>>(1+arg)&1 == 0 {
		return false
	}
	// (What is stored in what the receiver points to is in the one
	// structure that the caller takes the arguments of such a call
	// for.)
	return w>>(shareMaskShared+arg)&1 != 0
}

func (ctxt *Link) shareMasks() {
	ldr := ctxt.loader
	type word struct {
		s   loader.Sym
		key string
	}
	var words []word
	var recSyms []loader.Sym
	for s := loader.Sym(1); s < loader.Sym(ldr.NSym()); s++ {
		if !ldr.AttrReachable(s) {
			continue
		}
		name := ldr.SymName(s)
		if !strings.HasPrefix(name, "go:share") {
			continue
		}
		if key, ok := strings.CutPrefix(name, shareWordPrefix); ok {
			words = append(words, word{s, key})
		} else if strings.HasPrefix(name, shareRecPrefix) {
			recSyms = append(recSyms, s)
		}
	}
	// The records are for here only.
	defer func() {
		for _, s := range recSyms {
			ldr.SetAttrReachable(s, false)
		}
	}()
	if len(words) == 0 {
		return
	}
	if ctxt.DynlinkingGo() || ctxt.BuildMode != BuildModeExe && ctxt.BuildMode != BuildModePIE {
		return
	}
	has := func(name string) bool {
		for _, abi := range []int{0, 1} {
			if s := ldr.Lookup(name, abi); s != 0 && ldr.AttrReachable(s) {
				return true
			}
		}
		return false
	}
	madeFuncs := has("reflect.MakeFunc")
	methodValues := has("reflect.makeMethodValue")

	// The functions of each class, and the patterns.
	byKey := make(map[string][]*shareRec)
	var patterns, concrete []*shareRec
	bySig := make(map[string][]*shareRec) // the methods, by what follows the name
	keys := make(map[string]bool)
	for _, s := range recSyms {
		r := parseShareRec(ldr.Data(s))
		if r == nil {
			// Not understood: nothing is known.
			return
		}
		r.name = ldr.SymName(s)
		sig := parseShareKey(r.key)
		if sig == nil {
			return
		}
		r.sig = sig
		if sig.wild() {
			patterns = append(patterns, r)
		} else {
			concrete = append(concrete, r)
			byKey[r.key] = append(byKey[r.key], r)
			keys[r.key] = true
			if sig.kind == 'M' {
				tail := r.key[2+len(sig.name):]
				bySig[tail] = append(bySig[tail], r)
			}
		}
		for _, d := range r.deps {
			keys[d.key] = true
		}
		for _, d := range r.sdeps {
			keys[d.key] = true
		}
		for _, d := range r.mdeps {
			keys[d.key] = true
		}
		for _, d := range r.ddeps {
			keys[d.key] = true
		}
	}
	for _, w := range words {
		keys[w.key] = true
	}

	// The members of each class that some word needs.
	type class struct {
		recs   []*shareRec
		all    bool
		mask   uint64
		stores uint16
		src    uint16
		dst    uint16
	}
	classes := make(map[string]*class, len(keys))
	for key := range keys {
		c := &class{recs: byKey[key]}
		classes[key] = c
		sig := parseShareKey(key)
		if sig == nil {
			c.all = true
			continue
		}
		if sig.kind == 'F' && madeFuncs {
			c.all = true
			continue
		}
		if sig.wild() {
			// The key of a call in a function with type
			// parameters: every class that it may stand for.
			c.recs = nil
			for _, r := range concrete {
				if sig.unify(r.sig, methodValues) {
					c.recs = append(c.recs, r)
				}
			}
		} else if sig.kind == 'F' && methodValues {
			c.recs = append(c.recs[:len(c.recs):len(c.recs)], bySig[key[2:]]...)
		}
		for _, p := range patterns {
			if sig.unify(p.sig, methodValues) {
				c.recs = append(c.recs[:len(c.recs):len(c.recs)], p)
			}
		}
	}

	// Until nothing changes: a function shares a parameter if a
	// function that it passes it on to shares it.
	for changed := true; changed; {
		changed = false
		for _, c := range classes {
			m := c.mask
			if c.all {
				m = ^uint64(0)
			}
			for _, r := range c.recs {
				if m == ^uint64(0) {
					break
				}
				if r.mask&1 != 0 {
					// A mask that is computed when the
					// program runs.
					m = ^uint64(0)
					break
				}
				m |= r.mask
				for _, d := range r.deps {
					if dc := classes[d.key]; dc == nil || shareMaskShares(dc.mask, d.arg) {
						if d.param >= shareMaskArgs {
							m = ^uint64(0)
							break
						}
						m |= 1<<(1+d.param) | 1<<(shareMaskShared+d.param) | 1<<(shareMaskViaRecv+d.param) | 1<<(shareMaskHeld+d.param)
					}
				}
			}
			if m != c.mask {
				c.mask = m
				changed = true
			}
			st := c.stores
			if c.all {
				st = shareStoresAll
			}
			for _, r := range c.recs {
				st |= r.stores
				for _, d := range r.sdeps {
					if dc := classes[d.key]; dc == nil || d.arg >= shareMaskArgs || dc.stores&(1<<d.arg) != 0 {
						if d.param < shareMaskArgs {
							st |= 1 << d.param
						} else {
							st = shareStoresAll
						}
					}
				}
			}
			if st != c.stores {
				c.stores = st
				changed = true
			}
			src := c.src
			if c.all {
				src = ^uint16(0)
			}
			// bit returns the bit of src for the place p of an
			// argument: shareMixRecv for a receiver.
			bit := func(p int) uint16 {
				if p == shareMixRecv {
					return 1
				}
				if p >= shareMaskArgs {
					return ^uint16(0)
				}
				return 1 << (1 + p)
			}
			for _, r := range c.recs {
				src |= r.src
				for _, d := range r.mdeps {
					if dc := classes[d.key]; dc == nil || dc.src&bit(d.arg) != 0 {
						src |= bit(d.param)
					}
				}
			}
			if src != c.src {
				c.src = src
				changed = true
			}
			dst := c.dst
			if c.all {
				dst = ^uint16(0)
			}
			for _, r := range c.recs {
				dst |= r.dst
				for _, d := range r.ddeps {
					if dc := classes[d.key]; dc == nil || dc.src != 0 {
						dst |= bit(d.param)
					}
				}
			}
			if dst != c.dst {
				c.dst = dst
				changed = true
			}
		}
	}

	for _, w := range words {
		c := classes[w.key]
		// What compiled code reads: bit k+1 is set if a function of
		// the class may share its argument k, which is so if it
		// keeps it where another goroutine reaches, or in what its
		// receiver points to, which the caller does not know.
		var word uint64
		for arg := 0; arg < shareMaskArgs; arg++ {
			if shareMaskShares(c.mask, arg) {
				word |= 1 << (1 + arg)
			}
		}
		word |= uint64(c.stores) << shareWordStores
		if c.src != 0 {
			word |= 1 << shareWordMix
		}
		word |= uint64(c.src&1) << shareWordMixRecv
		word |= uint64(c.src>>1) & (1<<shareMaskArgs - 1) << shareWordMixArg
		word |= uint64(c.dst&1) << shareWordMixDstRecv
		word |= uint64(c.dst>>1) & (1<<shareMaskArgs - 1) << shareWordMixDst
		if c.mask&1 != 0 {
			word = ^uint64(0)
		}
		data := make([]byte, 8)
		ctxt.Arch.ByteOrder.PutUint64(data, word)
		sb := ldr.MakeSymbolUpdater(w.s)
		sb.SetData(data)
		if ctxt.Debugvlog > 1 {
			ctxt.Logf("sharemask %s: %#x (mask %#x) of %d functions\n", w.key, word, c.mask, len(c.recs))
			for _, r := range c.recs {
				ctxt.Logf("\t%s: mask %#x, stores %#x, stores in another's %#x, passes on to %d, %d and %d\n", r.name, r.mask, r.stores, r.src, len(r.deps), len(r.sdeps), len(r.mdeps))
			}
		}
	}
	if ctxt.Debugvlog != 0 {
		ctxt.Logf("sharemask: %d words, %d records, %d patterns\n", len(words), len(recSyms), len(patterns))
	}
}
