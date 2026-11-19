// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !ape && !apegen

package scan_test

import (
	"internal/runtime/gc/scan"
	"testing"
)

func TestScanSpanPackedGo(t *testing.T) {
	testScanSpanPacked(t, scan.ScanSpanPackedGo)
}
