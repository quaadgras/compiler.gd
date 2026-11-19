// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package langinfo

import (
	c23 "internal/c23"
	"sync"
	"time"
)

// NlLanginfo is nl_langinfo, of the C locale: its CODESET is "UTF-8",
// which is what the C library's multibyte functions read and write.
func NlLanginfo(item int32) c23.Ptr {
	langinfoMu.Lock()
	defer langinfoMu.Unlock()
	p, ok := langinfoStr[item]
	if !ok {
		p = c23.CString(langinfo(item))
		langinfoStr[item] = p
	}
	return p
}

var langinfoMu sync.Mutex

var langinfoStr = map[int32]c23.Ptr{}

func langinfo(item int32) string {
	switch {
	case item == 14: // CODESET
		return "UTF-8"
	case item == 0x10000: // RADIXCHAR
		return "."
	case item >= 0x20000 && item <= 0x20006: // ABDAY_1 ...
		return time.Weekday(item - 0x20000).String()[:3]
	case item >= 0x20007 && item <= 0x2000D: // DAY_1 ...
		return time.Weekday(item - 0x20007).String()
	case item >= 0x2000E && item <= 0x20019: // ABMON_1 ...
		return time.Month(item - 0x2000E + 1).String()[:3]
	case item >= 0x2001A && item <= 0x20025: // MON_1 ...
		return time.Month(item - 0x2001A + 1).String()
	}
	switch item {
	case 0x20026:
		return "AM"
	case 0x20027:
		return "PM"
	case 0x20028:
		return "%a %b %e %H:%M:%S %Y"
	case 0x20029:
		return "%m/%d/%y"
	case 0x2002A:
		return "%H:%M:%S"
	case 0x2002B:
		return "%I:%M:%S %p"
	case 0x50000:
		return "^[yY]"
	case 0x50001:
		return "^[nN]"
	}
	return ""
}
