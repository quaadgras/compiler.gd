// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"encoding/binary"
	"strconv"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
	"cmd/internal/objabi"
	"cmd/internal/sys"
)

// What the functions that are only known when the program runs do.
//
// An argument of a call of a function value, or of a method of an
// interface, is shared if the function that is called shares it, and
// the object was allocated before anyone knew which function that is
// (doc/gd/sharing.md). But the linker knows all of them: every method
// with that name and signature of a type that the program has, and
// every function with that signature that the program makes a value
// of. For each such class the program has a word, which is an escape
// mask (escmask.go): the masks of all the functions of the class, ORed.
// What the linker writes is what compiled code asks, where it allocates:
// bit k+1 is set if a function of the class may share its argument k,
// and bit ShareWordStores+k if one may store in an object that the
// argument reaches (escape's groups.go).
//
// The compiler writes the word with all bits set, so that a program
// that is linked by something that does not know is safe, and a record
// for every function that may be called so: its class, its mask, and
// the links of its parameters, which are the classes that it passes
// them on to. The linker follows those until nothing changes.

const (
	// ShareWordPrefix is the prefix of the name of a class's word,
	// before the class's key.
	ShareWordPrefix = "go:sharemask."
	// ShareRecPrefix is the prefix of the name of a function's record,
	// before the kind of class (M or F), a dot and the function's name.
	ShareRecPrefix = "go:sharerec."
)

// A ShareLink says that a parameter, dereferenced Derefs-1 times, is
// argument Arg of a function of the class Key.
type ShareLink struct {
	Key    string
	Arg    uint8
	Derefs uint8
}

// ShareWordStores is the first of the bits of a class's word that say
// in which arguments' structures a function of the class may store.
const ShareWordStores = 17

// MaxShareLinks is the number of links of a parameter over which it
// counts as shared.
const MaxShareLinks = 16

// A key is the kind of class (M for the methods of interfaces, R for
// their receivers, F for function values), a dot, the method's name for M, and the types of
// the parameters and of the results, each list in parentheses and each
// type as its length, a colon and its link string. A type that has a
// shape is written "?": such a key is a pattern, which a function with
// type parameters has, and stands for every key that it matches.

func shareSig(sb *strings.Builder, fields []*types.Field) (wild, ok bool) {
	sb.WriteByte('(')
	for _, f := range fields {
		if f.Type == nil {
			return false, false
		}
		str := f.Type.LinkString()
		if f.Type.HasShape() {
			str, wild = "?", true
		} else if f.IsDDD() {
			str = "..." + str
		}
		sb.WriteString(strconv.Itoa(len(str)))
		sb.WriteByte(':')
		sb.WriteString(str)
	}
	sb.WriteByte(')')
	return wild, true
}

// ShareKeyMethod returns the key of the class of the methods with the
// given name and the parameters and results of t, or "" if there is
// none, and whether it is a pattern. (The methods of all packages with
// a name that is not exported are one class.)
func ShareKeyMethod(name string, t *types.Type) (key string, wild bool) {
	if t == nil || t.Kind() != types.TFUNC || name == "" || strings.ContainsAny(name, "(.") {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString("M.")
	sb.WriteString(name)
	w1, ok1 := shareSig(&sb, t.Params())
	w2, ok2 := shareSig(&sb, t.Results())
	if !ok1 || !ok2 {
		return "", false
	}
	return sb.String(), w1 || w2
}

// ShareKeyRecv returns the key of the class of the receivers of the
// methods with the given name and the parameters and results of t: its
// word is a mask of one argument, the receiver, and says whether a
// method of the class lets another goroutine reach its receiver, or
// stores it through another parameter.
func ShareKeyRecv(name string, t *types.Type) (key string, wild bool) {
	key, wild = ShareKeyMethod(name, t)
	if key == "" {
		return "", false
	}
	return "R" + key[1:], wild
}

// ShareRecvRecord returns the record of a method for the class key of
// its receiver recv, of which ps says where it goes, or nil if nobody
// knows.
func ShareRecvRecord(key string, recv *types.Field, ps *ParamShare, stores bool) []byte {
	var mask uint64
	var links []ShareLink
	switch {
	case !recv.Type.HasPointers():
	case ps == nil || ps.Share != 0 || ps.Via&^1 != 0:
		mask = 1<<1 | 1<<EscMaskShared | 1<<EscMaskViaRecv | 1<<EscMaskHeld
	default:
		links = ps.Links
	}
	var b []byte
	b = binary.LittleEndian.AppendUint64(b, mask)
	b = binary.AppendUvarint(b, uint64(len(key)))
	b = append(b, key...)
	b = binary.AppendUvarint(b, uint64(len(links)))
	for _, l := range links {
		b = append(b, 0, l.Arg)
		b = binary.AppendUvarint(b, uint64(len(l.Key)))
		b = append(b, l.Key...)
	}
	var st uint16
	if stores {
		st = 1
	}
	return appendStores(b, st, 1, func(int) []ShareLink {
		if ps != nil {
			return append(ps.Links[:len(ps.Links):len(ps.Links)], ps.StoreLinks...)
		}
		return nil
	})
}

// ShareKeyFunc returns the key of the class of the function values
// with the parameters, after the receiver if there is one, and the
// results of t, or "", and whether it is a pattern.
func ShareKeyFunc(t *types.Type) (key string, wild bool) {
	if t == nil || t.Kind() != types.TFUNC {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString("F.")
	w1, ok1 := shareSig(&sb, t.RecvParams())
	w2, ok2 := shareSig(&sb, t.Results())
	if !ok1 || !ok2 {
		return "", false
	}
	return sb.String(), w1 || w2
}

// ShareRecord returns the record of a function of the class key, with
// the given parameters and what they say (shares may be nil, or
// shorter): the mask, the key, and for each parameter that has links
// and is not shared without them, the links.
func ShareRecord(key string, params []*types.Field, shares []ParamShare, stores uint16) []byte {
	var b []byte
	b = binary.LittleEndian.AppendUint64(b, EscMaskBaseOf(params))
	b = binary.AppendUvarint(b, uint64(len(key)))
	b = append(b, key...)
	var deps []byte
	n := 0
	for i, f := range params {
		if i >= EscMaskArgs || i >= len(shares) {
			break
		}
		if escNoteShare(f.Note)&(EscNoteLinked|1) != EscNoteLinked {
			continue
		}
		for _, l := range shares[i].Links {
			deps = append(deps, byte(i), l.Arg)
			deps = binary.AppendUvarint(deps, uint64(len(l.Key)))
			deps = append(deps, l.Key...)
			n++
		}
	}
	b = binary.AppendUvarint(b, uint64(n))
	b = append(b, deps...)
	return appendStores(b, stores, len(params), func(i int) []ShareLink {
		if i < len(shares) {
			return append(shares[i].Links[:len(shares[i].Links):len(shares[i].Links)], shares[i].StoreLinks...)
		}
		return nil
	})
}

// StoresAll is the mask of a function that may store in what any of
// its arguments reaches.
const StoresAll = 1<<EscMaskArgs - 1

// appendStores appends to a record the arguments in whose structures
// the function stores (bit k for argument k), and for each of its n
// parameters the classes that it passes the parameter on to: it stores
// in what the argument reaches if one of those does.
func appendStores(b []byte, stores uint16, n int, links func(i int) []ShareLink) []byte {
	b = binary.AppendUvarint(b, uint64(stores&StoresAll))
	var deps []byte
	nd := 0
	for i := 0; i < n && i < EscMaskArgs; i++ {
		for _, l := range links(i) {
			deps = append(deps, byte(i), l.Arg)
			deps = binary.AppendUvarint(deps, uint64(len(l.Key)))
			deps = append(deps, l.Key...)
			nd++
		}
	}
	b = binary.AppendUvarint(b, uint64(nd))
	return append(b, deps...)
}

// LocalSpans reports whether the target has spans of local objects
// (runtime.localSpans): only there does compiled code ask whether an
// object is local, and the linker need the records.
func LocalSpans(gd *base.Invocation) bool {
	if gd.Flag.Race || gd.Flag.MSan || gd.Flag.ASan {
		return false
	}
	switch gd.Ctxt.Headtype {
	case objabi.Hlinux, objabi.Hdarwin, objabi.Hfreebsd, objabi.Hwindows:
	default:
		return false
	}
	switch gd.Ctxt.Arch.Family {
	case sys.AMD64, sys.ARM64:
		return true
	}
	return false
}
