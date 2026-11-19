// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"sync"
)

// search.h: hsearch's table, tsearch's trees, lfind and lsearch, and
// insque and remque.

// hsearch's table: its entries are C's ENTRY {char *key; void *data},
// each an allocation of its own, so that what hsearch returns stays valid
// as the table grows (it grows as musl's does, where glibc's fails when
// full).
var hsearchState struct {
	sync.Mutex
	made bool
	tab  map[string]Ptr
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
	st.tab = make(map[string]Ptr, min(nel, 1<<16))
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
func Hsearch(item Ptr, action uint32) Ptr {
	st := &hsearchState
	st.Lock()
	defer st.Unlock()
	key := LoadPtr(item)
	k := GoString(key)
	if e, ok := st.tab[k]; ok {
		return e
	}
	if action == 0 || !st.made {
		SetErrno(ESRCH)
		return Ptr{}
	}
	e := Malloc(uint64(2 * wordSize))
	StorePtr(e, key)
	StorePtr(Add(e, int(wordSize)), LoadPtr(Add(item, int(wordSize))))
	st.tab[k] = e
	return e
}

// tsearch's nodes: {const void *key; node *left, *right; height}, an
// AVL tree as musl's. A node's address is what tsearch returns: C reads
// the key through it.

func tkey(n Ptr) Ptr          { return LoadPtr(n) }
func tleft(n Ptr) Ptr         { return LoadPtr(Add(n, int(wordSize))) }
func tright(n Ptr) Ptr        { return LoadPtr(Add(n, 2*int(wordSize))) }
func tsetLeft(n, c Ptr)       { StorePtr(Add(n, int(wordSize)), c) }
func tsetRight(n, c Ptr)      { StorePtr(Add(n, 2*int(wordSize)), c) }
func tsetHeight(n Ptr, h int) { storeWord(Add(n, 3*int(wordSize)), uintptr(h)) }

func theight(n Ptr) int {
	if Addr(n) == 0 {
		return 0
	}
	return int(loadWord(Add(n, 3*int(wordSize))))
}

func tfix(n Ptr) { tsetHeight(n, 1+max(theight(tleft(n)), theight(tright(n)))) }

func trotRight(n Ptr) Ptr {
	l := tleft(n)
	tsetLeft(n, tright(l))
	tsetRight(l, n)
	tfix(n)
	tfix(l)
	return l
}

func trotLeft(n Ptr) Ptr {
	r := tright(n)
	tsetRight(n, tleft(r))
	tsetLeft(r, n)
	tfix(n)
	tfix(r)
	return r
}

// tbalance restores the AVL property at n and returns the subtree's root.
func tbalance(n Ptr) Ptr {
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
func tinsert(n, key Ptr, cmp func(Ptr, Ptr) int32) (Ptr, Ptr) {
	if Addr(n) == 0 {
		nn := Malloc(uint64(4 * wordSize))
		StorePtr(nn, key)
		tsetHeight(nn, 1)
		return nn, nn
	}
	var found Ptr
	switch c := cmp(key, tkey(n)); {
	case c == 0:
		return n, n
	case c < 0:
		var l Ptr
		l, found = tinsert(tleft(n), key, cmp)
		tsetLeft(n, l)
	default:
		var r Ptr
		r, found = tinsert(tright(n), key, cmp)
		tsetRight(n, r)
	}
	return tbalance(n), found
}

// Tsearch is tsearch: the node of key in the tree *rootp, which it adds
// if there is none.
func Tsearch(key, rootp, compar Ptr) Ptr {
	if Addr(rootp) == 0 {
		return Ptr{}
	}
	root, n := tinsert(LoadPtr(rootp), key, cmpFunc(compar))
	StorePtr(rootp, root)
	return n
}

// Tfind is tfind: the node of key in the tree *rootp, or null.
func Tfind(key, rootp, compar Ptr) Ptr {
	if Addr(rootp) == 0 {
		return Ptr{}
	}
	cmp := cmpFunc(compar)
	n := LoadPtr(rootp)
	for Addr(n) != 0 {
		switch c := cmp(key, tkey(n)); {
		case c == 0:
			return n
		case c < 0:
			n = tleft(n)
		default:
			n = tright(n)
		}
	}
	return Ptr{}
}

// tremoveMin removes the smallest node of the subtree n: it returns the
// subtree's new root and that node.
func tremoveMin(n Ptr) (Ptr, Ptr) {
	if Addr(tleft(n)) == 0 {
		return tright(n), n
	}
	l, m := tremoveMin(tleft(n))
	tsetLeft(n, l)
	return tbalance(n), m
}

// tremove removes key's node from the subtree n: it returns the
// subtree's new root, the parent of the removed node (null for n
// itself) and whether it found it.
func tremove(n, key Ptr, cmp func(Ptr, Ptr) int32) (Ptr, Ptr, bool) {
	if Addr(n) == 0 {
		return n, Ptr{}, false
	}
	c := cmp(key, tkey(n))
	if c == 0 {
		l, r := tleft(n), tright(n)
		if Addr(r) == 0 {
			return l, Ptr{}, true
		}
		r, m := tremoveMin(r)
		tsetLeft(m, l)
		tsetRight(m, r)
		return tbalance(m), Ptr{}, true
	}
	var child, parent Ptr
	var ok bool
	if c < 0 {
		child, parent, ok = tremove(tleft(n), key, cmp)
		tsetLeft(n, child)
	} else {
		child, parent, ok = tremove(tright(n), key, cmp)
		tsetRight(n, child)
	}
	if ok && Addr(parent) == 0 {
		parent = n
	}
	return tbalance(n), parent, ok
}

// Tdelete is tdelete: it removes key's node from the tree *rootp and
// returns its parent, or, for the root, a pointer that is not null (the
// new root, or rootp if the tree is now empty); null if key is not there.
func Tdelete(key, rootp, compar Ptr) Ptr {
	if Addr(rootp) == 0 {
		return Ptr{}
	}
	root, parent, ok := tremove(LoadPtr(rootp), key, cmpFunc(compar))
	if !ok {
		return Ptr{}
	}
	StorePtr(rootp, root)
	if Addr(parent) == 0 {
		if Addr(root) == 0 {
			return rootp
		}
		return root
	}
	return parent
}

// Twalk is twalk: it calls action for each node, with preorder (0),
// postorder (1) and endorder (2) for an inner node and leaf (3) for a
// leaf, and the node's depth.
func Twalk(root, action Ptr) {
	if Addr(root) == 0 || Addr(action) == 0 {
		return
	}
	f := FuncOf[func(Ptr, uint32, int32)](action)
	var walk func(n Ptr, depth int32)
	walk = func(n Ptr, depth int32) {
		l, r := tleft(n), tright(n)
		if Addr(l) == 0 && Addr(r) == 0 {
			f(n, 3, depth)
			return
		}
		f(n, 0, depth)
		if Addr(l) != 0 {
			walk(l, depth+1)
		}
		f(n, 1, depth)
		if Addr(r) != 0 {
			walk(r, depth+1)
		}
		f(n, 2, depth)
	}
	walk(root, 0)
}

// Tdestroy is GNU's tdestroy: it calls freeNode with each node's key.
func Tdestroy(root, freeNode Ptr) {
	if Addr(root) == 0 {
		return
	}
	f := FuncOf[func(Ptr)](freeNode)
	var walk func(n Ptr)
	walk = func(n Ptr) {
		if l := tleft(n); Addr(l) != 0 {
			walk(l)
		}
		if r := tright(n); Addr(r) != 0 {
			walk(r)
		}
		f(tkey(n))
	}
	walk(root)
}

// Lfind is lfind: the first of the *nelp elements at base that compar
// finds equal to key, or null.
func Lfind(key, base, nelp Ptr, width uint64, compar Ptr) Ptr {
	cmp := cmpFunc(compar)
	n := loadLong(nelp)
	for i := int64(0); i < n; i++ {
		e := Add(base, int(i)*int(width))
		if cmp(key, e) == 0 {
			return e
		}
	}
	return Ptr{}
}

// Lsearch is lsearch: lfind, which adds key at the end if it is not
// there, and counts it in *nelp.
func Lsearch(key, base, nelp Ptr, width uint64, compar Ptr) Ptr {
	if e := Lfind(key, base, nelp, width, compar); Addr(e) != 0 {
		return e
	}
	n := loadLong(nelp)
	e := Add(base, int(n)*int(width))
	Memmove(e, key, uintptr(width))
	storeLong(nelp, n+1)
	return e
}

// Insque is insque: it puts element, a struct qelem {forw, back, ...},
// after pred in its list, or makes a list of it alone if pred is null.
func Insque(element, pred Ptr) {
	if Addr(pred) == 0 {
		StorePtr(element, Ptr{})
		StorePtr(Add(element, int(wordSize)), Ptr{})
		return
	}
	next := LoadPtr(pred)
	StorePtr(element, next)
	StorePtr(Add(element, int(wordSize)), pred)
	StorePtr(pred, element)
	if Addr(next) != 0 {
		StorePtr(Add(next, int(wordSize)), element)
	}
}

// Remque is remque: it removes element from its list.
func Remque(element Ptr) {
	next, prev := LoadPtr(element), LoadPtr(Add(element, int(wordSize)))
	if Addr(next) != 0 {
		StorePtr(Add(next, int(wordSize)), prev)
	}
	if Addr(prev) != 0 {
		StorePtr(prev, next)
	}
}
