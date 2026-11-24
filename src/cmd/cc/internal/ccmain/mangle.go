// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"strconv"
	"strings"
)

// Names with C++ linkage, as the Itanium C++ ABI mangles them
// (doc/gd/cxx.md): the mangled name is the symbol's name in everything
// after the parser, so units link by it.

// A mangler mangles one name: it keeps the name's substitutions.
type mangler struct {
	b    strings.Builder
	subs []string // the components that can be substituted, by their full mangling
}

// operatorCodes are the codes of operator names. A unary operator that
// shares its spelling with a binary one has its code in unaryCodes.
var operatorCodes = map[string]string{
	"new": "nw", "new[]": "na", "delete": "dl", "delete[]": "da",
	"+": "pl", "-": "mi", "*": "ml", "/": "dv", "%": "rm", "&": "an", "|": "or", "^": "eo", "~": "co",
	"=": "aS", "+=": "pL", "-=": "mI", "*=": "mL", "/=": "dV", "%=": "rM", "&=": "aN", "|=": "oR", "^=": "eO",
	"<<": "ls", ">>": "rs", "<<=": "lS", ">>=": "rS", "==": "eq", "!=": "ne", "<": "lt", ">": "gt",
	"<=": "le", ">=": "ge", "<=>": "ss", "!": "nt", "&&": "aa", "||": "oo", "++": "pp", "--": "mm",
	",": "cm", "->*": "pm", "->": "pt", "()": "cl", "[]": "ix",
}

var unaryCodes = map[string]string{"+": "ps", "-": "ng", "*": "de", "&": "ad"}

var builtinCodes = [...]string{
	kVoid: "v", kBool: "b", kChar: "c", kSChar: "a", kUChar: "h", kShort: "s", kUShort: "t",
	kInt: "i", kUInt: "j", kLong: "l", kULong: "m", kLLong: "x", kULLong: "y",
	kInt128: "n", kUInt128: "o", kIntCap: "l", kUIntCap: "m",
	kFloat: "f", kDouble: "d", kLDouble: "e", kNullptr: "Dn", kVaList: "P13__va_list_tag",
}

func sourceName(s string) string { return strconv.Itoa(len(s)) + s }

// seqID is a substitution's sequence id: none for the first, then 0, 1,
// ... in base 36.
func seqID(i int) string {
	if i == 0 {
		return "S_"
	}
	return "S" + strings.ToUpper(strconv.FormatInt(int64(i-1), 36)) + "_"
}

// sub writes the substitution for the component whose full mangling is
// key, if there is one.
func (m *mangler) sub(key string) bool {
	for i, s := range m.subs {
		if s == key {
			m.b.WriteString(seqID(i))
			return true
		}
	}
	return false
}

// scopeNames are the names of the namespaces and classes sc is in, the
// outermost first; sc's own last.
func scopeNames(sc *scope) []string {
	var names []string
	for ; sc != nil; sc = sc.parent {
		switch sc.kind {
		case skNamespace:
			if sc.parent == nil {
				continue // the global namespace
			}
			if sc.name == "" {
				names = append(names, "_GLOBAL__N_1")
			} else {
				names = append(names, sc.name)
			}
		case skClass:
			names = append(names, sc.name)
		}
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return names
}

// prefix writes the prefix of a nested name, the scopes names, with
// substitutions, and returns the prefix's full mangling.
func (m *mangler) prefix(names []string) string {
	key := ""
	// The longest prefix already seen.
	start := 0
	for n := len(names); n > 0; n-- {
		k := ""
		for _, s := range names[:n] {
			k += sourceName(s)
		}
		if n == 1 && names[0] == "std" {
			m.b.WriteString("St")
			key, start = k, n
			break
		}
		if m.sub("N" + k) {
			key, start = k, n
			break
		}
	}
	for _, s := range names[start:] {
		m.b.WriteString(sourceName(s))
		key += sourceName(s)
		m.subs = append(m.subs, "N"+key)
	}
	return key
}

// name writes the name of an entity in the scope sc: unqualified is its
// own name's mangling (a source name, an operator's code, C1, D1).
func (m *mangler) name(sc *scope, unqualified, cv string) {
	names := scopeNames(sc)
	switch {
	case len(names) == 0:
		m.b.WriteString(unqualified)
	case len(names) == 1 && names[0] == "std" && cv == "":
		m.b.WriteString("St" + unqualified)
	default:
		m.b.WriteString("N" + cv)
		m.prefix(names)
		m.b.WriteString(unqualified)
		m.b.WriteString("E")
	}
}

// rawType is t's mangling without substitutions: its key.
func rawType(t *ctype) string {
	var m mangler
	m.typ(t, true)
	return m.b.String()
}

// typ writes t's mangling. With raw, no substitutions are used or made.
func (m *mangler) typ(t *ctype, raw bool) {
	q := ""
	if t.q&qRestrict != 0 && t.kind == kPtr {
		q += "r"
	}
	if t.q&qVolatile != 0 {
		q += "V"
	}
	if t.q&qConst != 0 {
		q += "K"
	}
	if q != "" && t.kind != kFunc {
		key := ""
		if !raw {
			key = rawType(t)
			if m.sub(key) {
				return
			}
		}
		m.b.WriteString(q)
		u := *t
		u.q = 0
		m.typ(&u, raw)
		if !raw {
			m.subs = append(m.subs, key)
		}
		return
	}
	if t.enum != nil && t.enum.tag != "" {
		m.named(t.enum.sc, t.enum.tag, raw)
		return
	}
	switch t.kind {
	case kPtr, kArray, kFunc, kStruct, kComplex:
	case kBitInt:
		m.b.WriteString("DB" + itoa(t.n) + "_")
		return
	case kUBitInt:
		m.b.WriteString("DU" + itoa(t.n) + "_")
		return
	default:
		m.b.WriteString(builtinCodes[t.kind])
		return
	}
	key := ""
	if !raw {
		key = rawType(t)
		if m.sub(key) {
			return
		}
	}
	switch t.kind {
	case kPtr:
		switch t.ref {
		case 0:
			m.b.WriteString("P")
		case 1:
			m.b.WriteString("R")
		default:
			m.b.WriteString("O")
		}
		m.typ(t.elem, raw)
	case kComplex:
		m.b.WriteString("C")
		m.typ(t.elem, raw)
	case kArray:
		m.b.WriteString("A")
		if t.n >= 0 {
			m.b.WriteString(itoa(t.n))
		}
		m.b.WriteString("_")
		m.typ(t.elem, raw)
	case kFunc:
		m.b.WriteString("F")
		m.typ(t.fn.ret, raw)
		m.params(t.fn, raw)
		m.b.WriteString("E")
	case kStruct:
		tag := t.rec.tag
		if tag == "" {
			tag = "._" + itoa(int64(t.rec.id))
		}
		m.named(t.rec.sc, tag, raw)
		return // a class's name is its own substitution, made by named
	}
	if !raw {
		m.subs = append(m.subs, key)
	}
}

// named writes the name of the class or enumeration tag declared in sc.
func (m *mangler) named(sc *scope, tag string, raw bool) {
	names := append(scopeNames(sc), tag)
	if raw {
		if len(names) == 1 {
			m.b.WriteString(sourceName(tag))
			return
		}
		m.b.WriteString("N")
		for _, s := range names {
			m.b.WriteString(sourceName(s))
		}
		m.b.WriteString("E")
		return
	}
	if len(names) == 1 {
		if m.sub("N" + sourceName(tag)) {
			return
		}
		m.b.WriteString(sourceName(tag))
		m.subs = append(m.subs, "N"+sourceName(tag))
		return
	}
	k := ""
	for _, s := range names {
		k += sourceName(s)
	}
	if m.sub("N" + k) {
		return
	}
	if len(names) == 2 && names[0] == "std" {
		m.b.WriteString("St" + sourceName(tag))
		m.subs = append(m.subs, "N"+k)
		return
	}
	m.b.WriteString("N")
	m.prefix(names)
	m.b.WriteString("E")
}

// params writes a function's parameter types: without their top-level
// qualifiers, v if there are none.
func (m *mangler) params(ft *funcType, raw bool) {
	ps := ft.params
	if ft.recv != nil {
		ps = ps[1:]
	}
	if len(ps) == 0 && !ft.variadic {
		m.b.WriteString("v")
	}
	for _, pt := range ps {
		if pt.q != 0 && pt.ref == 0 {
			pt = pt.unqual()
		}
		m.typ(pt, raw)
	}
	if ft.variadic {
		m.b.WriteString("z")
	}
}

// mangleFunc is the mangled name of the function named name (in the
// source: an identifier, or "operator+", "~T", ...) declared in sc with
// type ft.
func mangleFunc(sc *scope, name string, ft *funcType) string {
	var m mangler
	m.b.WriteString("_Z")
	cv := ""
	if ft.recv != nil && ft.recv.elem.q&qConst != 0 {
		cv = "K"
	}
	var unq string
	switch {
	case ft.ctor:
		unq = "C1"
	case ft.dtor:
		unq = "D1"
	case ft.conv != nil:
		// The type's substitutions come after the prefix's: written
		// below, in place.
		unq = "cv"
	case strings.HasPrefix(name, "operator"):
		op := strings.TrimPrefix(name, "operator")
		nparams := len(ft.params)
		code := operatorCodes[op]
		if u, ok := unaryCodes[op]; ok && nparams == 1 {
			code = u
		}
		unq = code
	default:
		unq = sourceName(name)
	}
	if ft.conv != nil {
		names := scopeNames(sc)
		m.b.WriteString("N" + cv)
		m.prefix(names)
		m.b.WriteString("cv")
		m.typ(ft.conv, false)
		m.b.WriteString("E")
	} else {
		m.name(sc, unq, cv)
	}
	m.params(ft, false)
	return m.b.String()
}

// mangleVar is the mangled name of the variable name declared in sc: its
// own name, in the global namespace.
func mangleVar(sc *scope, name string) string {
	if len(scopeNames(sc)) == 0 {
		return name
	}
	var m mangler
	m.b.WriteString("_Z")
	m.name(sc, sourceName(name), "")
	return m.b.String()
}
