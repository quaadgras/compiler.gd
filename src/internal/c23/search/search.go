// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package search

import (
	c23 "internal/c23"
	"sync"
)

// hsearch's table: its entries are C's ENTRY {char *key; void *data},
// each an allocation of its own, so that what hsearch returns stays valid
// as the table grows (it grows as musl's does, where glibc's fails when
// full).
var hsearchState struct {
	sync.Mutex
	made bool
	tab  map[string]c23.Ptr
}

// Hcreate is hcreate: it makes the table, unless there is one (0).
func Hcreate(nel uint64) int32 {
	st := &hsearchState
	st.Lock()
	defer st.Unlock()
	if st.made {
		return 0
	}
	st.made = true
	st.tab = make(map[string]c23.Ptr, min(nel, 1<<16))
	return 1
}

// Hdestroy is hdestroy.
func Hdestroy() {
	st := &hsearchState
	st.Lock()
	defer st.Unlock()
	st.made, st.tab = false, nil
}

// Hsearch is hsearch: the entry of item's key, or null if there is none
// and action is FIND (errno ESRCH); with ENTER, an entry of item if there
// is none.
func Hsearch(item c23.Ptr, action uint32) c23.Ptr {
	st := &hsearchState
	st.Lock()
	defer st.Unlock()
	key := c23.LoadPtr(item)
	k := c23.GoString(key)
	if e, ok := st.tab[k]; ok {
		return e
	}
	if action == 0 || !st.made {
		c23.SetErrno(c23.ESRCH)
		return c23.Ptr{}
	}
	e := c23.Malloc(uint64(2 * c23.WordSize))
	c23.StorePtr(e, key)
	c23.StorePtr(c23.Add(e, int(c23.WordSize)), c23.LoadPtr(c23.Add(item, int(c23.WordSize))))
	st.tab[k] = e
	return e
}

func tkey(n c23.Ptr) c23.Ptr { return c23.LoadPtr(n) }

func tleft(n c23.Ptr) c23.Ptr { return c23.LoadPtr(c23.Add(n, int(c23.WordSize))) }

func tright(n c23.Ptr) c23.Ptr { return c23.LoadPtr(c23.Add(n, 2*int(c23.WordSize))) }

func tsetLeft(n, c c23.Ptr) { c23.StorePtr(c23.Add(n, int(c23.WordSize)), c) }

func tsetRight(n, c c23.Ptr) { c23.StorePtr(c23.Add(n, 2*int(c23.WordSize)), c) }

func tsetHeight(n c23.Ptr, h int) { c23.StoreWord(c23.Add(n, 3*int(c23.WordSize)), uintptr(h)) }

func theight(n c23.Ptr) int {
	if c23.Addr(n) == 0 {
		return 0
	}
	return int(c23.LoadWord(c23.Add(n, 3*int(c23.WordSize))))
}

func tfix(n c23.Ptr) { tsetHeight(n, 1+max(theight(tleft(n)), theight(tright(n)))) }

func trotRight(n c23.Ptr) c23.Ptr {
	l := tleft(n)
	tsetLeft(n, tright(l))
	tsetRight(l, n)
	tfix(n)
	tfix(l)
	return l
}

func trotLeft(n c23.Ptr) c23.Ptr {
	r := tright(n)
	tsetRight(n, tleft(r))
	tsetLeft(r, n)
	tfix(n)
	tfix(r)
	return r
}

// tbalance restores the AVL property at n and returns the subtree's root.
func tbalance(n c23.Ptr) c23.Ptr {
	tfix(n)
	switch b := theight(tleft(n)) - theight(tright(n)); {
	case b > 1:
		if l := tleft(n); theight(tleft(l)) < theight(tright(l)) {
			tsetLeft(n, trotLeft(l))
		}
		return trotRight(n)
	case b < -1:
		if r := tright(n); theight(tright(r)) < theight(tleft(r)) {
			tsetRight(n, trotRight(r))
		}
		return trotLeft(n)
	}
	return n
}

// tinsert inserts key in the subtree n, unless it is there: it returns
// the subtree's new root and key's node.
func tinsert(n, key c23.Ptr, cmp func(c23.Ptr, c23.Ptr) int32) (c23.Ptr, c23.Ptr) {
	if c23.Addr(n) == 0 {
		nn := c23.Malloc(uint64(4 * c23.WordSize))
		c23.StorePtr(nn, key)
		tsetHeight(nn, 1)
		return nn, nn
	}
	var found c23.Ptr
	switch c := cmp(key, tkey(n)); {
	case c == 0:
		return n, n
	case c < 0:
		var l c23.Ptr
		l, found = tinsert(tleft(n), key, cmp)
		tsetLeft(n, l)
	default:
		var r c23.Ptr
		r, found = tinsert(tright(n), key, cmp)
		tsetRight(n, r)
	}
	return tbalance(n), found
}

// Tsearch is tsearch: the node of key in the tree *rootp, which it adds
// if there is none.
func Tsearch(key, rootp, compar c23.Ptr) c23.Ptr {
	if c23.Addr(rootp) == 0 {
		return c23.Ptr{}
	}
	root, n := tinsert(c23.LoadPtr(rootp), key, c23.CmpFunc(compar))
	c23.StorePtr(rootp, root)
	return n
}

// Tfind is tfind: the node of key in the tree *rootp, or null.
func Tfind(key, rootp, compar c23.Ptr) c23.Ptr {
	if c23.Addr(rootp) == 0 {
		return c23.Ptr{}
	}
	cmp := c23.CmpFunc(compar)
	n := c23.LoadPtr(rootp)
	for c23.Addr(n) != 0 {
		switch c := cmp(key, tkey(n)); {
		case c == 0:
			return n
		case c < 0:
			n = tleft(n)
		default:
			n = tright(n)
		}
	}
	return c23.Ptr{}
}

// tremoveMin removes the smallest node of the subtree n: it returns the
// subtree's new root and that node.
func tremoveMin(n c23.Ptr) (c23.Ptr, c23.Ptr) {
	if c23.Addr(tleft(n)) == 0 {
		return tright(n), n
	}
	l, m := tremoveMin(tleft(n))
	tsetLeft(n, l)
	return tbalance(n), m
}

// tremove removes key's node from the subtree n: it returns the
// subtree's new root, the parent of the removed node (null for n
// itself) and whether it found it.
func tremove(n, key c23.Ptr, cmp func(c23.Ptr, c23.Ptr) int32) (c23.Ptr, c23.Ptr, bool) {
	if c23.Addr(n) == 0 {
		return n, c23.Ptr{}, false
	}
	c := cmp(key, tkey(n))
	if c == 0 {
		l, r := tleft(n), tright(n)
		if c23.Addr(r) == 0 {
			return l, c23.Ptr{}, true
		}
		r, m := tremoveMin(r)
		tsetLeft(m, l)
		tsetRight(m, r)
		return tbalance(m), c23.Ptr{}, true
	}
	var child, parent c23.Ptr
	var ok bool
	if c < 0 {
		child, parent, ok = tremove(tleft(n), key, cmp)
		tsetLeft(n, child)
	} else {
		child, parent, ok = tremove(tright(n), key, cmp)
		tsetRight(n, child)
	}
	if ok && c23.Addr(parent) == 0 {
		parent = n
	}
	return tbalance(n), parent, ok
}

// Tdelete is tdelete: it removes key's node from the tree *rootp and
// returns its parent, or, for the root, a pointer that is not null (the
// new root, or rootp if the tree is now empty); null if key is not there.
func Tdelete(key, rootp, compar c23.Ptr) c23.Ptr {
	if c23.Addr(rootp) == 0 {
		return c23.Ptr{}
	}
	root, parent, ok := tremove(c23.LoadPtr(rootp), key, c23.CmpFunc(compar))
	if !ok {
		return c23.Ptr{}
	}
	c23.StorePtr(rootp, root)
	if c23.Addr(parent) == 0 {
		if c23.Addr(root) == 0 {
			return rootp
		}
		return root
	}
	return parent
}

// Twalk is twalk: it calls action for each node, with preorder (0),
// postorder (1) and endorder (2) for an inner node and leaf (3) for a
// leaf, and the node's depth.
func Twalk(root, action c23.Ptr) {
	if c23.Addr(root) == 0 || c23.Addr(action) == 0 {
		return
	}
	f := c23.FuncOf[func(c23.Ptr, uint32, int32)](action)
	var walk func(n c23.Ptr, depth int32)
	walk = func(n c23.Ptr, depth int32) {
		l, r := tleft(n), tright(n)
		if c23.Addr(l) == 0 && c23.Addr(r) == 0 {
			f(n, 3, depth)
			return
		}
		f(n, 0, depth)
		if c23.Addr(l) != 0 {
			walk(l, depth+1)
		}
		f(n, 1, depth)
		if c23.Addr(r) != 0 {
			walk(r, depth+1)
		}
		f(n, 2, depth)
	}
	walk(root, 0)
}

// Tdestroy is GNU's tdestroy: it calls freeNode with each node's key.
func Tdestroy(root, freeNode c23.Ptr) {
	if c23.Addr(root) == 0 {
		return
	}
	f := c23.FuncOf[func(c23.Ptr)](freeNode)
	var walk func(n c23.Ptr)
	walk = func(n c23.Ptr) {
		if l := tleft(n); c23.Addr(l) != 0 {
			walk(l)
		}
		if r := tright(n); c23.Addr(r) != 0 {
			walk(r)
		}
		f(tkey(n))
	}
	walk(root)
}

// Lfind is lfind: the first of the *nelp elements at base that compar
// finds equal to key, or null.
func Lfind(key, base, nelp c23.Ptr, width uint64, compar c23.Ptr) c23.Ptr {
	cmp := c23.CmpFunc(compar)
	n := c23.LoadLong(nelp)
	for i := int64(0); i < n; i++ {
		e := c23.Add(base, int(i)*int(width))
		if cmp(key, e) == 0 {
			return e
		}
	}
	return c23.Ptr{}
}

// Lsearch is lsearch: lfind, which adds key at the end if it is not
// there, and counts it in *nelp.
func Lsearch(key, base, nelp c23.Ptr, width uint64, compar c23.Ptr) c23.Ptr {
	if e := Lfind(key, base, nelp, width, compar); c23.Addr(e) != 0 {
		return e
	}
	n := c23.LoadLong(nelp)
	e := c23.Add(base, int(n)*int(width))
	c23.Memmove(e, key, uintptr(width))
	c23.StoreLong(nelp, n+1)
	return e
}

// Insque is insque: it puts element, a struct qelem {forw, back, ...},
// after pred in its list, or makes a list of it alone if pred is null.
func Insque(element, pred c23.Ptr) {
	if c23.Addr(pred) == 0 {
		c23.StorePtr(element, c23.Ptr{})
		c23.StorePtr(c23.Add(element, int(c23.WordSize)), c23.Ptr{})
		return
	}
	next := c23.LoadPtr(pred)
	c23.StorePtr(element, next)
	c23.StorePtr(c23.Add(element, int(c23.WordSize)), pred)
	c23.StorePtr(pred, element)
	if c23.Addr(next) != 0 {
		c23.StorePtr(c23.Add(next, int(c23.WordSize)), element)
	}
}

// Remque is remque: it removes element from its list.
func Remque(element c23.Ptr) {
	next, prev := c23.LoadPtr(element), c23.LoadPtr(c23.Add(element, int(c23.WordSize)))
	if c23.Addr(next) != 0 {
		c23.StorePtr(c23.Add(next, int(c23.WordSize)), prev)
	}
	if c23.Addr(prev) != 0 {
		c23.StorePtr(prev, next)
	}
}
