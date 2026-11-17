// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"sync"
	"unsafe"
)

// locale.h: the only locale is C's ("C", or "POSIX").

// lconv is C's struct lconv, in glibc's and musl's order of members.
type lconv struct {
	decimalPoint, thousandsSep, grouping, intCurrSymbol, currencySymbol,
	monDecimalPoint, monThousandsSep, monGrouping, positiveSign, negativeSign unsafe.Pointer
	chars [14]int8 // int_frac_digits ... int_n_sign_posn
}

var (
	lconvOnce sync.Once
	lconvC    Ptr
	localeC   Ptr
)

// Localeconv returns the C locale's numeric and monetary conventions:
// "." as the decimal point, empty strings and CHAR_MAX otherwise. C must
// not modify them.
func Localeconv() Ptr {
	lconvOnce.Do(func() {
		lconvC = New[lconv]()
		StorePtr(lconvC, CString("."))
		empty := CString("")
		for i := 1; i < 10; i++ {
			StorePtr(Add(lconvC, i*8), empty)
		}
		for i := 0; i < 14; i++ {
			Store8(Add(lconvC, 80+i), 127)
		}
		localeC = CString("C")
	})
	return lconvC
}

// Setlocale is setlocale: only the C locale exists, under the names "C",
// "POSIX" and "" (the environment's locale, which is C's), so setlocale
// returns "C" for those and for a query (a null locale), and null for
// any other name or category.
func Setlocale(category int32, locale Ptr) Ptr {
	Localeconv()
	if category < 0 || category > 6 { // LC_CTYPE (0) ... LC_ALL (6)
		return Ptr{}
	}
	if Addr(locale) != 0 {
		switch GoString(locale) {
		case "", "C", "POSIX":
		default:
			return Ptr{}
		}
	}
	return localeC
}
