// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

// HasTearable reports whether a value of type t holds a slice, a string
// or an interface: values of more than one word, which are loaded and
// stored whole where goroutines may race on them (runtime/shared.go).
func HasTearable(t *Type) bool {
	switch {
	case t.IsSlice(), t.IsString(), t.IsInterface():
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasTearable(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasTearable(f.Type) {
				return true
			}
		}
	}
	return false
}

// HasIface reports whether a value of type t holds an interface.
func HasIface(t *Type) bool {
	switch {
	case t.IsInterface():
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasIface(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasIface(f.Type) {
				return true
			}
		}
	}
	return false
}

// HasString reports whether a value of type t holds a string.
func HasString(t *Type) bool {
	switch {
	case t.IsString():
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasString(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasString(f.Type) {
				return true
			}
		}
	}
	return false
}

// HasWhole reports whether a value of type t holds values that are
// loaded and stored whole by default, without the sharing analysis: the
// interfaces, and the strings where they are aligned to 16
// (StringAligned16; runtime/shared_string.go).
func HasWhole(t *Type) bool {
	return HasIface(t) || StringAligned16() && HasString(t)
}
