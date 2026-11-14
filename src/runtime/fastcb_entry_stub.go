// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 && !arm64

package runtime

// The direct C-ABI virtual-call entry thunk exists on amd64 and arm64;
// see fastcb_entry_amd64.go and fastcb_entry_arm64.go.
const fastcbEntrySupported = false

func fastcbEntryPC() uintptr { return 0 }
