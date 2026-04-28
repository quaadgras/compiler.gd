// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

// Stub used when cmd/go is being built as go_bootstrap (toolchain1's
// cmd/go that compiles toolchain2/3). cmd/dist forbids go_bootstrap
// from depending on cgo packages, and the real stdembed transitively
// pulls in archive/tar → os/user (for tar header Uname/Gname),
// which is cgo-using on most platforms.
//
// During bootstrap, the real $GOROOT/src tree is always present, so
// the embedded archive is never needed — IsEmpty reporting true and
// Materialize returning nil makes cmd/go fall through to GOROOT.

package stdembed

// IsEmpty always returns true under cmd_go_bootstrap; bootstrap builds
// don't carry the embedded archive.
func IsEmpty() bool { return true }

// Hash returns the empty-archive hash. Stable so callers comparing
// against a sentinel see a consistent value.
func Hash() string { return "" }

// Materialize is a no-op under cmd_go_bootstrap. Bootstrap always has
// $GOROOT/src on disk; the materialisation path doesn't apply.
func Materialize(dst string) error { return nil }
