// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gd

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/noder"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/pkginit"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/staticdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/archive"
	"cmd/internal/bio"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"encoding/json"
	"fmt"
	"strings"
)

// These modes say which kind of object file to generate.
// The default use of the toolchain is to set both bits,
// generating a combined compiler+linker object, one that
// serves to describe the package to both the compiler and the linker.
// In fact the compiler and linker read nearly disjoint sections of
// that file, though, so in a distributed build setting it can be more
// efficient to split the output into two files, supplying the compiler
// object only to future compilations and the linker object only to
// future links.
//
// By default a combined object is written, but if -linkobj is specified
// on the command line then the default -o output is a compiler object
// and the -linkobj output is a linker object.
const (
	modeCompilerObj = 1 << iota
	modeLinkerObj
)

func dumpobj(gd *base.Invocation) {
	if gd.Flag.LinkObj == "" {
		dumpobj1(gd, gd.Flag.LowerO, modeCompilerObj|modeLinkerObj)
		return
	}
	dumpobj1(gd, gd.Flag.LowerO, modeCompilerObj)
	dumpobj1(gd, gd.Flag.LinkObj, modeLinkerObj)
}

func dumpobj1(gd *base.Invocation, outfile string, mode int) {
	bout, err := bio.Create(outfile)
	if err != nil {
		gd.FlushErrors()
		fmt.Printf("can't create %s: %v\n", outfile, err)
		gd.ErrorExit()
	}

	bout.WriteString("!<arch>\n")

	if mode&modeCompilerObj != 0 {
		start := startArchiveEntry(bout)
		dumpCompilerObj(gd, bout)
		finishArchiveEntry(bout, start, "__.PKGDEF")
	}
	if mode&modeLinkerObj != 0 {
		start := startArchiveEntry(bout)
		dumpLinkerObj(gd, bout)
		finishArchiveEntry(bout, start, "_go_.o")
	}

	if err := bout.Close(); err != nil {
		gd.FlushErrors()
		fmt.Printf("error while writing to file %s: %v\n", outfile, err)
		gd.ErrorExit()
	}
}

func printObjHeader(gd *base.Invocation, bout *bio.Writer) {
	bout.WriteString(objabi.HeaderString())
	if gd.Flag.BuildID != "" {
		fmt.Fprintf(bout, "build id %q\n", gd.Flag.BuildID)
	}
	if types.LocalPkg(gd).Name == "main" {
		fmt.Fprintf(bout, "main\n")
	}
	fmt.Fprintf(bout, "\n") // header ends with blank line
}

func startArchiveEntry(bout *bio.Writer) int64 {
	var arhdr [archive.HeaderSize]byte
	bout.Write(arhdr[:])
	return bout.Offset()
}

func finishArchiveEntry(bout *bio.Writer, start int64, name string) {
	bout.Flush()
	size := bout.Offset() - start
	if size&1 != 0 {
		bout.WriteByte(0)
	}
	bout.MustSeek(start-archive.HeaderSize, 0)

	var arhdr [archive.HeaderSize]byte
	archive.FormatHeader(arhdr[:], name, size)
	bout.Write(arhdr[:])
	bout.Flush()
	bout.MustSeek(start+size+(size&1), 0)
}

func dumpCompilerObj(gd *base.Invocation, bout *bio.Writer) {
	printObjHeader(gd, bout)
	noder.WriteExports(gd, bout)
}

func dumpdata(gd *base.Invocation) {
	reflectdata.WriteGCSymbols(gd)
	reflectdata.WritePluginTable(gd)
	dumpembeds(gd)

	if reflectdata.ZeroSize > 0 {
		zero := gd.PkgLinksym("go:map", "zero", obj.ABI0)
		objw.Global(gd, zero, int32(reflectdata.ZeroSize), obj.DUPOK|obj.RODATA)
		zero.Set(obj.AttrStatic, true)
	}

	staticdata.WriteFuncSyms(gd)
	addGCLocals(gd)
}

func dumpLinkerObj(gd *base.Invocation, bout *bio.Writer) {
	printObjHeader(gd, bout)

	if len(typecheck.Target(gd).CgoPragmas) != 0 {
		// write empty export section; must be before cgo section
		fmt.Fprintf(bout, "\n$$\n\n$$\n\n")
		fmt.Fprintf(bout, "\n$$  // cgo\n")
		if err := json.NewEncoder(bout).Encode(typecheck.Target(gd).CgoPragmas); err != nil {
			gd.Fatalf("serializing pragcgobuf: %v", err)
		}
		fmt.Fprintf(bout, "\n$$\n\n")
	}

	fmt.Fprintf(bout, "\n!\n")

	obj.WriteObjFile(gd.Ctxt, bout)
}

func dumpGlobal(gd *base.Invocation, n *ir.Name) {
	if n.Type() == nil {
		gd.Fatalf("external %v nil type\n", n)
	}
	if n.Class == ir.PFUNC {
		return
	}
	if n.Sym().Pkg != types.LocalPkg(gd) {
		return
	}
	types.CalcSize(n.Type())
	ggloblnod(gd, n)
	if n.CoverageAuxVar() || n.Linksym().Static() {
		return
	}
	gd.Ctxt.DwarfGlobal(types.TypeSymName(n.Type()), n.Linksym())
}

func dumpGlobalConst(gd *base.Invocation, n *ir.Name) {
	// only export typed constants
	t := n.Type()
	if t == nil {
		return
	}
	if n.Sym().Pkg != types.LocalPkg(gd) {
		return
	}
	// only export integer constants for now
	if !t.IsInteger() {
		return
	}
	v := n.Val()
	if t.IsUntyped() {
		// Export untyped integers as int (if they fit).
		t = types.Types[types.TINT]
		if ir.ConstOverflow(gd, v, t) {
			return
		}
	} else {
		// If the type of the constant is an instantiated generic, we need to emit
		// that type so the linker knows about it. See issue 51245.
		_ = reflectdata.TypeLinksym(gd, t)
	}
	gd.Ctxt.DwarfIntConst(n.Sym().Name, types.TypeSymName(t), ir.IntVal(gd, t, v))
}

// addGCLocals adds gcargs, gclocals, gcregs, and stack object symbols to Ctxt.Data.
//
// This is done during the sequential phase after compilation, since
// global symbols can't be declared during parallel compilation.
func addGCLocals(gd *base.Invocation) {
	for _, s := range gd.Ctxt.Text {
		fn := s.Func()
		if fn == nil {
			continue
		}
		for _, gcsym := range []*obj.LSym{fn.GCArgs, fn.GCLocals} {
			if gcsym != nil && !gcsym.OnList() {
				objw.Global(gd, gcsym, int32(len(gcsym.P)), obj.RODATA|obj.DUPOK)
			}
		}
		if x := fn.StackObjects; x != nil {
			objw.Global(gd, x, int32(len(x.P)), obj.RODATA)
			x.Set(obj.AttrStatic, true)
		}
		if x := fn.OpenCodedDeferInfo; x != nil {
			objw.Global(gd, x, int32(len(x.P)), obj.RODATA|obj.DUPOK)
		}
		if x := fn.ArgInfo; x != nil {
			objw.Global(gd, x, int32(len(x.P)), obj.RODATA|obj.DUPOK)
			x.Set(obj.AttrStatic, true)
		}
		if x := fn.ArgLiveInfo; x != nil {
			objw.Global(gd, x, int32(len(x.P)), obj.RODATA|obj.DUPOK)
			x.Set(obj.AttrStatic, true)
		}
		if x := fn.WrapInfo; x != nil && !x.OnList() {
			objw.Global(gd, x, int32(len(x.P)), obj.RODATA|obj.DUPOK)
			x.Set(obj.AttrStatic, true)
		}
		for _, jt := range fn.JumpTables {
			objw.Global(gd, jt.Sym, int32(len(jt.Targets)*gd.Ctxt.Arch.PtrSize), obj.RODATA)
		}
	}
}

func ggloblnod(gd *base.Invocation, nam *ir.Name) {
	s := nam.Linksym()

	// main_inittask and runtime_inittask in package runtime (and in
	// test/initempty.go) aren't real variable declarations, but
	// linknamed variables pointing to the compiler's generated
	// .inittask symbol. The real symbol was already written out in
	// pkginit.Task, so we need to avoid writing them out a second time
	// here, otherwise base.Ctxt.Globl will fail.
	if strings.HasSuffix(s.Name, "..inittask") && s.OnList() {
		return
	}

	s.Gotype = reflectdata.TypeLinksym(gd, nam.Type())
	flags := 0
	if nam.Readonly() {
		flags = obj.RODATA
	}
	if nam.Type() != nil && !nam.Type().HasPointers() {
		flags |= obj.NOPTR
	}
	size := nam.Type().Size()
	linkname := nam.Sym().Linkname
	name := nam.Sym().Name

	var saveType objabi.SymKind
	if nam.CoverageAuxVar() {
		saveType = s.Type
	}

	// We've skipped linkname'd globals's instrument, so we can skip them here as well.
	if gd.Flag.ASan && linkname == "" && pkginit.InstrumentGlobalsMap[name] != nil {
		// Write the new size of instrumented global variables that have
		// trailing redzones into object file.
		rzSize := pkginit.GetRedzoneSizeForGlobal(size)
		sizeWithRZ := rzSize + size
		gd.Ctxt.Globl(s, sizeWithRZ, flags)
	} else {
		gd.Ctxt.Globl(s, size, flags)
	}
	if nam.Libfuzzer8BitCounter() {
		s.Type = objabi.SLIBFUZZER_8BIT_COUNTER
	}
	if nam.CoverageAuxVar() && saveType == objabi.SCOVERAGE_COUNTER {
		// restore specialized counter type (which Globl call above overwrote)
		s.Type = saveType
	}
	if nam.Sym().Linkname != "" {
		// Make sure linkname'd symbol is non-package. When a symbol is
		// both imported and linkname'd, s.Pkg may not set to "_" in
		// types.Sym.Linksym because LSym already exists. Set it here.
		s.Pkg = "_"
	}
}

func dumpembeds(gd *base.Invocation) {
	for _, v := range typecheck.Target(gd).Embeds {
		staticdata.WriteEmbed(gd, v)
	}
}
