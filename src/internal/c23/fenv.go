// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"sync"
	"sync/atomic"
)

// fenv.h: the floating-point environment, Annex F's.
//
// Each C thread (a goroutine) has its own environment, in its gTable: a
// rounding direction and the exceptions raised. Go's ABI keeps the
// hardware in round-to-nearest at every call, and only Go's state there,
// so the environment is software's, and the hardware's only inside the
// helpers that compute one operation each, from the thread's direction
// to its exceptions (fenv_amd64.s, fenv_arm64.s). C under #pragma STDC
// FENV_ACCESS ON or FENV_ROUND calls them for its arithmetic, conversions
// and comparisons; other C is Go's arithmetic, as C lets it assume the
// default environment there. Elsewhere (fpHardware false: wasm, which has
// no rounding directions or exceptions, and the other architectures),
// fenv.h defines only FE_TONEAREST and no exceptions, as C lets an
// implementation that supports no others.
//
// The macros' values are x86's: the directions are the x87 control word's
// rounding field, the exceptions the SSE status bits.
const (
	FeToNearest  = 0
	FeDownward   = 0x400
	FeUpward     = 0x800
	FeTowardZero = 0xc00
	feRoundMask  = 0xc00

	// FeDynamic is no constant direction (FENV_ROUND FE_DYNAMIC, or no
	// FENV_ROUND): the thread's.
	FeDynamic = -1

	FeInvalid   = 0x01
	FeDivByZero = 0x04
	FeOverflow  = 0x08
	FeUnderflow = 0x10
	FeInexact   = 0x20
	FeAllExcept = FeInvalid | FeDivByZero | FeOverflow | FeUnderflow | FeInexact
)

type fenvState struct {
	Round  int32 // the dynamic rounding direction
	Except int32 // the exceptions raised
}

// fenvUsed is set when a thread first uses its environment: until then,
// every thread's is the default, and the library need not look.
var FenvUsed atomic.Bool

// fenv returns the calling thread's environment.
func Fenv() *fenvState {
	if !FenvUsed.Load() {
		FenvUsed.Store(true)
	}
	return &GSelf().fe
}

// UseFenv is called at start by C that has code under FENV_ACCESS ON: the
// library raises exceptions for it from the start.
func UseFenv() { FenvUsed.Store(true) }

// curRound is the calling thread's rounding direction.
func CurRound() int32 {
	if !FenvUsed.Load() {
		return FeToNearest
	}
	return GSelf().fe.Round
}

// raise raises excepts in the calling thread's environment.
func Raise(excepts int32) { Fenv().Except |= excepts }

// mode is the direction m names: the thread's for FeDynamic.
func (e *fenvState) mode(m int32) int32 {
	if m == FeDynamic {
		return e.Round
	}
	return m
}

// inheritFenv is the environment a new thread starts with: its creator's
// (C23 7.6).
func inheritFenv() fenvState {
	if !FenvUsed.Load() {
		return fenvState{}
	}
	return GSelf().fe
}

// Operations for C under FENV_ACCESS or FENV_ROUND: each rounds in
// direction m (FeDynamic: the thread's) and raises its exceptions in the
// thread's environment.

// Arith is x op y ('+', '-', '*', '/') on doubles.
func Arith(op byte, x, y float64, m int32) float64 {
	e := Fenv()
	ctl := FpControl(e.mode(m))
	var r float64
	var st uint64
	switch op {
	case '+':
		r, st = FpAdd64(ctl, x, y)
	case '-':
		r, st = FpSub64(ctl, x, y)
	case '*':
		r, st = FpMul64(ctl, x, y)
	default:
		r, st = FpDiv64(ctl, x, y)
	}
	e.Except |= FpExcepts(st)
	return r
}

// Arithf is x op y on floats.
func Arithf(op byte, x, y float32, m int32) float32 {
	e := Fenv()
	ctl := FpControl(e.mode(m))
	var r float32
	var st uint64
	switch op {
	case '+':
		r, st = fpAdd32(ctl, x, y)
	case '-':
		r, st = fpSub32(ctl, x, y)
	case '*':
		r, st = fpMul32(ctl, x, y)
	default:
		r, st = fpDiv32(ctl, x, y)
	}
	e.Except |= FpExcepts(st)
	return r
}

// Cmp is x op y ('<', '>', 'l' for <=, 'g' for >=): C's relational
// operators are IEC 60559's signaling comparisons, which raise invalid for
// a NaN. (== and != are quiet: Go's.)
func Cmp(op byte, x, y float64) bool {
	if x != x || y != y {
		Raise(FeInvalid)
		return false
	}
	switch op {
	case '<':
		return x < y
	case '>':
		return x > y
	case 'l':
		return x <= y
	}
	return x >= y
}

// Narrow converts a double to float.
func Narrow(x float64, m int32) float32 {
	e := Fenv()
	r, st := FpNarrow(FpControl(e.mode(m)), x)
	e.Except |= FpExcepts(st)
	return r
}

// IntToDouble converts a 64-bit integer to double.
func IntToDouble(x int64, m int32) float64 {
	e := Fenv()
	r, st := fpIntTo64(FpControl(e.mode(m)), x)
	e.Except |= FpExcepts(st)
	return r
}

// IntToFloat converts a 64-bit integer to float.
func IntToFloat(x int64, m int32) float32 {
	e := Fenv()
	r, st := fpIntTo32(FpControl(e.mode(m)), x)
	e.Except |= FpExcepts(st)
	return r
}

// UintToDouble converts an unsigned 64-bit integer to double: halved,
// keeping a sticky bit, it rounds as it would whole, and doubling is
// exact.
func UintToDouble(x uint64, m int32) float64 {
	if int64(x) >= 0 {
		return IntToDouble(int64(x), m)
	}
	return 2 * IntToDouble(int64(x>>1|x&1), m)
}

// UintToFloat converts an unsigned 64-bit integer to float.
func UintToFloat(x uint64, m int32) float32 {
	if int64(x) >= 0 {
		return IntToFloat(int64(x), m)
	}
	return 2 * IntToFloat(int64(x>>1|x&1), m)
}

// ToInt converts x to a signed integer of bits bits, truncating: out of
// range or a NaN raises invalid (Annex F), a fraction inexact (as the
// hardware does). Out of range, the value is the hardware's, which C
// leaves unspecified: the 32-bit conversion's for 32 bits or fewer.
func ToInt(x float64, bits int) int64 {
	t := math.Trunc(x)
	lim := math.Ldexp(1, bits-1)
	switch {
	case t >= -lim && t < lim:
		if t != x {
			Raise(FeInexact)
		}
		return int64(t)
	case bits <= 32 && t >= -(1<<31) && t < 1<<31:
		Raise(FeInvalid)
		return int64(t)
	case bits <= 32:
		Raise(FeInvalid)
		return math.MinInt32
	}
	Raise(FeInvalid)
	return math.MinInt64
}

// ToUint converts x to an unsigned integer of bits bits, truncating.
func ToUint(x float64, bits int) uint64 {
	t := math.Trunc(x)
	if t >= 0 && t < math.Ldexp(1, bits) {
		if t != x {
			Raise(FeInexact)
		}
		return uint64(t)
	}
	Raise(FeInvalid)
	if t >= -(1<<63) && t < 1<<63 {
		return uint64(int64(t))
	}
	return 1 << 63
}

// InRound calls f in rounding direction m: FENV_ROUND's constant direction
// applies to some library functions as if fesetround had set it (C23
// 7.6.2).
func InRound[T any](m int32, f func() T) T {
	e := Fenv()
	saved := e.Round
	e.Round = m
	defer func() { e.Round = saved }()
	return f()
}

// The library's functions.

func Feclearexcept(excepts int32) int32 {
	if excepts&^FeAllExcept != 0 {
		return 1
	}
	Fenv().Except &^= excepts
	return 0
}

func Feraiseexcept(excepts int32) int32 {
	if excepts&^FeAllExcept != 0 {
		return 1
	}
	Raise(excepts)
	return 0
}

// Fesetexcept sets the flags without raising: the same, with every
// exception masked.
func Fesetexcept(excepts int32) int32 { return Feraiseexcept(excepts) }

func Fetestexcept(excepts int32) int32 {
	if !FenvUsed.Load() {
		return 0
	}
	return GSelf().fe.Except & excepts & FeAllExcept
}

// fexcept_t is an int: the flags.
func Fegetexceptflag(flagp Ptr, excepts int32) int32 {
	if excepts&^FeAllExcept != 0 {
		return 1
	}
	Store32(flagp, uint32(Fenv().Except&excepts))
	return 0
}

func Fesetexceptflag(flagp Ptr, excepts int32) int32 {
	if excepts&^FeAllExcept != 0 {
		return 1
	}
	e := Fenv()
	e.Except = e.Except&^excepts | int32(Load32(flagp))&excepts
	return 0
}

func Fetestexceptflag(flagp Ptr, excepts int32) int32 {
	return int32(Load32(flagp)) & excepts & FeAllExcept
}

func Fegetround() int32 { return CurRound() }

func Fesetround(round int32) int32 {
	switch round {
	case FeToNearest:
	case FeDownward, FeUpward, FeTowardZero:
		if !FpHardware {
			return 1
		}
	default:
		return 1
	}
	Fenv().Round = round
	return 0
}

// fenv_t is an int: the direction and the flags, whose bits differ.
func Fegetenv(envp Ptr) int32 {
	e := Fenv()
	Store32(envp, uint32(e.Round|e.Except))
	return 0
}

func Feholdexcept(envp Ptr) int32 {
	Fegetenv(envp)
	Fenv().Except = 0
	return 0
}

func Fesetenv(envp Ptr) int32 {
	v := int32(Load32(envp))
	e := Fenv()
	e.Round = v & feRoundMask
	e.Except = v & FeAllExcept
	return 0
}

func Feupdateenv(envp Ptr) int32 {
	raised := Fenv().Except
	Fesetenv(envp)
	Raise(raised)
	return 0
}

// femode_t is an int: the direction.
func Fegetmode(modep Ptr) int32 {
	Store32(modep, uint32(CurRound()))
	return 0
}

func Fesetmode(modep Ptr) int32 {
	Fenv().Round = int32(Load32(modep)) & feRoundMask
	return 0
}

// feDfl is what FE_DFL_ENV and FE_DFL_MODE point to: round to nearest,
// no exceptions.
// It is made when first asked for, so that a program that does not ask
// does not link what allocating it takes.
var (
	feDflOnce sync.Once
	feDfl     Ptr
)

func FeDfl() Ptr {
	feDflOnce.Do(func() { feDfl = Malloc(8) })
	return feDfl
}

// FltRounds is FLT_ROUNDS: the thread's direction, as float.h numbers
// them.
func FltRounds() int32 {
	switch CurRound() {
	case FeTowardZero:
		return 0
	case FeUpward:
		return 2
	case FeDownward:
		return 3
	}
	return 1
}
