// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package locale

import (
	c23 "internal/c23"
	"os"
	"strings"
	"sync"
)

// POSIX's locale objects. The library has two locales, "C" (or "POSIX")
// and "C.UTF-8", which behave alike: the C locale's multibyte characters
// are already UTF-8's, and its classes and cases those of Unicode. A
// locale_t is an object of 4 bytes that says which of the six categories
// are C.UTF-8's (bit LC_x); each thread uses a locale_t, or the global
// locale (setlocale's), LC_GLOBAL_LOCALE, which is the pointer (locale_t)-1.

const (
	lcAll     = 6
	nCategory = 6

	// lcAllMask is glibc's LC_ALL_MASK: LC_CTYPE_MASK ... LC_IDENTIFICATION_MASK
	// but LC_ALL's bit; the categories glibc has and cmd/cc's locale.h
	// does not are accepted and ignored.
	lcAllMask = 0x1fbf
)

var (
	globalMu   sync.Mutex
	globalUTF8 uint32 // the categories of the global locale that are C.UTF-8

	names = map[string]*c23.Void{} // setlocale's results, which C must not change

	// current is the calling thread's locale_t, null for the global locale.
	current = c23.NewTLS(func() *c23.Void { return c23.Malloc(uint64(c23.WordSize)) })
)

// localeName reports whether name is C.UTF-8's, and ok whether it is a
// locale the library has.
func localeName(name string) (utf8, ok bool) {
	switch name {
	case "C", "POSIX":
		return false, true
	}
	base, codeset, found := strings.Cut(name, ".")
	if found && (base == "C" || base == "POSIX") {
		switch strings.ToLower(strings.ReplaceAll(codeset, "-", "")) {
		case "utf8":
			return true, true
		}
	}
	return false, false
}

var categoryNames = [nCategory]string{"LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE", "LC_MONETARY", "LC_MESSAGES"}

// envName is the name "" means for category: LC_ALL's, the category's
// variable or LANG, if set and not empty; a name the library does not
// have is C's.
func envName(category int) bool {
	for _, v := range []string{"LC_ALL", categoryNames[category], "LANG"} {
		if s := os.Getenv(v); s != "" {
			utf8, ok := localeName(s)
			return ok && utf8
		}
	}
	return false
}

// resolve returns which categories of mask name gives, as utf8, and
// whether it names a locale. "" is the environment's (envName).
func resolve(mask uint32, name string) (utf8 uint32, ok bool) {
	if name == "" {
		for c := range nCategory {
			if mask&(1<<c) != 0 && envName(c) {
				utf8 |= 1 << c
			}
		}
		return utf8, true
	}
	u, ok := localeName(name)
	if !ok {
		return 0, false
	}
	if u {
		utf8 = mask & (1<<nCategory - 1)
	}
	return utf8, true
}

// isGlobal reports whether p is LC_GLOBAL_LOCALE: the integer -1 as a
// pointer, whatever else the conversion made of its bits. No object has
// such an address.
func isGlobal(p *c23.Void) bool { return uint32(c23.Addr(p)) == 0xffffffff }

func globalPtr() *c23.Void { return c23.FromInt(^uintptr(0)) }

func newObj(utf8 uint32) *c23.Void {
	p := c23.Malloc(4)
	c23.Store32(p, utf8)
	return p
}

// Newlocale is newlocale: a locale whose categories in mask are name's
// and the others base's (C's, if base is null). It modifies base and
// returns it, if base is not null. A name other than "", "C", "POSIX"
// and C.UTF-8's (C.utf8 and the like) fails with ENOENT, as a locale
// glibc has not installed does.
func Newlocale(mask int32, name, base *c23.Void) *c23.Void {
	if c23.Addr(name) == 0 || uint32(mask)&^lcAllMask != 0 || isGlobal(base) {
		c23.SetErrno(c23.EINVAL)
		return (*c23.Void)(nil)
	}
	m := uint32(mask) & (1<<nCategory - 1)
	utf8, ok := resolve(m, c23.GoString(name))
	if !ok {
		c23.SetErrno(c23.ENOENT)
		return (*c23.Void)(nil)
	}
	if c23.Addr(base) == 0 {
		return newObj(utf8)
	}
	c23.Store32(base, c23.Load32(base)&^m|utf8)
	return base
}

// Duplocale is duplocale: a copy of loc, or of the global locale if loc
// is LC_GLOBAL_LOCALE.
func Duplocale(loc *c23.Void) *c23.Void {
	if isGlobal(loc) {
		globalMu.Lock()
		defer globalMu.Unlock()
		return newObj(globalUTF8)
	}
	return newObj(c23.Load32(loc))
}

// Freelocale is freelocale.
func Freelocale(loc *c23.Void) {
	if c23.Addr(loc) != 0 && !isGlobal(loc) {
		c23.Free(loc)
	}
}

// Uselocale is uselocale: it makes loc the calling thread's locale (the
// global one, if it is LC_GLOBAL_LOCALE), unless loc is null, and
// returns the one the thread used, LC_GLOBAL_LOCALE if the global.
func Uselocale(loc *c23.Void) *c23.Void {
	cur := current.Get()
	old := c23.LoadPtr(cur)
	switch {
	case isGlobal(loc):
		c23.StorePtr(cur, (*c23.Void)(nil))
	case c23.Addr(loc) != 0:
		c23.StorePtr(cur, loc)
	}
	if c23.Addr(old) == 0 {
		return globalPtr()
	}
	return old
}

// nameOf returns the C string of a locale's name, which it keeps.
func nameOf(s string) *c23.Void {
	p, ok := names[s]
	if !ok {
		p = c23.CString(s)
		names[s] = p
	}
	return p
}

func catName(utf8 uint32, c int) string {
	if utf8&(1<<c) != 0 {
		return "C.UTF-8"
	}
	return "C"
}

// allName is LC_ALL's name: the categories', if they are the same, or
// glibc's list of them.
func allName(utf8 uint32) string {
	switch utf8 {
	case 0:
		return "C"
	case 1<<nCategory - 1:
		return "C.UTF-8"
	}
	var b strings.Builder
	for c := range nCategory {
		if c > 0 {
			b.WriteByte(';')
		}
		b.WriteString(categoryNames[c] + "=" + catName(utf8, c))
	}
	return b.String()
}

// setGlobal sets the global locale's category (or all of them) to name,
// a name or LC_ALL's list of names, reporting whether it could.
func setGlobal(category int, name string) bool {
	if category == lcAll && strings.Contains(name, "=") {
		var utf8 uint32
		seen := 0
		for _, kv := range strings.Split(name, ";") {
			k, v, _ := strings.Cut(kv, "=")
			c := -1
			for i, n := range categoryNames {
				if n == k {
					c = i
				}
			}
			u, ok := localeName(v)
			if c < 0 || !ok {
				return false
			}
			seen |= 1 << c
			if u {
				utf8 |= 1 << c
			}
		}
		if seen != 1<<nCategory-1 {
			return false
		}
		globalUTF8 = utf8
		return true
	}
	mask := uint32(1<<nCategory - 1)
	if category != lcAll {
		mask = 1 << category
	}
	utf8, ok := resolve(mask, name)
	if !ok {
		return false
	}
	globalUTF8 = globalUTF8&^mask | utf8
	return true
}
