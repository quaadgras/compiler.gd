// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"cmd/internal/sys"
	"cmd/link/internal/ld"
)

func Init() (*sys.Arch, ld.Arch) {
	theArch := ld.Arch{
		Funcalign: 16,
		Maxalign:  32,
		Minalign:  1,

		Archinit:      archinit,
		AssignAddress: assignAddress,
		Asmb:          asmb,
		Asmb2:         asmb2,
		Gentext:       gentext,
	}

	return sys.ArchWasm, theArch
}

func archinit(ctxt *ld.Link) {
	if ctxt.FlagRound == -1 {
		ctxt.FlagRound = 4096
	}
	if ctxt.FlagTextAddr == -1 {
		ctxt.FlagTextAddr = 0
	}
}
