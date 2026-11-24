// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libgen

import (
	c23 "internal/c23"
	c23_string "internal/c23/string"
	"sync"
)

var dotOnce sync.Once

var dotStr *c23.Void

// dot is the "." basename and dirname return for an empty path.
func dot() *c23.Void {
	dotOnce.Do(func() { dotStr = c23.CString(".") })
	return dotStr
}

// Basename is POSIX's basename (glibc's __xpg_basename): the last element
// of path, whose trailing slashes it removes in place.
func Basename(path *c23.Void) *c23.Void {
	if c23.Addr(path) == 0 || c23.Load8(path) == 0 {
		return dot()
	}
	i := int(c23_string.Strlen(path)) - 1
	for ; i > 0 && c23.Load8(c23.Add(path, i)) == '/'; i-- {
		c23.Store8(c23.Add(path, i), 0)
	}
	for ; i > 0 && c23.Load8(c23.Add(path, i-1)) != '/'; i-- {
	}
	return c23.Add(path, i)
}

// Dirname is dirname: path without its last element, which it removes
// in place, as musl does ("//" is "/", where glibc keeps "//").
func Dirname(path *c23.Void) *c23.Void {
	if c23.Addr(path) == 0 || c23.Load8(path) == 0 {
		return dot()
	}
	at := func(i int) byte { return c23.Load8(c23.Add(path, i)) }
	i := int(c23_string.Strlen(path)) - 1
	for ; at(i) == '/'; i-- {
		if i == 0 {
			c23.Store8(c23.Add(path, 1), 0)
			return path
		}
	}
	for ; at(i) != '/'; i-- {
		if i == 0 {
			return dot()
		}
	}
	for ; at(i) == '/'; i-- {
		if i == 0 {
			c23.Store8(c23.Add(path, 1), 0)
			return path
		}
	}
	c23.Store8(c23.Add(path, i+1), 0)
	return path
}
