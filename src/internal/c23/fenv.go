// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

// fenv.h. Go's floating point rounds to nearest, and keeps no exception
// flags a program can see: fenv.h defines FE_TONEAREST, and no exception
// macros (FE_ALL_EXCEPT is 0), as C lets an implementation that supports
// no others. Asking for none of them succeeds.

func Feclearexcept(excepts int32) int32 { return B2I(excepts != 0) }
func Feraiseexcept(excepts int32) int32 { return B2I(excepts != 0) }
func Fesetexcept(excepts int32) int32   { return B2I(excepts != 0) }
func Fetestexcept(excepts int32) int32  { return 0 }

func Fegetexceptflag(flagp Ptr, excepts int32) int32 {
	Store32(flagp, 0)
	return 0
}

func Fesetexceptflag(flagp Ptr, excepts int32) int32  { return B2I(excepts != 0) }
func Fetestexceptflag(flagp Ptr, excepts int32) int32 { return 0 }

// feToNearest is FE_TONEAREST, the one rounding direction.
const feToNearest = 0

func Fegetround() int32 { return feToNearest }

func Fesetround(round int32) int32 { return B2I(round != feToNearest) }

// The environments and modes hold nothing: there is one.
func Fegetenv(envp Ptr) int32 {
	Store32(envp, 0)
	return 0
}

func Feholdexcept(envp Ptr) int32 { return Fegetenv(envp) }
func Fesetenv(envp Ptr) int32     { return 0 }
func Feupdateenv(envp Ptr) int32  { return 0 }

func Fegetmode(modep Ptr) int32 {
	Store32(modep, 0)
	return 0
}

func Fesetmode(modep Ptr) int32 { return 0 }

// feDfl is what FE_DFL_ENV and FE_DFL_MODE point to.
var feDfl = Malloc(8)

func FeDfl() Ptr { return feDfl }
