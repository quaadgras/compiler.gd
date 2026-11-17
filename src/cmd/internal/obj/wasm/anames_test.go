// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"cmd/internal/obj"
	"testing"
)

// TestAnames checks that Anames has a name for every instruction, in
// their order: the assembler finds an instruction by its name, so one
// that is missing gives every instruction after it the wrong name.
func TestAnames(t *testing.T) {
	if got, want := len(Anames), int(ALAST-obj.ABaseWasm)+1; got != want {
		t.Fatalf("len(Anames) = %d, want %d: is anames.go generated from a.out.go?", got, want)
	}
	for _, c := range []struct {
		as   obj.As
		name string
	}{{AGet, "Get"}, {ALinePlace, "LinePlace"}, {AMOVB, "MOVB"}, {AMOVD, "MOVD"}, {ALAST, "LAST"}} {
		if got := Anames[c.as-obj.ABaseWasm]; got != c.name {
			t.Errorf("Anames[%d] = %q, want %q", c.as-obj.ABaseWasm, got, c.name)
		}
	}
}
