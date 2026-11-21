// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import ()

// search.h: hsearch's table, tsearch's trees, lfind and lsearch, and
// insque and remque.

// tsearch's nodes: {const void *key; node *left, *right; height}, an
// AVL tree as musl's. A node's address is what tsearch returns: C reads
// the key through it.
