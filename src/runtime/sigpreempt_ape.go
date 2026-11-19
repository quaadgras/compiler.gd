// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

// sigPreempt is the host's _SIGURG, set by apeinit.
var sigPreempt uint32
