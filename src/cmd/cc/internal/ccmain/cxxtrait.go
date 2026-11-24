// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// The built-in type traits (__is_enum(T), __is_base_of(B, D), ...): what
// <type_traits> is made of, as in GCC and clang.

// isTraitName reports whether name is a built-in type trait.
func isTraitName(name string) bool {
	return strings.HasPrefix(name, "__is_") || strings.HasPrefix(name, "__has_") && name != "__has_include"
}

// dummy is an expression of type t that is never evaluated: an lvalue
// if t is an lvalue reference.
func (p *parser) dummy(t *ctype, at pos) expr {
	if t.isRef() {
		d := &deref{node{t.elem, at}, &goVal{node{ptrTo(t.elem), at}, ""}}
		if t.ref == 2 {
			p.xvalue(d)
		}
		return d
	}
	return &goVal{node{t.unqual(), at}, ""}
}

// typeTrait parses a built-in type trait's operands, after its name,
// and gives its value.
func (p *parser) typeTrait(name string, at pos) expr {
	p.expect("(")
	var ts []*ctype
	for !p.is(")") {
		if s := p.lookupAt0(); s != nil && s.kind == symPack && p.isAt(1, "...") {
			p.pos += 2
			for _, a := range s.pack {
				ts = append(ts, a.t)
			}
		} else {
			ts = append(ts, p.typeName())
		}
		if !p.accept(",") {
			break
		}
	}
	p.expect(")")
	if len(ts) == 0 {
		p.errorAt(at, "%s needs a type", name)
	}
	for _, t := range ts {
		if hasTParam(t) {
			return &depConst{intConst{node{tBool, at}, 0}, -1}
		}
	}
	return &intConst{node{tBool, at}, b2i(p.trait(name, ts, at))}
}

func (p *parser) trait(name string, ts []*ctype, at pos) bool {
	t := ts[0]
	u := t
	if u.isRef() {
		u = u.elem
	}
	p.complete(baseElem(u))
	ci := elemClass(u)
	second := func() *ctype {
		if len(ts) < 2 {
			p.errorAt(at, "%s needs two types", name)
		}
		return ts[1]
	}
	scalar := func(t *ctype) bool { return t.isScalar() && !t.isRef() }
	switch name {
	case "__is_enum":
		return t.enum != nil
	case "__is_class":
		return t.kind == kStruct && !t.rec.union
	case "__is_union":
		return t.kind == kStruct && t.rec.union
	case "__is_same", "__is_same_as":
		return sameType(t, second())
	case "__is_base_of":
		d := second()
		p.complete(d)
		return t.kind == kStruct && d.kind == kStruct && !t.rec.union && (t.rec == d.rec || isBaseOf(t, d))
	case "__is_void":
		return t.kind == kVoid
	case "__is_integral":
		return t.isInteger() && t.enum == nil
	case "__is_floating_point":
		return t.isFloat()
	case "__is_arithmetic":
		return t.isArith() && t.enum == nil
	case "__is_signed":
		return t.isArith() && t.enum == nil && !t.isUnsigned()
	case "__is_unsigned":
		return t.isInteger() && t.enum == nil && t.isUnsigned()
	case "__is_pointer":
		return t.kind == kPtr && t.ref == 0
	case "__is_nullptr":
		return t.kind == kNullptr
	case "__is_array":
		return t.kind == kArray
	case "__is_function":
		return t.kind == kFunc
	case "__is_reference":
		return t.isRef()
	case "__is_lvalue_reference":
		return t.isRef() && t.ref == 1
	case "__is_rvalue_reference":
		return t.isRef() && t.ref == 2
	case "__is_const":
		return t.q&qConst != 0
	case "__is_volatile":
		return t.q&qVolatile != 0
	case "__is_member_pointer", "__is_member_object_pointer", "__is_member_function_pointer":
		return false
	case "__is_scalar":
		return scalar(t)
	case "__is_fundamental":
		return t.isArith() && t.enum == nil || t.kind == kVoid || t.kind == kNullptr
	case "__is_object":
		return t.kind != kFunc && t.kind != kVoid && !t.isRef()
	case "__is_compound":
		return !(t.isArith() && t.enum == nil || t.kind == kVoid || t.kind == kNullptr)
	case "__is_empty":
		return ci != nil && t.kind == kStruct && ci.empty
	case "__is_polymorphic", "__has_virtual_destructor":
		if ci == nil || t.kind != kStruct {
			return false
		}
		if name == "__is_polymorphic" {
			return ci.poly
		}
		for _, v := range ci.vfuncs {
			if v.t.fn.dtor {
				return true
			}
		}
		return false
	case "__is_abstract":
		if ci != nil && t.kind == kStruct {
			for _, v := range ci.vfuncs {
				if v.pure {
					return true
				}
			}
		}
		return false
	case "__is_final":
		return false
	case "__is_aggregate":
		return u.kind == kArray || ci != nil && !ci.userCtor && !ci.poly
	case "__is_trivially_copyable", "__has_trivial_copy", "__is_trivially_relocatable":
		return !t.isRef() && !needsCopy(u) && !needsDtor(u) && u.kind != kVoid && u.kind != kFunc
	case "__is_trivially_destructible", "__has_trivial_destructor":
		return !needsDtor(u) && u.kind != kVoid && u.kind != kFunc
	case "__is_destructible", "__is_nothrow_destructible":
		return u.kind != kVoid && u.kind != kFunc
	case "__is_trivial", "__is_pod", "__is_standard_layout", "__is_literal_type", "__is_literal":
		if name == "__is_standard_layout" || name == "__is_literal_type" || name == "__is_literal" {
			return ci == nil || !ci.poly
		}
		return !needsCtor(u) && !needsCopy(u) && !needsDtor(u)
	case "__is_trivially_constructible", "__has_trivial_constructor":
		switch len(ts) {
		case 1:
			return !needsCtor(u) && !t.isRef()
		case 2:
			a := ts[1]
			if a.isRef() {
				a = a.elem
			}
			if u.kind == kStruct {
				return sameType(a.unqual(), u.unqual()) && !needsCopy(u)
			}
			return p.constructible(ts, at)
		}
		return false
	case "__is_trivially_assignable", "__has_trivial_assign":
		if len(ts) == 1 {
			return !needsCopy(u)
		}
		a := ts[1]
		if a.isRef() {
			a = a.elem
		}
		if u.kind == kStruct {
			return sameType(a.unqual(), u.unqual()) && !needsCopy(u)
		}
		return p.assignable(ts, at)
	case "__is_constructible", "__is_nothrow_constructible":
		return p.constructible(ts, at)
	case "__is_assignable", "__is_nothrow_assignable":
		return p.assignable(ts, at)
	case "__is_convertible", "__is_convertible_to", "__is_nothrow_convertible":
		to := second()
		if t.kind == kVoid || to.kind == kVoid {
			return t.kind == kVoid && to.kind == kVoid
		}
		if to.kind == kArray || to.kind == kFunc {
			return false
		}
		return p.convRank(p.dummy(t, at), to) != rkNone
	}
	p.errorAt(at, "the type trait %s is not supported", name)
	return false
}

// constructible reports whether an object of type ts[0] can be
// initialized with values of the types ts[1:].
func (p *parser) constructible(ts []*ctype, at pos) bool {
	t := ts[0]
	if t.kind == kVoid || t.kind == kFunc || t.kind == kArray && t.n < 0 {
		return false
	}
	var args []expr
	for _, a := range ts[1:] {
		if a.kind == kVoid {
			return false
		}
		args = append(args, p.dummy(a, at))
	}
	if t.isRef() {
		return len(args) == 1 && p.convRank(args[0], t) != rkNone
	}
	if t.kind == kStruct {
		p.complete(t)
		if !t.rec.complete {
			return false
		}
		if len(args) == 1 {
			if at := args[0].ty(); at.kind == kStruct && (sameType(at.unqual(), t.unqual()) || isBaseOf(t, at)) {
				// A copy: unless it is deleted.
				s := p.resolve(ctors(t), nil, args, at0(args[0]), "", true)
				return s != nil && !s.deleted
			}
		}
		s := p.resolve(ctors(t), nil, args, at, "", true)
		if s != nil {
			return !s.deleted
		}
		return len(args) == 1 && args[0].ty().kind == kStruct && p.classConvRank(args[0], args[0].ty().unqual(), t.unqual()) != rkNone
	}
	switch len(args) {
	case 0:
		return true
	case 1:
		return p.convRank(args[0], t.unqual()) != rkNone
	}
	return false
}

func at0(e expr) pos { return e.at() }

// assignable reports whether a value of type ts[1] can be assigned to
// an expression of type ts[0].
func (p *parser) assignable(ts []*ctype, at pos) bool {
	if len(ts) < 2 {
		p.errorAt(at, "__is_assignable needs two types")
	}
	t, a := ts[0], p.dummy(ts[1], at)
	if t.isRef() {
		u := t.elem
		if u.kind == kStruct {
			p.complete(u)
			ci := classOf(u)
			if ci == nil {
				return false
			}
			head := ci.scope.vars["operator="]
			if head == nil {
				return false
			}
			s := p.resolve(funcSet(head), p.dummy(t, at), []expr{a}, at, "", true)
			return s != nil && !s.deleted
		}
		return t.ref == 1 && u.q&qConst == 0 && p.convRank(a, u.unqual()) != rkNone
	}
	if t.kind == kStruct {
		p.complete(t)
		ci := classOf(t)
		if ci == nil {
			return false
		}
		head := ci.scope.vars["operator="]
		if head == nil {
			return false
		}
		s := p.resolve(funcSet(head), p.dummy(t, at), []expr{a}, at, "", true)
		return s != nil && !s.deleted
	}
	return false
}
