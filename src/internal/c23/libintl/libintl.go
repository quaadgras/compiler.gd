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

type Ptr = c23.Ptr

var (
	mu       sync.Mutex
	domain   Ptr                // textdomain's, "messages" until set
	dirs     = map[string]Ptr{} // bindtextdomain's
	codesets = map[string]Ptr{} // bind_textdomain_codeset's
)

func Gettext(msgid Ptr) Ptr { return msgid }

func Dgettext(domainname, msgid Ptr) Ptr { return msgid }

func Dcgettext(domainname, msgid Ptr, category int32) Ptr { return msgid }

func Ngettext(msgid1, msgid2 Ptr, n uint64) Ptr {
	if n == 1 {
		return msgid1
	}
	return msgid2
}

func Dngettext(domainname, msgid1, msgid2 Ptr, n uint64) Ptr {
	return Ngettext(msgid1, msgid2, n)
}

func Dcngettext(domainname, msgid1, msgid2 Ptr, n uint64, category int32) Ptr {
	return Ngettext(msgid1, msgid2, n)
}

// Textdomain is textdomain: it sets the domain, unless domainname is
// null, and returns it ("messages" is the default, and "" sets it).
func Textdomain(domainname Ptr) Ptr {
	mu.Lock()
	defer mu.Unlock()
	if domain.Obj == nil {
		domain = c23.CString("messages")
	}
	if domainname.Obj != nil {
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
func bind(m map[string]Ptr, domainname, value Ptr, def string) Ptr {
	if domainname.Obj == nil || c23.GoString(domainname) == "" {
		c23.SetErrno(c23.EINVAL)
		return Ptr{}
	}
	d := c23.GoString(domainname)
	mu.Lock()
	defer mu.Unlock()
	if value.Obj != nil {
		m[d] = c23.CString(c23.GoString(value))
	}
	if p, ok := m[d]; ok {
		return p
	}
	if def == "" {
		return Ptr{}
	}
	p := c23.CString(def)
	m[d] = p
	return p
}

// Bindtextdomain is bindtextdomain: the directory of the domain's
// catalogs (glibc's default, /usr/share/locale).
func Bindtextdomain(domainname, dirname Ptr) Ptr {
	return bind(dirs, domainname, dirname, "/usr/share/locale")
}

// BindTextdomainCodeset is bind_textdomain_codeset: null until set.
func BindTextdomainCodeset(domainname, codeset Ptr) Ptr {
	return bind(codesets, domainname, codeset, "")
}
