// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wctype

import c23 "internal/c23"

// The functions of a locale (POSIX's _l functions): the library's
// locales classify characters alike (internal/c23/locale), so each is
// the function of the global locale.

func IswalnumL(c uint32, _ c23.Ptr) int32         { return Iswalnum(c) }
func IswalphaL(c uint32, _ c23.Ptr) int32         { return Iswalpha(c) }
func IswblankL(c uint32, _ c23.Ptr) int32         { return Iswblank(c) }
func IswcntrlL(c uint32, _ c23.Ptr) int32         { return Iswcntrl(c) }
func IswdigitL(c uint32, _ c23.Ptr) int32         { return Iswdigit(c) }
func IswgraphL(c uint32, _ c23.Ptr) int32         { return Iswgraph(c) }
func IswlowerL(c uint32, _ c23.Ptr) int32         { return Iswlower(c) }
func IswprintL(c uint32, _ c23.Ptr) int32         { return Iswprint(c) }
func IswpunctL(c uint32, _ c23.Ptr) int32         { return Iswpunct(c) }
func IswspaceL(c uint32, _ c23.Ptr) int32         { return Iswspace(c) }
func IswupperL(c uint32, _ c23.Ptr) int32         { return Iswupper(c) }
func IswxdigitL(c uint32, _ c23.Ptr) int32        { return Iswxdigit(c) }
func IswctypeL(c, desc uint32, _ c23.Ptr) int32   { return Iswctype(c, desc) }
func WctypeL(name, _ c23.Ptr) uint32              { return Wctype(name) }
func TowlowerL(c uint32, _ c23.Ptr) uint32        { return Towlower(c) }
func TowupperL(c uint32, _ c23.Ptr) uint32        { return Towupper(c) }
func TowctransL(c, desc uint32, _ c23.Ptr) uint32 { return Towctrans(c, desc) }
func WctransL(name, _ c23.Ptr) uint32             { return Wctrans(name) }
