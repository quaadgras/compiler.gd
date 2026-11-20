// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objabi

import (
	"internal/syslist"
	"maps"
	"slices"
	"testing"
)

func TestPortableHosts(t *testing.T) {
	if !maps.EqualFunc(PortableHosts, syslist.PortableHosts, slices.Equal) {
		t.Errorf("PortableHosts = %v, internal/syslist.PortableHosts = %v", PortableHosts, syslist.PortableHosts)
	}
}
