// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64

package runtime

import "unsafe"

// strhash should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/aristanetworks/goarista
//   - github.com/bytedance/sonic
//   - github.com/bytedance/go-tagexpr/v2
//   - github.com/cloudwego/dynamicgo
//   - github.com/v2fly/v2ray-core/v5
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
// gd fork: implemented in asm_$GOARCH.s. It reads the cached hash in
// word 1 of heap-rep strings and folds the inline-rep fast path; on a
// miss it routes through runtime·memhash (maps.MemHash) with seed 0.
//
//go:nosplit
//go:linkname strhash
func strhash(p unsafe.Pointer, h uintptr) uintptr
