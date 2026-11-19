// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objabi

import (
	"internal/syslist"
	"slices"
	"testing"
)

func TestAPEHosts(t *testing.T) {
	if !slices.Equal(APEHosts, syslist.APEHosts) {
		t.Errorf("APEHosts = %v, internal/syslist.APEHosts = %v", APEHosts, syslist.APEHosts)
	}
}
