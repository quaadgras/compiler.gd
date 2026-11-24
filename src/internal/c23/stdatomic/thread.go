// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdatomic

import (
	c23 "internal/c23"
)

// stdatomic.h's functions, for C that takes their addresses (calls are
// the macros'). An atomic_flag is an _Atomic _Bool.
func AtomicThreadFence(order uint32) {}

func AtomicFlagTestAndSet(p *c23.Void) uint8 { return c23.AtomicFetch[uint8](p, '=', 1) }

func AtomicFlagTestAndSetExplicit(p *c23.Void, order uint32) uint8 { return AtomicFlagTestAndSet(p) }

func AtomicFlagClear(p *c23.Void) { c23.AtomicStore[uint8](p, 0) }

func AtomicFlagClearExplicit(p *c23.Void, order uint32) { AtomicFlagClear(p) }
