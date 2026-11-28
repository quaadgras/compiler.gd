// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cxx

import "internal/c23"

// A std::unordered_map and a std::unordered_set find their elements
// with a map of Go's, std::__go_map<K, V>: from the hash that C++
// computes for a key to the first of the elements that have it. The
// elements themselves are C++'s, each where it was made, as C++ says
// they stay.

// MapGet is (*m)[k]: the zero value if k is not there.
func MapGet[K comparable, V any](m *map[K]V, k K) V { return (*m)[k] }

// MapSet is (*m)[k] = v.
func MapSet[K comparable, V any](m *map[K]V, k K, v V) {
	if *m == nil {
		*m = map[K]V{}
	}
	(*m)[k] = v
}

// MapDelete is delete(*m, k).
func MapDelete[K comparable, V any](m *map[K]V, k K) { delete(*m, k) }

// MapLen is len(*m).
func MapLen[K comparable, V any](m *map[K]V) c23.ULong { return c23.ULong(len(*m)) }

// MapReset makes *m the map of nothing: what a constructor starts with,
// in memory that held anything, and what clear leaves.
func MapReset[K comparable, V any](m *map[K]V) { *m = nil }
