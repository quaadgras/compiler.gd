// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js || wasip1

package filelock

// WebAssembly has no file locks: locking succeeds and does nothing.
// In a page the go command is the only program that sees its files;
// under node, two go commands that share a cache are not kept apart
// (the cache writes its entries whole, by rename).

type lockType int8

const (
	readLock = iota + 1
	writeLock
)

func lock(f File, lt lockType) error { return nil }

func unlock(f File) error { return nil }
