// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package x86

import (
	"cmd/compile/internal/ssagen"
	"cmd/internal/obj/x86"
	"fmt"
	"internal/buildcfg"
	"os"
)

func Init(arch *ssagen.ArchInfo) {
	arch.LinkArch = &x86.Link386
	arch.REGSP = x86.REGSP
	arch.SSAGenValue = ssaGenValue
	arch.SSAGenBlock = ssaGenBlock
	arch.MAXWIDTH = (1 << 32) - 1
	switch v := buildcfg.GO386; v {
	case "sse2":
	case "softfloat":
		arch.SoftFloat = true
	case "387":
		// TODO(in-process): These two os.Exit calls take down the host
		// process; convert to panic + handle in gd.Main when an
		// embedder cares about GO386=387/unknown failures. Reachable
		// only on 386 builds with an unsupported GO386 setting.
		fmt.Fprintf(os.Stderr, "unsupported setting GO386=387. Consider using GO386=softfloat instead.\n")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unsupported setting GO386=%s\n", v)
		os.Exit(1)

	}

	arch.ZeroRange = zerorange
	arch.Ginsnop = ginsnop

	arch.SSAMarkMoves = ssaMarkMoves
}
