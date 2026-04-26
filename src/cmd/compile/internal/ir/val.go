// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"go/constant"

	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
)

func ConstType(n Node) constant.Kind {
	if n == nil || n.Op() != OLITERAL {
		return constant.Unknown
	}
	return n.Val().Kind()
}

// IntVal returns v converted to int64.
// Note: if t is uint64, very large values will be converted to negative int64.
func IntVal(gd *base.Invocation, t *types.Type, v constant.Value) int64 {
	if t.IsUnsigned() {
		if x, ok := constant.Uint64Val(v); ok {
			return int64(x)
		}
	} else {
		if x, ok := constant.Int64Val(v); ok {
			return x
		}
	}
	gd.Fatalf("%v out of range for %v", v, t)
	panic("unreachable")
}

func AssertValidTypeForConst(gd *base.Invocation, t *types.Type, v constant.Value) {
	if !ValidTypeForConst(gd, t, v) {
		gd.Fatalf("%v (%v) does not represent %v (%v)", t, t.Kind(), v, v.Kind())
	}
}

func ValidTypeForConst(gd *base.Invocation, t *types.Type, v constant.Value) bool {
	switch v.Kind() {
	case constant.Unknown:
		return OKForConst[t.Kind()]
	case constant.Bool:
		return t.IsBoolean()
	case constant.String:
		return t.IsString()
	case constant.Int:
		return t.IsInteger()
	case constant.Float:
		return t.IsFloat()
	case constant.Complex:
		return t.IsComplex()
	}

	gd.Fatalf("unexpected constant kind: %v", v)
	panic("unreachable")
}

var OKForConst [types.NTYPE]bool

// Int64Val returns n as an int64.
// n must be an integer or rune constant.
func Int64Val(n Node) int64 {
	gd := n.compiler()
	if !IsConst(n, constant.Int) {
		gd.Fatalf("Int64Val(%v)", n)
	}
	x, ok := constant.Int64Val(n.Val())
	if !ok {
		gd.Fatalf("Int64Val(%v)", n)
	}
	return x
}

// Uint64Val returns n as a uint64.
// n must be an integer or rune constant.
func Uint64Val(n Node) uint64 {
	gd := n.compiler()
	if !IsConst(n, constant.Int) {
		gd.Fatalf("Uint64Val(%v)", n)
	}
	x, ok := constant.Uint64Val(n.Val())
	if !ok {
		gd.Fatalf("Uint64Val(%v)", n)
	}
	return x
}

// BoolVal returns n as a bool.
// n must be a boolean constant.
func BoolVal(n Node) bool {
	gd := n.compiler()
	if !IsConst(n, constant.Bool) {
		gd.Fatalf("BoolVal(%v)", n)
	}
	return constant.BoolVal(n.Val())
}

// StringVal returns the value of a literal string Node as a string.
// n must be a string constant.
func StringVal(n Node) string {
	gd := n.compiler()
	if !IsConst(n, constant.String) {
		gd.Fatalf("StringVal(%v)", n)
	}
	return constant.StringVal(n.Val())
}
