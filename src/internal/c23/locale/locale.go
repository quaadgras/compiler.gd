// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package locale

import (
	c23 "internal/c23"
	"sync"
	"unsafe"
)

// lconv is C's struct lconv, in glibc's and musl's order of members.
type lconv struct {
	decimalPoint, thousandsSep, grouping, intCurrSymbol, currencySymbol,
	monDecimalPoint, monThousandsSep, monGrouping, positiveSign, negativeSign unsafe.Pointer
	chars [14]int8 // int_frac_digits ... int_n_sign_posn
}

var lconvOnce sync.Once

var lconvC c23.Ptr

var localeC c23.Ptr

// Localeconv returns the C locale's numeric and monetary conventions:
// "." as the decimal point, empty strings and CHAR_MAX otherwise. C must
// not modify them.
func Localeconv() c23.Ptr {
	lconvOnce.Do(func() {
		lconvC = c23.New[lconv]()
		c23.StorePtr(lconvC, c23.CString("."))
		empty := c23.CString("")
		for i := 1; i < 10; i++ {
			c23.StorePtr(c23.Add(lconvC, i*int(c23.WordSize)), empty)
		}
		for i := 0; i < 14; i++ {
			c23.Store8(c23.Add(lconvC, 10*int(c23.WordSize)+i), 127)
		}
		localeC = c23.CString("C")
	})
	return lconvC
}

// Setlocale is setlocale: only the C locale exists, under the names "C",
// "POSIX" and "" (the environment's locale, which is C's), so setlocale
// returns "C" for those and for a query (a null locale), and null for
// any other name or category.
func Setlocale(category int32, locale c23.Ptr) c23.Ptr {
	Localeconv()
	if category < 0 || category > 6 { // LC_CTYPE (0) ... LC_ALL (6)
		return c23.Ptr{}
	}
	if c23.Addr(locale) != 0 {
		switch c23.GoString(locale) {
		case "", "C", "POSIX":
		default:
			return c23.Ptr{}
		}
	}
	return localeC
}
