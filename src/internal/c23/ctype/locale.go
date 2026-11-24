// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ctype

import c23 "internal/c23"

// The functions of a locale (POSIX's _l functions): the library's
// locales classify characters alike (internal/c23/locale), so each is
// the function of the global locale.

func IsalnumL(c int32, _ *c23.Void) int32  { return Isalnum(c) }
func IsalphaL(c int32, _ *c23.Void) int32  { return Isalpha(c) }
func IsblankL(c int32, _ *c23.Void) int32  { return Isblank(c) }
func IscntrlL(c int32, _ *c23.Void) int32  { return Iscntrl(c) }
func IsdigitL(c int32, _ *c23.Void) int32  { return Isdigit(c) }
func IsgraphL(c int32, _ *c23.Void) int32  { return Isgraph(c) }
func IslowerL(c int32, _ *c23.Void) int32  { return Islower(c) }
func IsprintL(c int32, _ *c23.Void) int32  { return Isprint(c) }
func IspunctL(c int32, _ *c23.Void) int32  { return Ispunct(c) }
func IsspaceL(c int32, _ *c23.Void) int32  { return Isspace(c) }
func IsupperL(c int32, _ *c23.Void) int32  { return Isupper(c) }
func IsxdigitL(c int32, _ *c23.Void) int32 { return Isxdigit(c) }
func TolowerL(c int32, _ *c23.Void) int32  { return Tolower(c) }
func ToupperL(c int32, _ *c23.Void) int32  { return Toupper(c) }

// Isascii is isascii: whether c is a 7-bit character.
func Isascii(c int32) int32 { return c23.B2I(uint32(c) < 0x80) }

// Toascii is toascii: c's low 7 bits.
func Toascii(c int32) int32 { return c & 0x7f }
