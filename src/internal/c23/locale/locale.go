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

var lconvC *c23.Void

// Localeconv returns the C locale's numeric and monetary conventions:
// "." as the decimal point, empty strings and CHAR_MAX otherwise. C must
// not modify them.
func Localeconv() *c23.Void {
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
	})
	return lconvC
}

// Setlocale is setlocale: the locales are "C" (also "POSIX") and
// "C.UTF-8" (also "C.utf8" and the like), which behave alike, and ""
// is the environment's (LC_ALL, the category's variable or LANG), or
// C's if the environment names another. It returns the category's name
// (LC_ALL's is glibc's list of the categories' if they differ), or null
// for another name or category.
func Setlocale(category int32, locale *c23.Void) *c23.Void {
	Localeconv()
	if category < 0 || category > lcAll {
		return (*c23.Void)(nil)
	}
	globalMu.Lock()
	defer globalMu.Unlock()
	if c23.Addr(locale) != 0 && !setGlobal(int(category), c23.GoString(locale)) {
		return (*c23.Void)(nil)
	}
	if category == lcAll {
		return nameOf(allName(globalUTF8))
	}
	return nameOf(catName(globalUTF8, int(category)))
}
