// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "fmt"

// __int128 in Go: c23.Int128 and c23.Uint128, whose operators are
// methods.

func is128(t *ctype) bool { return t.kind == kInt128 || t.kind == kUInt128 }

var methods128 = map[string]string{
	"+": "Add", "-": "Sub", "*": "Mul", "/": "Div", "%": "Rem",
	"&": "And", "|": "Or", "^": "Xor", "<<": "Shl", ">>": "Shr",
}

// arith128 is x op y in t, an __int128 type (y is a count for shifts).
func arith128(op, x, y string) string {
	if op == "<<" || op == ">>" {
		return fmt.Sprintf("%s.%s(uint(%s))", x, methods128[op], y)
	}
	return fmt.Sprintf("%s.%s(%s)", x, methods128[op], y)
}

// convert128 converts to or from an __int128 type.
func convert128(x string, from, to *ctype) string {
	fromPtr := from.isPtr() || from.kind == kNullptr
	switch {
	case is128(to) && is128(from):
		switch {
		case from.kind == to.kind:
			return x
		case to.kind == kInt128:
			return x + ".I()"
		}
		return x + ".U()"
	case is128(to):
		pre := "I128"
		if to.kind == kUInt128 {
			pre = "U128"
		}
		switch {
		case fromPtr:
			return fmt.Sprintf("c23.%sU(uint64(c23.Addr(%s)))", pre, x)
		case from.isFloat():
			return fmt.Sprintf("c23.%sF(float64(%s))", pre, x)
		case from.isUnsigned():
			return fmt.Sprintf("c23.%sU(uint64(%s))", pre, x)
		}
		return fmt.Sprintf("c23.%s(int64(%s))", pre, x)
	}
	switch {
	case to.kind == kBool:
		return fmt.Sprintf("c23.B2U8(!%s.IsZero())", x)
	case to.isFloat():
		return fmt.Sprintf("%s(%s.Float64())", goType(to), x)
	case to.isPtr() || to.kind == kNullptr:
		return fmt.Sprintf("c23.FromInt(uintptr(%s.Lo))", x)
	}
	return wrapBits(fmt.Sprintf("%s(%s.Lo)", goType(to), x), to)
}
