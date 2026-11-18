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
