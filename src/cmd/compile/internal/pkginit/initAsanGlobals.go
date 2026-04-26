// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkginit

import (
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// instrumentGlobals declares a global array of _asan_global structures and initializes it.
func instrumentGlobals(gd *base.Invocation, fn *ir.Func) *ir.Name {
	asanGlobalStruct, asanLocationStruct, defStringstruct := createtypes(gd)
	lname := typecheck.Lookup
	tconv := typecheck.ConvNop
	// Make a global array of asanGlobalStruct type.
	// var asanglobals []asanGlobalStruct
	arraytype := types.NewArray(asanGlobalStruct, int64(len(InstrumentGlobalsMap(gd))))
	symG := lname(gd, ".asanglobals")
	globals := ir.NewNameAt(gd, gd.Pos, symG, arraytype)
	globals.Class = ir.PEXTERN
	symG.Def = globals
	typecheck.Target(gd).Externs = append(typecheck.Target(gd).Externs, globals)
	// Make a global array of asanLocationStruct type.
	// var asanL []asanLocationStruct
	arraytype = types.NewArray(asanLocationStruct, int64(len(InstrumentGlobalsMap(gd))))
	symL := lname(gd, ".asanL")
	asanlocation := ir.NewNameAt(gd, gd.Pos, symL, arraytype)
	asanlocation.Class = ir.PEXTERN
	symL.Def = asanlocation
	typecheck.Target(gd).Externs = append(typecheck.Target(gd).Externs, asanlocation)
	// Make three global string variables to pass the global name and module name
	// and the name of the source file that defines it.
	// var asanName string
	// var asanModulename string
	// var asanFilename string
	symL = lname(gd, ".asanName")
	asanName := ir.NewNameAt(gd, gd.Pos, symL, types.Types[types.TSTRING])
	asanName.Class = ir.PEXTERN
	symL.Def = asanName
	typecheck.Target(gd).Externs = append(typecheck.Target(gd).Externs, asanName)

	symL = lname(gd, ".asanModulename")
	asanModulename := ir.NewNameAt(gd, gd.Pos, symL, types.Types[types.TSTRING])
	asanModulename.Class = ir.PEXTERN
	symL.Def = asanModulename
	typecheck.Target(gd).Externs = append(typecheck.Target(gd).Externs, asanModulename)

	symL = lname(gd, ".asanFilename")
	asanFilename := ir.NewNameAt(gd, gd.Pos, symL, types.Types[types.TSTRING])
	asanFilename.Class = ir.PEXTERN
	symL.Def = asanFilename
	typecheck.Target(gd).Externs = append(typecheck.Target(gd).Externs, asanFilename)

	var init ir.Nodes
	var c ir.Node
	// globals[i].odrIndicator = 0 is the default, no need to set it explicitly here.
	for i, n := range InstrumentGlobalsSlice(gd) {
		setField := func(f string, val ir.Node, i int) {
			r := ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT,
				ir.NewIndexExpr(gd, gd.Pos, globals, ir.NewInt(gd, gd.Pos, int64(i))), lname(gd, f)), val)
			init.Append(typecheck.Stmt(gd, r))
		}
		// globals[i].beg = uintptr(unsafe.Pointer(&n))
		c = tconv(gd, typecheck.NodAddr(gd, n), types.Types[types.TUNSAFEPTR])
		c = tconv(gd, c, types.Types[types.TUINTPTR])
		setField("beg", c, i)
		// Assign globals[i].size.
		g := n.(*ir.Name)
		size := g.Type().Size()
		c = typecheck.DefaultLit(gd, ir.NewInt(gd, gd.Pos, size), types.Types[types.TUINTPTR])
		setField("size", c, i)
		// Assign globals[i].sizeWithRedzone.
		rzSize := GetRedzoneSizeForGlobal(size)
		sizeWithRz := rzSize + size
		c = typecheck.DefaultLit(gd, ir.NewInt(gd, gd.Pos, sizeWithRz), types.Types[types.TUINTPTR])
		setField("sizeWithRedzone", c, i)
		// The C string type is terminated by a null character "\0", Go should use three-digit
		// octal "\000" or two-digit hexadecimal "\x00" to create null terminated string.
		// asanName = symbol's linkname + "\000"
		// globals[i].name = (*defString)(unsafe.Pointer(&asanName)).data
		name := g.Linksym().Name
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, asanName, ir.NewString(gd, gd.Pos, name+"\000"))))
		c = tconv(gd, typecheck.NodAddr(gd, asanName), types.Types[types.TUNSAFEPTR])
		c = tconv(gd, c, types.NewPtr(defStringstruct))
		c = ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, c, lname(gd, "data"))
		setField("name", c, i)

		// Set the name of package being compiled as a unique identifier of a module.
		// asanModulename = pkgName + "\000"
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, asanModulename, ir.NewString(gd, gd.Pos, types.LocalPkg(gd).Name+"\000"))))
		c = tconv(gd, typecheck.NodAddr(gd, asanModulename), types.Types[types.TUNSAFEPTR])
		c = tconv(gd, c, types.NewPtr(defStringstruct))
		c = ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, c, lname(gd, "data"))
		setField("moduleName", c, i)
		// Assign asanL[i].filename, asanL[i].line, asanL[i].column
		// and assign globals[i].location = uintptr(unsafe.Pointer(&asanL[i]))
		asanLi := ir.NewIndexExpr(gd, gd.Pos, asanlocation, ir.NewInt(gd, gd.Pos, int64(i)))
		filename := ir.NewString(gd, gd.Pos, gd.Ctxt.PosTable.Pos(n.Pos()).Filename()+"\000")
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, asanFilename, filename)))
		c = tconv(gd, typecheck.NodAddr(gd, asanFilename), types.Types[types.TUNSAFEPTR])
		c = tconv(gd, c, types.NewPtr(defStringstruct))
		c = ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, c, lname(gd, "data"))
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, asanLi, lname(gd, "filename")), c)))
		line := ir.NewInt(gd, gd.Pos, int64(n.Pos().Line()))
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, asanLi, lname(gd, "line")), line)))
		col := ir.NewInt(gd, gd.Pos, int64(n.Pos().Col()))
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, asanLi, lname(gd, "column")), col)))
		c = tconv(gd, typecheck.NodAddr(gd, asanLi), types.Types[types.TUNSAFEPTR])
		c = tconv(gd, c, types.Types[types.TUINTPTR])
		setField("sourceLocation", c, i)
	}
	fn.Body.Append(init...)
	return globals
}

// createtypes creates the asanGlobal, asanLocation and defString struct type.
// Go compiler does not refer to the C types, we represent the struct field
// by a uintptr, then use type conversion to make copies of the data.
// E.g., (*defString)(asanGlobal.name).data to C string.
//
// Keep in sync with src/runtime/asan/asan.go.
// type asanGlobal struct {
//	beg               uintptr
//	size              uintptr
//	size_with_redzone uintptr
//	name              uintptr
//	moduleName        uintptr
//	hasDynamicInit    uintptr
//	sourceLocation    uintptr
//	odrIndicator      uintptr
// }
//
// type asanLocation struct {
//	filename uintptr
//	line     int32
//	column   int32
// }
//
// defString is synthesized struct type meant to capture the underlying
// implementations of string.
// type defString struct {
//	data uintptr
//	len  uintptr
// }

func createtypes(gd *base.Invocation) (*types.Type, *types.Type, *types.Type) {
	up := types.Types[types.TUINTPTR]
	i32 := types.Types[types.TINT32]
	fname := typecheck.Lookup
	nxp := src.NoXPos
	nfield := types.NewField
	asanGlobal := types.NewStruct([]*types.Field{
		nfield(nxp, fname(gd, "beg"), up),
		nfield(nxp, fname(gd, "size"), up),
		nfield(nxp, fname(gd, "sizeWithRedzone"), up),
		nfield(nxp, fname(gd, "name"), up),
		nfield(nxp, fname(gd, "moduleName"), up),
		nfield(nxp, fname(gd, "hasDynamicInit"), up),
		nfield(nxp, fname(gd, "sourceLocation"), up),
		nfield(nxp, fname(gd, "odrIndicator"), up),
	})
	types.CalcSize(asanGlobal)

	asanLocation := types.NewStruct([]*types.Field{
		nfield(nxp, fname(gd, "filename"), up),
		nfield(nxp, fname(gd, "line"), i32),
		nfield(nxp, fname(gd, "column"), i32),
	})
	types.CalcSize(asanLocation)

	defString := types.NewStruct([]*types.Field{
		types.NewField(nxp, fname(gd, "data"), up),
		types.NewField(nxp, fname(gd, "len"), up),
	})
	types.CalcSize(defString)

	return asanGlobal, asanLocation, defString
}

// Calculate redzone for globals.
func GetRedzoneSizeForGlobal(size int64) int64 {
	maxRZ := int64(1 << 18)
	minRZ := int64(32)
	redZone := (size / minRZ / 4) * minRZ
	switch {
	case redZone > maxRZ:
		redZone = maxRZ
	case redZone < minRZ:
		redZone = minRZ
	}
	// Round up to multiple of minRZ.
	if size%minRZ != 0 {
		redZone += minRZ - (size % minRZ)
	}
	return redZone
}

// InstrumentGlobalsMap contains only package-local (and unlinknamed from somewhere else)
// globals.
// And the key is the object name. For example, in package p, a global foo would be in this
// map as "foo".
// Consider range over maps is nondeterministic, make a slice to hold all the values in the
// InstrumentGlobalsMap and iterate over the InstrumentGlobalsSlice.
// InstrumentGlobalsMap returns the per-Invocation instrumentation map,
// lazy-initialised on first use.
func InstrumentGlobalsMap(gd *base.Invocation) map[string]ir.Node {
	m, _ := gd.PkginitInstrumentGlobalsMap.(map[string]ir.Node)
	if m == nil {
		m = make(map[string]ir.Node)
		gd.PkginitInstrumentGlobalsMap = m
	}
	return m
}

// InstrumentGlobalsSlice returns the deterministic-order companion to
// InstrumentGlobalsMap.
func InstrumentGlobalsSlice(gd *base.Invocation) []ir.Node {
	s, _ := gd.PkginitInstrumentGlobalsSlice.([]ir.Node)
	return s
}

func canInstrumentGlobal(gd *base.Invocation, g ir.Node) bool {
	if g.Op() != ir.ONAME {
		return false
	}
	n := g.(*ir.Name)
	if n.Class == ir.PFUNC {
		return false
	}
	if n.Sym().Pkg != types.LocalPkg(gd) {
		return false
	}
	// Do not instrument any _cgo_ related global variables, because they are declared in C code.
	if strings.Contains(n.Sym().Name, "cgo") {
		return false
	}

	// Do not instrument counter globals in internal/fuzz. These globals are replaced by the linker.
	// See go.dev/issue/72766 for more details.
	if n.Sym().Pkg.Path == "internal/fuzz" && (n.Sym().Name == "_counters" || n.Sym().Name == "_ecounters") {
		return false
	}

	// Do not instrument globals that are linknamed, because their home package will do the work.
	if n.Sym().Linkname != "" {
		return false
	}

	return true
}
