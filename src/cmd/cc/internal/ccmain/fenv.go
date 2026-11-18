// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"internal/buildcfg"
	"math"
	"math/big"
	"strings"
)

// The floating-point environment (internal/c23's fenv.go). Go's arithmetic
// rounds to nearest and raises nothing C can see, which C lets code assume
// unless it says otherwise: under #pragma STDC FENV_ACCESS ON (the thread's
// rounding direction and exceptions) or FENV_ROUND (a constant direction),
// the parser marks the floating-point operations and conversions it makes
// (unit.fenv), and the generator calls internal/c23's helpers for them,
// which run the one instruction in the direction asked and raise its
// exceptions in the thread's environment. Under FP_CONTRACT OFF, products
// are rounded alone, not fused with an addition as Go may.

// fenvPragmas is the state of the floating-point pragmas.
type fenvPragmas struct {
	access     bool  // FENV_ACCESS ON
	round      int32 // FENV_ROUND's constant direction, or feDynamic
	noContract bool  // FP_CONTRACT OFF
}

// The rounding directions, as fenv.h and internal/c23 have them.
const (
	feToNearest  = 0
	feDownward   = 0x400
	feUpward     = 0x800
	feTowardZero = 0xc00
	feDynamic    = -1
)

var defaultPragmas = fenvPragmas{round: feDynamic}

// fenvHardware reports whether the target has rounding directions and
// exception flags (internal/c23's fpHardware): if not, fenv.h has only
// FE_TONEAREST, and Annex F is not claimed.
func fenvHardware() bool {
	return buildcfg.GOARCH == "amd64" || buildcfg.GOARCH == "arm64"
}

// fenvMacros are the predefined macros where it does.
const fenvMacros = `
#define __GD_FENV__ 1
#define __STDC_IEC_559__ 1
#define __STDC_IEC_60559_BFP__ 202311L
`

// runtime reports whether operations need the run-time environment: its
// direction or its exceptions.
func (s fenvPragmas) runtime() bool {
	return s.access || s.round != feDynamic && s.round != feToNearest
}

func (s fenvPragmas) active() bool { return s.runtime() || s.noContract }

// mode is the direction argument of internal/c23's helpers.
func (s fenvPragmas) mode() string { return fmt.Sprint(s.round) }

// stdcPragma applies #pragma STDC t.text t.str.
func (p *parser) stdcPragma(t token) {
	switch t.text {
	case "FENV_ACCESS":
		p.fe.access = t.str == "ON"
	case "FP_CONTRACT":
		p.fe.noContract = t.str == "OFF"
	case "FENV_ROUND":
		dirs := map[string]int32{"FE_TONEAREST": feToNearest, "FE_DYNAMIC": feDynamic}
		if fenvHardware() {
			dirs["FE_DOWNWARD"], dirs["FE_UPWARD"], dirs["FE_TOWARDZERO"] = feDownward, feUpward, feTowardZero
		}
		d, ok := dirs[t.str]
		if !ok {
			p.errorAt(pos{t.file, t.line}, "#pragma STDC FENV_ROUND %s: not a rounding direction %s supports", t.str, buildcfg.GOARCH)
		}
		p.fe.round = d
	}
}

// fenvMark records e, an operation or conversion of floating values, for
// the generator, if pragmas apply to it: they do not to an initializer that
// runs as if at translation time (C23 F.8.5).
func (p *parser) fenvMark(e expr) expr {
	if !p.fe.active() || p.translationTime > 0 {
		return e
	}
	if p.u.fenv == nil {
		p.u.fenv = map[expr]fenvPragmas{}
	}
	p.u.fenv[e] = p.fe
	return e
}

// foldsAtRuntime reports whether constant floating operations are left to
// run (C23 F.8.4) unless they are exact: then any direction gives the same
// value, raising nothing.
func (p *parser) foldsAtRuntime() bool {
	return p.fe.runtime() && p.translationTime == 0
}

// exactOp reports whether x op y is v exactly, all of them finite: v is
// the operation rounded to its type.
func exactOp(op string, x, y, v float64) bool {
	for _, f := range []float64{x, y, v} {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return false
		}
	}
	bx, by, bv := new(big.Float).SetFloat64(x), new(big.Float).SetFloat64(y), new(big.Float).SetFloat64(v)
	r := new(big.Float).SetPrec(2200)
	switch op {
	case "+", "-":
		if v == 0 {
			// x - x is -0 rounding downward.
			return false
		}
		if op == "+" {
			r.Add(bx, by)
		} else {
			r.Sub(bx, by)
		}
	case "*":
		r.Mul(bx, by)
	case "/":
		if y == 0 {
			return false
		}
		r.Mul(bv, by)
		return r.Cmp(bx) == 0
	default:
		return true // a comparison of numbers
	}
	return r.Cmp(bv) == 0
}

// The generator.

// fenvOf is the pragmas that apply to e, if any do.
func (g *gen) fenvOf(e expr) (fenvPragmas, bool) {
	s, ok := g.u.fenv[e]
	return s, ok
}

// fenvArith is x op y, of floating type t, under s; "" if it is Go's.
func fenvArith(op, x, y string, t *ctype, s fenvPragmas) string {
	switch {
	case !t.isFloat() || !strings.Contains("+-*/", op):
	case s.runtime() && t.kind == kFloat:
		return fmt.Sprintf("c23.Arithf('%s', %s, %s, %s)", op, x, y, s.mode())
	case s.runtime():
		return fmt.Sprintf("c23.Arith('%s', %s, %s, %s)", op, x, y, s.mode())
	case s.noContract && op == "*":
		// An explicit conversion rounds: Go fuses no further.
		return fmt.Sprintf("%s(%s * %s)", goType(t), x, y)
	}
	return ""
}

// fenvCompare is x op y, relational, of floating values, under
// FENV_ACCESS: a NaN raises invalid; "" if it is Go's.
func fenvCompare(op, x, y string, t *ctype, s fenvPragmas) string {
	c := map[string]string{"<": "'<'", ">": "'>'", "<=": "'l'", ">=": "'g'"}[op]
	if !s.access || !t.isFloat() || c == "" {
		return ""
	}
	return fmt.Sprintf("c23.Cmp(%s, float64(%s), float64(%s))", c, x, y)
}

// fenvConvert converts the Go value x of C type from to C type to under s,
// where the conversion can round or raise; ok false where it is Go's.
func fenvConvert(x string, from, to *ctype, s fenvPragmas) (string, bool) {
	if is128(from) || is128(to) || from.kind == kBool || to.kind == kBool {
		return "", false
	}
	m := s.mode()
	switch {
	case to.kind == kFloat && (from.kind == kDouble || from.kind == kLDouble) && s.runtime():
		return fmt.Sprintf("c23.Narrow(%s, %s)", x, m), true
	case to.isFloat() && from.isInteger() && s.runtime():
		bits := from.sizeof() * 8
		if from.isBitInt() {
			bits = from.n
		}
		if to.kind == kFloat && bits <= 16 || to.kind != kFloat && bits <= 32 {
			return "", false // exact
		}
		fn := "IntToDouble"
		if to.kind == kFloat {
			fn = "IntToFloat"
		}
		arg := "int64"
		if from.isUnsigned() && bits == 64 {
			fn, arg = strings.Replace(fn, "Int", "Uint", 1), "uint64"
		}
		return fmt.Sprintf("c23.%s(%s(%s), %s)", fn, arg, x, m), true
	case to.isInteger() && from.isFloat() && s.access:
		bits := to.sizeof() * 8
		if to.isBitInt() {
			bits = to.n
		}
		fn := "ToInt"
		if to.isUnsigned() {
			fn = "ToUint"
		}
		return wrapBits(fmt.Sprintf("%s(c23.%s(float64(%s), %d))", goType(to), fn, x, bits), to), true
	}
	return "", false
}

// convertUnder converts as fenvConvert does, or as Go does.
func convertUnder(x string, from, to *ctype, s fenvPragmas) string {
	if code, ok := fenvConvert(x, from, to, s); ok {
		return code
	}
	return convertCode(x, from, to)
}

// roundedFuncs are the library functions FENV_ROUND's constant direction
// applies to (C23 7.6.2), with their float and long double forms.
var roundedFuncs = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(`acos asin atan atan2 cos sin tan acosh asinh atanh cosh sinh tanh
		exp exp2 expm1 log log10 log1p log2 cbrt hypot pow sqrt erf erfc lgamma tgamma fma
		acospi asinpi atanpi atan2pi cospi sinpi tanpi exp10 exp10m1 exp2m1 log10p1 log2p1 logp1
		compoundn pown powr rootn rsqrt rint nearbyint lrint llrint`) {
		m[f], m[f+"f"], m[f+"l"] = true, true, true
	}
	for _, f := range strings.Fields(`fadd faddl daddl fsub fsubl dsubl fmul fmull dmull fdiv fdivl ddivl
		ffma ffmal dfmal fsqrt fsqrtl dsqrtl atof strtod strtof strtold strfromd strfromf strfroml
		printf fprintf sprintf snprintf vprintf vfprintf vsprintf vsnprintf
		scanf fscanf sscanf vscanf vfscanf vsscanf wcstod wcstof wcstold
		wprintf fwprintf swprintf vwprintf vfwprintf vswprintf wscanf fwscanf swscanf vwscanf vfwscanf vswscanf`) {
		m[f] = true
	}
	return m
}()

// fenvCall wraps code, a call of c, in FENV_ROUND's direction, if one
// applies to the function called.
func (g *gen) fenvCall(c *call, code string) string {
	s, ok := g.fenvOf(c)
	if !ok || s.round == feDynamic || c.t.kind == kVoid {
		return code
	}
	fn := c.fn
	if d, ok := fn.(*decay); ok {
		fn = d.x // the function, as a pointer
	}
	v, ok := fn.(*varRef)
	if !ok || !roundedFuncs[v.sym.name] {
		return code
	}
	return fmt.Sprintf("c23.InRound(%d, func() %s { return %s })", s.round, goType(c.t), code)
}

// floatFits reports whether f, truncated, is a value of integer type t: if
// not, converting it raises invalid.
func floatFits(f float64, t *ctype) bool {
	bits := int(t.sizeof() * 8)
	if t.isBitInt() {
		bits = int(t.n)
	}
	tr := math.Trunc(f)
	if t.isUnsigned() {
		return tr >= 0 && tr < math.Ldexp(1, bits)
	}
	return tr >= -math.Ldexp(1, bits-1) && tr < math.Ldexp(1, bits-1)
}

// exactInt reports whether v is the integer i (unsigned if u) exactly.
func exactInt(v float64, i int64, u bool) bool {
	bi := new(big.Float).SetInt64(i)
	if u {
		bi.SetUint64(uint64(i))
	}
	return new(big.Float).SetFloat64(v).Cmp(bi) == 0
}
