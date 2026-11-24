// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package libintl is libintl.h's functions for cmd/cc: gettext's, as
// with no message catalogs: a message translates to itself, or to the
// plural form of n by English's rule.
package libintl

import (
	c23 "internal/c23"
	"sync"
)

var (
	mu       sync.Mutex
	domain   *c23.Void                // textdomain's, "messages" until set
	dirs     = map[string]*c23.Void{} // bindtextdomain's
	codesets = map[string]*c23.Void{} // bind_textdomain_codeset's
)

func Gettext(msgid *c23.Void) *c23.Void { return msgid }

func Dgettext(domainname, msgid *c23.Void) *c23.Void { return msgid }

func Dcgettext(domainname, msgid *c23.Void, category int32) *c23.Void { return msgid }

func Ngettext(msgid1, msgid2 *c23.Void, n uint64) *c23.Void {
	if n == 1 {
		return msgid1
	}
	return msgid2
}

func Dngettext(domainname, msgid1, msgid2 *c23.Void, n uint64) *c23.Void {
	return Ngettext(msgid1, msgid2, n)
}

func Dcngettext(domainname, msgid1, msgid2 *c23.Void, n uint64, category int32) *c23.Void {
	return Ngettext(msgid1, msgid2, n)
}

// Textdomain is textdomain: it sets the domain, unless domainname is
// null, and returns it ("messages" is the default, and "" sets it).
func Textdomain(domainname *c23.Void) *c23.Void {
	mu.Lock()
	defer mu.Unlock()
	if domain == nil {
		domain = c23.CString("messages")
	}
	if domainname != nil {
		d := c23.GoString(domainname)
		if d == "" {
			d = "messages"
		}
		domain = c23.CString(d)
	}
	return domain
}

// bind sets (unless value is null) and returns m's value of domain:
// def's if not set.
func bind(m map[string]*c23.Void, domainname, value *c23.Void, def string) *c23.Void {
	if domainname == nil || c23.GoString(domainname) == "" {
		c23.SetErrno(c23.EINVAL)
		return (*c23.Void)(nil)
	}
	d := c23.GoString(domainname)
	mu.Lock()
	defer mu.Unlock()
	if value != nil {
		m[d] = c23.CString(c23.GoString(value))
	}
	if p, ok := m[d]; ok {
		return p
	}
	if def == "" {
		return (*c23.Void)(nil)
	}
	p := c23.CString(def)
	m[d] = p
	return p
}

// Bindtextdomain is bindtextdomain: the directory of the domain's
// catalogs (glibc's default, /usr/share/locale).
func Bindtextdomain(domainname, dirname *c23.Void) *c23.Void {
	return bind(dirs, domainname, dirname, "/usr/share/locale")
}

// BindTextdomainCodeset is bind_textdomain_codeset: null until set.
func BindTextdomainCodeset(domainname, codeset *c23.Void) *c23.Void {
	return bind(codesets, domainname, codeset, "")
}
