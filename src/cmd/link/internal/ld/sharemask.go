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
)

type shareDep struct {
	param, arg int
	key        string
}

type shareRec struct {
	key  string
	sig  *shareSig // of a pattern
	mask uint64
	deps []shareDep
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

// matches reports whether the pattern p stands for the key k. With
// anyKind, a method stands for the function value that is made of it.
func (p *shareSig) matches(k *shareSig, anyKind bool) bool {
	if p.kind != k.kind {
		if !anyKind || p.kind != 'M' || k.kind != 'F' {
			return false
		}
	} else if p.name != k.name {
		return false
	}
	if len(p.params) != len(k.params) || len(p.results) != len(k.results) {
		return false
	}
	for i, s := range p.params {
		if s != "?" && s != k.params[i] {
			return false
		}
	}
	for i, s := range p.results {
		if s != "?" && s != k.results[i] {
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
	return w>>(shareMaskShared+arg)&1 != 0 || w>>(shareMaskViaRecv+arg)&1 != 0
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
	var patterns []*shareRec
	bySig := make(map[string][]*shareRec) // the methods, by what follows the name
	keys := make(map[string]bool)
	for _, s := range recSyms {
		r := parseShareRec(ldr.Data(s))
		if r == nil {
			// Not understood: nothing is known.
			return
		}
		sig := parseShareKey(r.key)
		if sig == nil {
			return
		}
		if sig.wild() {
			r.sig = sig
			patterns = append(patterns, r)
		} else {
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
	}
	for _, w := range words {
		keys[w.key] = true
	}

	// The members of each class that some word needs.
	type class struct {
		recs []*shareRec
		all  bool
		mask uint64
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
		if sig.kind == 'F' {
			if madeFuncs {
				c.all = true
				continue
			}
			if methodValues {
				c.recs = append(c.recs[:len(c.recs):len(c.recs)], bySig[key[2:]]...)
			}
		}
		for _, p := range patterns {
			if p.sig.matches(sig, methodValues) {
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
		}
	}

	for _, w := range words {
		c := classes[w.key]
		data := make([]byte, 8)
		ctxt.Arch.ByteOrder.PutUint64(data, c.mask)
		sb := ldr.MakeSymbolUpdater(w.s)
		sb.SetData(data)
		if ctxt.Debugvlog > 1 {
			ctxt.Logf("sharemask %s: %#x of %d functions\n", w.key, c.mask, len(c.recs))
		}
	}
	if ctxt.Debugvlog != 0 {
		ctxt.Logf("sharemask: %d words, %d records, %d patterns\n", len(words), len(recSyms), len(patterns))
	}
}
