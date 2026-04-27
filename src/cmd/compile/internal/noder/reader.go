// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"encoding/hex"
	"fmt"
	"go/constant"
	"internal/buildcfg"
	"internal/pkgbits"
	"path/filepath"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/dwarfgen"
	"cmd/compile/internal/inline"
	"cmd/compile/internal/inline/interleaved"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/staticinit"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/hash"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/src"
)

// This file implements cmd/compile backend's reader for the Unified
// IR export data.

// A pkgReader reads Unified IR export data.
type pkgReader struct {
	pkgbits.PkgDecoder

	gd *base.Invocation

	// Indices for encoded things; lazily populated as needed.
	//
	// Note: Objects (i.e., ir.Names) are lazily instantiated by
	// populating their types.Sym.Def; see objReader below.

	posBases []*src.PosBase
	pkgs     []*types.Pkg
	typs     []*types.Type

	// offset for rewriting the given (absolute!) index into the output,
	// but bitwise inverted so we can detect if we're missing the entry
	// or not.
	newindex []index
}

func newPkgReader(gd *base.Invocation, pr pkgbits.PkgDecoder) *pkgReader {
	return &pkgReader{
		PkgDecoder: pr,
		gd:         gd,

		posBases: make([]*src.PosBase, pr.NumElems(pkgbits.SectionPosBase)),
		pkgs:     make([]*types.Pkg, pr.NumElems(pkgbits.SectionPkg)),
		typs:     make([]*types.Type, pr.NumElems(pkgbits.SectionType)),

		newindex: make([]index, pr.TotalElems()),
	}
}

// A pkgReaderIndex compactly identifies an index (and its
// corresponding dictionary) within a package's export data.
type pkgReaderIndex struct {
	pr        *pkgReader
	idx       index
	dict      *readerDict
	methodSym *types.Sym

	// gd is propagated to readers built from this index for the
	// synthetic case (where pr is nil but the synthetic body still
	// needs an Invocation for diag/Pos routing).
	gd *base.Invocation

	synthetic func(pos src.XPos, r *reader)
}

func (pri pkgReaderIndex) asReader(k pkgbits.SectionKind, marker pkgbits.SyncMarker) *reader {
	if pri.synthetic != nil {
		return &reader{gd: pri.gd, synthetic: pri.synthetic}
	}

	r := pri.pr.newReader(k, pri.idx, marker)
	r.dict = pri.dict
	r.methodSym = pri.methodSym
	return r
}

func (pr *pkgReader) newReader(k pkgbits.SectionKind, idx index, marker pkgbits.SyncMarker) *reader {
	return &reader{
		Decoder: pr.NewDecoder(k, idx, marker),
		p:       pr,
		gd:      pr.gd,
	}
}

// A reader provides APIs for reading an individual element.
type reader struct {
	pkgbits.Decoder

	gd *base.Invocation
	p  *pkgReader

	dict *readerDict

	// TODO(mdempsky): The state below is all specific to reading
	// function bodies. It probably makes sense to split it out
	// separately so that it doesn't take up space in every reader
	// instance.

	curfn       *ir.Func
	locals      []*ir.Name
	closureVars []*ir.Name

	// funarghack is used during inlining to suppress setting
	// Field.Nname to the inlined copies of the parameters. This is
	// necessary because we reuse the same types.Type as the original
	// function, and most of the compiler still relies on field.Nname to
	// find parameters/results.
	funarghack bool

	// methodSym is the name of method's name, if reading a method.
	// It's nil if reading a normal function or closure body.
	methodSym *types.Sym

	// dictParam is the .dict param, if any.
	dictParam *ir.Name

	// synthetic is a callback function to construct a synthetic
	// function body. It's used for creating the bodies of function
	// literals used to curry arguments to shaped functions.
	synthetic func(pos src.XPos, r *reader)

	// scopeVars is a stack tracking the number of variables declared in
	// the current function at the moment each open scope was opened.
	scopeVars         []int
	marker            dwarfgen.ScopeMarker
	lastCloseScopePos src.XPos

	// === details for handling inline body expansion ===

	// If we're reading in a function body because of inlining, this is
	// the call that we're inlining for.
	inlCaller    *ir.Func
	inlCall      *ir.CallExpr
	inlFunc      *ir.Func
	inlTreeIndex int
	inlPosBases  map[*src.PosBase]*src.PosBase

	// suppressInlPos tracks whether position base rewriting for
	// inlining should be suppressed. See funcLit.
	suppressInlPos int

	delayResults bool

	// Label to return to.
	retlabel *types.Sym
}

// A readerDict represents an instantiated "compile-time dictionary,"
// used for resolving any derived types needed for instantiating a
// generic object.
//
// A compile-time dictionary can either be "shaped" or "non-shaped."
// Shaped compile-time dictionaries are only used for instantiating
// shaped type definitions and function bodies, while non-shaped
// compile-time dictionaries are used for instantiating runtime
// dictionaries.
type readerDict struct {
	shaped bool // whether this is a shaped dictionary

	// baseSym is the symbol for the object this dictionary belongs to.
	// If the object is an instantiated function or defined type, then
	// baseSym is the mangled symbol, including any type arguments.
	baseSym *types.Sym

	// For non-shaped dictionaries, shapedObj is a reference to the
	// corresponding shaped object (always a function or defined type).
	shapedObj *ir.Name

	// targs holds the implicit and explicit type arguments in use for
	// reading the current object. For example:
	//
	//	func F[T any]() {
	//		type X[U any] struct { t T; u U }
	//		var _ X[string]
	//	}
	//
	//	var _ = F[int]
	//
	// While instantiating F[int], we need to in turn instantiate
	// X[string]. [int] and [string] are explicit type arguments for F
	// and X, respectively; but [int] is also the implicit type
	// arguments for X.
	//
	// (As an analogy to function literals, explicits are the function
	// literal's formal parameters, while implicits are variables
	// captured by the function literal.)
	targs []*types.Type

	// implicits counts how many of types within targs are implicit type
	// arguments; the rest are explicit.
	implicits int

	derived      []derivedInfo // reloc index of the derived type's descriptor
	derivedTypes []*types.Type // slice of previously computed derived types

	// These slices correspond to entries in the runtime dictionary.
	typeParamMethodExprs []readerMethodExprInfo
	subdicts             []objInfo
	rtypes               []typeInfo
	itabs                []itabInfo
}

type readerMethodExprInfo struct {
	typeParamIdx int
	method       *types.Sym
}

func setType(n ir.Node, typ *types.Type) {
	n.SetType(typ)
	n.SetTypecheck(1)
}

func setValue(name *ir.Name, val constant.Value) {
	name.SetVal(val)
	name.Defn = nil
}

// @@@ Positions

// pos reads a position from the bitstream.
func (r *reader) pos() src.XPos {
	return r.gd.Ctxt.PosTable.XPos(r.pos0())
}

// origPos reads a position from the bitstream, and returns both the
// original raw position and an inlining-adjusted position.
func (r *reader) origPos() (origPos, inlPos src.XPos) {
	r.suppressInlPos++
	origPos = r.pos()
	r.suppressInlPos--
	inlPos = r.inlPos(origPos)
	return
}

func (r *reader) pos0() src.Pos {
	r.Sync(pkgbits.SyncPos)
	if !r.Bool() {
		return src.NoPos
	}

	posBase := r.posBase()
	line := r.Uint()
	col := r.Uint()
	return src.MakePos(posBase, line, col)
}

// posBase reads a position base from the bitstream.
func (r *reader) posBase() *src.PosBase {
	return r.inlPosBase(r.p.posBaseIdx(r.Reloc(pkgbits.SectionPosBase)))
}

// posBaseIdx returns the specified position base, reading it first if
// needed.
func (pr *pkgReader) posBaseIdx(idx index) *src.PosBase {
	if b := pr.posBases[idx]; b != nil {
		return b
	}

	r := pr.newReader(pkgbits.SectionPosBase, idx, pkgbits.SyncPosBase)
	var b *src.PosBase

	absFilename := r.String()
	filename := absFilename

	// For build artifact stability, the export data format only
	// contains the "absolute" filename as returned by objabi.AbsFile.
	// However, some tests (e.g., test/run.go's asmcheck tests) expect
	// to see the full, original filename printed out. Re-expanding
	// "$GOROOT" to buildcfg.GOROOT is a close-enough approximation to
	// satisfy this.
	//
	// The export data format only ever uses slash paths
	// (for cross-operating-system reproducible builds),
	// but error messages need to use native paths (backslash on Windows)
	// as if they had been specified on the command line.
	// (The go command always passes native paths to the compiler.)
	const dollarGOROOT = "$GOROOT"
	if buildcfg.GOROOT != "" && strings.HasPrefix(filename, dollarGOROOT) {
		filename = filepath.FromSlash(buildcfg.GOROOT + filename[len(dollarGOROOT):])
	}

	if r.Bool() {
		b = src.NewFileBase(filename, absFilename)
	} else {
		pos := r.pos0()
		line := r.Uint()
		col := r.Uint()
		b = src.NewLinePragmaBase(pos, filename, absFilename, line, col)
	}

	pr.posBases[idx] = b
	return b
}

// inlPosBase returns the inlining-adjusted src.PosBase corresponding
// to oldBase, which must be a non-inlined position. When not
// inlining, this is just oldBase.
func (r *reader) inlPosBase(oldBase *src.PosBase) *src.PosBase {
	if index := oldBase.InliningIndex(); index >= 0 {
		r.gd.Fatalf("oldBase %v already has inlining index %v", oldBase, index)
	}

	if r.inlCall == nil || r.suppressInlPos != 0 {
		return oldBase
	}

	if newBase, ok := r.inlPosBases[oldBase]; ok {
		return newBase
	}

	newBase := src.NewInliningBase(oldBase, r.inlTreeIndex)
	r.inlPosBases[oldBase] = newBase
	return newBase
}

// inlPos returns the inlining-adjusted src.XPos corresponding to
// xpos, which must be a non-inlined position. When not inlining, this
// is just xpos.
func (r *reader) inlPos(xpos src.XPos) src.XPos {
	pos := r.gd.Ctxt.PosTable.Pos(xpos)
	pos.SetBase(r.inlPosBase(pos.Base()))
	return r.gd.Ctxt.PosTable.XPos(pos)
}

// @@@ Packages

// pkg reads a package reference from the bitstream.
func (r *reader) pkg() *types.Pkg {
	r.Sync(pkgbits.SyncPkg)
	return r.p.pkgIdx(r.Reloc(pkgbits.SectionPkg))
}

// pkgIdx returns the specified package from the export data, reading
// it first if needed.
func (pr *pkgReader) pkgIdx(idx index) *types.Pkg {
	if pkg := pr.pkgs[idx]; pkg != nil {
		return pkg
	}

	pkg := pr.newReader(pkgbits.SectionPkg, idx, pkgbits.SyncPkgDef).doPkg()
	pr.pkgs[idx] = pkg
	return pkg
}

// doPkg reads a package definition from the bitstream.
func (r *reader) doPkg() *types.Pkg {
	path := r.String()
	switch path {
	case "":
		path = r.p.PkgPath()
	case "builtin":
		return types.BuiltinPkg(r.gd)
	case "unsafe":
		return types.UnsafePkg(r.gd)
	}

	name := r.String()

	pkg := types.NewPkg(r.gd, path, "")

	if pkg.Name == "" {
		pkg.Name = name
	} else {
		r.gd.Assertf(pkg.Name == name, "package %q has name %q, but want %q", pkg.Path, pkg.Name, name)
	}

	return pkg
}

// @@@ Types

func (r *reader) typ() *types.Type {
	return r.typWrapped(true)
}

// typWrapped is like typ, but allows suppressing generation of
// unnecessary wrappers as a compile-time optimization.
func (r *reader) typWrapped(wrapped bool) *types.Type {
	return r.p.typIdx(r.typInfo(), r.dict, wrapped)
}

func (r *reader) typInfo() typeInfo {
	r.Sync(pkgbits.SyncType)
	if r.Bool() {
		return typeInfo{idx: index(r.Len()), derived: true}
	}
	return typeInfo{idx: r.Reloc(pkgbits.SectionType), derived: false}
}

// typListIdx returns a list of the specified types, resolving derived
// types within the given dictionary.
func (pr *pkgReader) typListIdx(infos []typeInfo, dict *readerDict) []*types.Type {
	typs := make([]*types.Type, len(infos))
	for i, info := range infos {
		typs[i] = pr.typIdx(info, dict, true)
	}
	return typs
}

// typIdx returns the specified type. If info specifies a derived
// type, it's resolved within the given dictionary. If wrapped is
// true, then method wrappers will be generated, if appropriate.
func (pr *pkgReader) typIdx(info typeInfo, dict *readerDict, wrapped bool) *types.Type {
	idx := info.idx
	var where **types.Type
	if info.derived {
		where = &dict.derivedTypes[idx]
		idx = dict.derived[idx].idx
	} else {
		where = &pr.typs[idx]
	}

	if typ := *where; typ != nil {
		return typ
	}

	r := pr.newReader(pkgbits.SectionType, idx, pkgbits.SyncTypeIdx)
	r.dict = dict

	typ := r.doTyp()
	if typ == nil {
		pr.gd.Fatalf("doTyp returned nil for info=%v", info)
	}

	// For recursive type declarations involving interfaces and aliases,
	// above r.doTyp() call may have already set pr.typs[idx], so just
	// double check and return the type.
	//
	// Example:
	//
	//     type F = func(I)
	//
	//     type I interface {
	//         m(F)
	//     }
	//
	// The writer writes data types in following index order:
	//
	//     0: func(I)
	//     1: I
	//     2: interface{m(func(I))}
	//
	// The reader resolves it in following index order:
	//
	//     0 -> 1 -> 2 -> 0 -> 1
	//
	// and can divide in logically 2 steps:
	//
	//  - 0 -> 1     : first time the reader reach type I,
	//                 it creates new named type with symbol I.
	//
	//  - 2 -> 0 -> 1: the reader ends up reaching symbol I again,
	//                 now the symbol I was setup in above step, so
	//                 the reader just return the named type.
	//
	// Now, the functions called return, the pr.typs looks like below:
	//
	//  - 0 -> 1 -> 2 -> 0 : [<T> I <T>]
	//  - 0 -> 1 -> 2      : [func(I) I <T>]
	//  - 0 -> 1           : [func(I) I interface { "".m(func("".I)) }]
	//
	// The idx 1, corresponding with type I was resolved successfully
	// after r.doTyp() call.

	if prev := *where; prev != nil {
		return prev
	}

	if wrapped {
		// Only cache if we're adding wrappers, so that other callers that
		// find a cached type know it was wrapped.
		*where = typ

		r.needWrapper(typ)
	}

	if !typ.IsUntyped() {
		types.CheckSize(typ)
	}

	return typ
}

func (r *reader) doTyp() *types.Type {
	switch tag := pkgbits.CodeType(r.Code(pkgbits.SyncType)); tag {
	default:
		panic(fmt.Sprintf("unexpected type: %v", tag))

	case pkgbits.TypeBasic:
		return *basics[r.Len()]

	case pkgbits.TypeNamed:
		obj := r.obj()
		assert(r.gd, obj.Op() == ir.OTYPE)
		return obj.Type()

	case pkgbits.TypeTypeParam:
		return r.dict.targs[r.Len()]

	case pkgbits.TypeArray:
		len := int64(r.Uint64())
		return types.NewArray(r.typ(), len)
	case pkgbits.TypeChan:
		dir := dirs[r.Len()]
		return types.NewChan(r.typ(), dir)
	case pkgbits.TypeMap:
		return types.NewMap(r.typ(), r.typ())
	case pkgbits.TypePointer:
		return types.NewPtr(r.typ())
	case pkgbits.TypeSignature:
		return r.signature(nil)
	case pkgbits.TypeSlice:
		return types.NewSlice(r.typ())
	case pkgbits.TypeStruct:
		return r.structType()
	case pkgbits.TypeInterface:
		return r.interfaceType()
	case pkgbits.TypeUnion:
		return r.unionType()
	}
}

func (r *reader) unionType() *types.Type {
	// In the types1 universe, we only need to handle value types.
	// Impure interfaces (i.e., interfaces with non-trivial type sets
	// like "int | string") can only appear as type parameter bounds,
	// and this is enforced by the types2 type checker.
	//
	// However, type unions can still appear in pure interfaces if the
	// type union is equivalent to "any". E.g., typeparam/issue52124.go
	// declares variables with the type "interface { any | int }".
	//
	// To avoid needing to represent type unions in types1 (since we
	// don't have any uses for that today anyway), we simply fold them
	// to "any".

	// TODO(mdempsky): Restore consistency check to make sure folding to
	// "any" is safe. This is unfortunately tricky, because a pure
	// interface can reference impure interfaces too, including
	// cyclically (#60117).
	if false {
		pure := false
		for i, n := 0, r.Len(); i < n; i++ {
			_ = r.Bool() // tilde
			term := r.typ()
			if term.IsEmptyInterface() {
				pure = true
			}
		}
		if !pure {
			r.gd.Fatalf("impure type set used in value type")
		}
	}

	return types.Types[types.TINTER]
}

func (r *reader) interfaceType() *types.Type {
	nmethods, nembeddeds := r.Len(), r.Len()
	implicit := nmethods == 0 && nembeddeds == 1 && r.Bool()
	assert(r.gd, !implicit) // implicit interfaces only appear in constraints

	fields := make([]*types.Field, nmethods+nembeddeds)
	methods, embeddeds := fields[:nmethods], fields[nmethods:]

	for i := range methods {
		methods[i] = types.NewField(r.pos(), r.selector(), r.signature(types.FakeRecv()))
	}
	for i := range embeddeds {
		embeddeds[i] = types.NewField(src.NoXPos, nil, r.typ())
	}

	if len(fields) == 0 {
		return types.Types[types.TINTER] // empty interface
	}
	return types.NewInterface(fields)
}

func (r *reader) structType() *types.Type {
	fields := make([]*types.Field, r.Len())
	for i := range fields {
		field := types.NewField(r.pos(), r.selector(), r.typ())
		field.Note = r.String()
		if r.Bool() {
			field.Embedded = 1
		}
		fields[i] = field
	}
	return types.NewStruct(fields)
}

func (r *reader) signature(recv *types.Field) *types.Type {
	r.Sync(pkgbits.SyncSignature)

	params := r.params()
	results := r.params()
	if r.Bool() { // variadic
		params[len(params)-1].SetIsDDD(true)
	}

	// gd Phase G.2.1: wire format is stock. NewSignature extends
	// locally based on results — every compile in the tree uses the
	// same PhaseGActive gate so the outcome is reproducible without
	// needing to ride a bit through pkgbits.
	return types.NewSignature(r.gd, recv, params, results)
}

func (r *reader) params() []*types.Field {
	r.Sync(pkgbits.SyncParams)
	params := make([]*types.Field, r.Len())
	for i := range params {
		params[i] = r.param()
	}
	return params
}

func (r *reader) param() *types.Field {
	r.Sync(pkgbits.SyncParam)
	return types.NewField(r.pos(), r.localIdent(), r.typ())
}

// @@@ Objects

// objReader maps qualified identifiers (represented as *types.Sym) to
// a pkgReader and corresponding index that can be used for reading
// that object's definition. Lives on *base.Invocation (gd.NoderObjReader)
// so multiple compile invocations don't share the table.
func objReader(gd *base.Invocation) map[*types.Sym]pkgReaderIndex {
	m, _ := gd.NoderObjReader.(map[*types.Sym]pkgReaderIndex)
	if m == nil {
		m = map[*types.Sym]pkgReaderIndex{}
		gd.NoderObjReader = m
	}
	return m
}

// obj reads an instantiated object reference from the bitstream.
func (r *reader) obj() ir.Node {
	return r.p.objInstIdx(r.objInfo(), r.dict, false)
}

// objInfo reads an instantiated object reference from the bitstream
// and returns the encoded reference to it, without instantiating it.
func (r *reader) objInfo() objInfo {
	r.Sync(pkgbits.SyncObject)
	if r.Version().Has(pkgbits.DerivedFuncInstance) {
		assert(r.gd, !r.Bool())
	}
	idx := r.Reloc(pkgbits.SectionObj)

	explicits := make([]typeInfo, r.Len())
	for i := range explicits {
		explicits[i] = r.typInfo()
	}

	return objInfo{idx, explicits}
}

// objInstIdx returns the encoded, instantiated object. If shaped is
// true, then the shaped variant of the object is returned instead.
func (pr *pkgReader) objInstIdx(info objInfo, dict *readerDict, shaped bool) ir.Node {
	explicits := pr.typListIdx(info.explicits, dict)

	var implicits []*types.Type
	if dict != nil {
		implicits = dict.targs
	}

	return pr.objIdx(info.idx, implicits, explicits, shaped)
}

// objIdx returns the specified object, instantiated with the given
// type arguments, if any.
// If shaped is true, then the shaped variant of the object is returned
// instead.
func (pr *pkgReader) objIdx(idx index, implicits, explicits []*types.Type, shaped bool) ir.Node {
	n, err := pr.objIdxMayFail(idx, implicits, explicits, shaped)
	if err != nil {
		pr.gd.Fatalf("%v", err)
	}
	return n
}

// objIdxMayFail is equivalent to objIdx, but returns an error rather than
// failing the build if this object requires type arguments and the incorrect
// number of type arguments were passed.
//
// Other sources of internal failure (such as duplicate definitions) still fail
// the build.
func (pr *pkgReader) objIdxMayFail(idx index, implicits, explicits []*types.Type, shaped bool) (ir.Node, error) {
	rname := pr.newReader(pkgbits.SectionName, idx, pkgbits.SyncObject1)
	_, sym := rname.qualifiedIdent()
	tag := pkgbits.CodeObj(rname.Code(pkgbits.SyncCodeObj))

	if tag == pkgbits.ObjStub {
		assert(pr.gd, !sym.IsBlank())
		switch sym.Pkg {
		case types.BuiltinPkg(pr.gd), types.UnsafePkg(pr.gd):
			return sym.Def.(ir.Node), nil
		}
		if pri, ok := objReader(pr.gd)[sym]; ok {
			return pri.pr.objIdxMayFail(pri.idx, nil, explicits, shaped)
		}
		if sym.Pkg.Path == "runtime" {
			return typecheck.LookupRuntime(pr.gd, sym.Name), nil
		}
		pr.gd.Fatalf("unresolved stub: %v", sym)
	}

	dict, err := pr.objDictIdx(sym, idx, implicits, explicits, shaped)
	if err != nil {
		return nil, err
	}

	sym = dict.baseSym
	if !sym.IsBlank() && sym.Def != nil {
		return sym.Def.(*ir.Name), nil
	}

	r := pr.newReader(pkgbits.SectionObj, idx, pkgbits.SyncObject1)
	rext := pr.newReader(pkgbits.SectionObjExt, idx, pkgbits.SyncObject1)

	r.dict = dict
	rext.dict = dict

	do := func(op ir.Op, hasTParams bool) *ir.Name {
		pos := r.pos()
		setBasePos(pr.gd, pos)
		if hasTParams {
			r.typeParamNames()
		}

		name := ir.NewDeclNameAt(pr.gd, pos, op, sym)
		name.Class = ir.PEXTERN // may be overridden later
		if !sym.IsBlank() {
			if sym.Def != nil {
				pr.gd.FatalfAt(name.Pos(), "already have a definition for %v", name)
			}
			assert(pr.gd, sym.Def == nil)
			sym.Def = name
		}
		return name
	}

	switch tag {
	default:
		panic("unexpected object")

	case pkgbits.ObjAlias:
		name := do(ir.OTYPE, false)

		if r.Version().Has(pkgbits.AliasTypeParamNames) {
			r.typeParamNames()
		}

		// Clumsy dance: the r.typ() call here might recursively find this
		// type alias name, before we've set its type (#66873). So we
		// temporarily clear sym.Def and then restore it later, if still
		// unset.
		hack := sym.Def == name
		if hack {
			sym.Def = nil
		}
		typ := r.typ()
		if hack {
			if sym.Def != nil {
				name = sym.Def.(*ir.Name)
				assert(pr.gd, types.IdenticalStrict(name.Type(), typ))
				return name, nil
			}
			sym.Def = name
		}

		setType(name, typ)
		name.SetAlias(true)
		return name, nil

	case pkgbits.ObjConst:
		name := do(ir.OLITERAL, false)
		typ := r.typ()
		val := FixValue(pr.gd, typ, r.Value())
		setType(name, typ)
		setValue(name, val)
		return name, nil

	case pkgbits.ObjFunc:
		if sym.Name == "init" {
			sym = Renameinit(r.gd)
		}

		npos := r.pos()
		setBasePos(pr.gd, npos)
		r.typeParamNames()
		typ := r.signature(nil)
		fpos := r.pos()

		fn := ir.NewFunc(pr.gd, fpos, npos, sym, typ)
		name := fn.Nname
		if !sym.IsBlank() {
			if sym.Def != nil {
				pr.gd.FatalfAt(name.Pos(), "already have a definition for %v", name)
			}
			assert(pr.gd, sym.Def == nil)
			sym.Def = name
		}

		if r.hasTypeParams() {
			name.Func.SetDupok(true)
			if r.dict.shaped {
				setType(name, shapeSig(r.gd, name.Func, r.dict))
			} else {
				todoDictsAppend(r.gd, func() {
					r.dict.shapedObj = pr.objIdx(idx, implicits, explicits, true).(*ir.Name)
				})
			}
		}

		rext.funcExt(name, nil)
		return name, nil

	case pkgbits.ObjType:
		name := do(ir.OTYPE, true)
		typ := types.NewNamed(name)
		setType(name, typ)
		if r.hasTypeParams() && r.dict.shaped {
			typ.SetHasShape(true)
		}

		// Important: We need to do this before SetUnderlying.
		rext.typeExt(name)

		// We need to defer CheckSize until we've called SetUnderlying to
		// handle recursive types.
		types.DeferCheckSize()
		typ.SetUnderlying(r.typWrapped(false))
		types.ResumeCheckSize()

		if r.hasTypeParams() && !r.dict.shaped {
			todoDictsAppend(r.gd, func() {
				r.dict.shapedObj = pr.objIdx(idx, implicits, explicits, true).(*ir.Name)
			})
		}

		methods := make([]*types.Field, r.Len())
		for i := range methods {
			methods[i] = r.method(rext)
		}
		if len(methods) != 0 {
			typ.SetMethods(methods)
		}

		if !r.dict.shaped {
			r.needWrapper(typ)
		}

		return name, nil

	case pkgbits.ObjVar:
		name := do(ir.ONAME, false)
		setType(name, r.typ())
		rext.varExt(name)
		return name, nil
	}
}

func (dict *readerDict) mangle(sym *types.Sym) *types.Sym {
	if !dict.hasTypeParams() {
		return sym
	}

	// If sym is a locally defined generic type, we need the suffix to
	// stay at the end after mangling so that types/fmt.go can strip it
	// out again when writing the type's runtime descriptor (#54456).
	base, suffix := types.SplitVargenSuffix(sym.Name)

	var buf strings.Builder
	buf.WriteString(base)
	buf.WriteByte('[')
	for i, targ := range dict.targs {
		if i > 0 {
			if i == dict.implicits {
				buf.WriteByte(';')
			} else {
				buf.WriteByte(',')
			}
		}
		buf.WriteString(targ.LinkString())
	}
	buf.WriteByte(']')
	buf.WriteString(suffix)
	return sym.Pkg.Lookup(buf.String())
}

// shapify returns the shape type for targ.
//
// If basic is true, then the type argument is used to instantiate a
// type parameter whose constraint is a basic interface.
func shapify(gd *base.Invocation, targ *types.Type, basic bool) *types.Type {
	if targ.Kind() == types.TFORW {
		if targ.IsFullyInstantiated() {
			// For recursive instantiated type argument, it may  still be a TFORW
			// when shapifying happens. If we don't have targ's underlying type,
			// shapify won't work. The worst case is we end up not reusing code
			// optimally in some tricky cases.
			if gd.Debug.Shapify != 0 {
				gd.Warn("skipping shaping of recursive type %v", targ)
			}
			if targ.HasShape() {
				return targ
			}
		} else {
			gd.Fatalf("%v is missing its underlying type", targ)
		}
	}
	// For fully instantiated shape interface type, use it as-is. Otherwise, the instantiation
	// involved recursive generic interface may cause mismatching in function signature, see issue #65362.
	if targ.Kind() == types.TINTER && targ.IsFullyInstantiated() && targ.HasShape() {
		return targ
	}

	// When a pointer type is used to instantiate a type parameter
	// constrained by a basic interface, we know the pointer's element
	// type can't matter to the generated code. In this case, we can use
	// an arbitrary pointer type as the shape type. (To match the
	// non-unified frontend, we use `*byte`.)
	//
	// Otherwise, we simply use the type's underlying type as its shape.
	//
	// TODO(mdempsky): It should be possible to do much more aggressive
	// shaping still; e.g., collapsing all pointer-shaped types into a
	// common type, collapsing scalars of the same size/alignment into a
	// common type, recursively shaping the element types of composite
	// types, and discarding struct field names and tags. However, we'll
	// need to start tracking how type parameters are actually used to
	// implement some of these optimizations.
	pointerShaping := basic && targ.IsPtr() && !targ.Elem().NotInHeap()
	// The exception is when the type parameter is a pointer to a type
	// which `Type.HasShape()` returns true, but `Type.IsShape()` returns
	// false, like `*[]go.shape.T`. This is because the type parameter is
	// used to instantiate a generic function inside another generic function.
	// In this case, we want to keep the targ as-is, otherwise, we may lose the
	// original type after `*[]go.shape.T` is shapified to `*go.shape.uint8`.
	// See issue #54535, #71184.
	if pointerShaping && !targ.Elem().IsShape() && targ.Elem().HasShape() {
		return targ
	}
	under := targ.Underlying()
	if pointerShaping {
		under = types.NewPtr(types.Types[types.TUINT8])
	}

	// Hash long type names to bound symbol name length seen by users,
	// particularly for large protobuf structs (#65030).
	uls := under.LinkString()
	if gd.Debug.MaxShapeLen != 0 &&
		len(uls) > gd.Debug.MaxShapeLen {
		h := hash.Sum32([]byte(uls))
		uls = hex.EncodeToString(h[:])
	}

	sym := types.ShapePkg(gd).Lookup(uls)
	if sym.Def == nil {
		name := ir.NewDeclNameAt(gd, under.Pos(), ir.OTYPE, sym)
		typ := types.NewNamed(name)
		typ.SetUnderlying(under)
		sym.Def = typed(typ, name)
	}
	res := sym.Def.Type()
	assert(gd, res.IsShape())
	assert(gd, res.HasShape())
	return res
}

// objDictIdx reads and returns the specified object dictionary.
func (pr *pkgReader) objDictIdx(sym *types.Sym, idx index, implicits, explicits []*types.Type, shaped bool) (*readerDict, error) {
	r := pr.newReader(pkgbits.SectionObjDict, idx, pkgbits.SyncObject1)

	dict := readerDict{
		shaped: shaped,
	}

	nimplicits := r.Len()
	nexplicits := r.Len()

	if nimplicits > len(implicits) || nexplicits != len(explicits) {
		return nil, fmt.Errorf("%v has %v+%v params, but instantiated with %v+%v args", sym, nimplicits, nexplicits, len(implicits), len(explicits))
	}

	dict.targs = append(implicits[:nimplicits:nimplicits], explicits...)
	dict.implicits = nimplicits

	// Within the compiler, we can just skip over the type parameters.
	for range dict.targs[dict.implicits:] {
		// Skip past bounds without actually evaluating them.
		r.typInfo()
	}

	dict.derived = make([]derivedInfo, r.Len())
	dict.derivedTypes = make([]*types.Type, len(dict.derived))
	for i := range dict.derived {
		dict.derived[i] = derivedInfo{idx: r.Reloc(pkgbits.SectionType)}
		if r.Version().Has(pkgbits.DerivedInfoNeeded) {
			assert(pr.gd, !r.Bool())
		}
	}

	// Runtime dictionary information; private to the compiler.

	// If any type argument is already shaped, then we're constructing a
	// shaped object, even if not explicitly requested (i.e., calling
	// objIdx with shaped==true). This can happen with instantiating
	// types that are referenced within a function body.
	for _, targ := range dict.targs {
		if targ.HasShape() {
			dict.shaped = true
			break
		}
	}

	// And if we're constructing a shaped object, then shapify all type
	// arguments.
	for i, targ := range dict.targs {
		basic := r.Bool()
		if dict.shaped {
			dict.targs[i] = shapify(pr.gd, targ, basic)
		}
	}

	dict.baseSym = dict.mangle(sym)

	dict.typeParamMethodExprs = make([]readerMethodExprInfo, r.Len())
	for i := range dict.typeParamMethodExprs {
		typeParamIdx := r.Len()
		method := r.selector()

		dict.typeParamMethodExprs[i] = readerMethodExprInfo{typeParamIdx, method}
	}

	dict.subdicts = make([]objInfo, r.Len())
	for i := range dict.subdicts {
		dict.subdicts[i] = r.objInfo()
	}

	dict.rtypes = make([]typeInfo, r.Len())
	for i := range dict.rtypes {
		dict.rtypes[i] = r.typInfo()
	}

	dict.itabs = make([]itabInfo, r.Len())
	for i := range dict.itabs {
		dict.itabs[i] = itabInfo{typ: r.typInfo(), iface: r.typInfo()}
	}

	return &dict, nil
}

func (r *reader) typeParamNames() {
	r.Sync(pkgbits.SyncTypeParamNames)

	for range r.dict.targs[r.dict.implicits:] {
		r.pos()
		r.localIdent()
	}
}

func (r *reader) method(rext *reader) *types.Field {
	r.Sync(pkgbits.SyncMethod)
	npos := r.pos()
	sym := r.selector()
	r.typeParamNames()
	recv := r.param()
	typ := r.signature(recv)

	fpos := r.pos()
	fn := ir.NewFunc(r.gd, fpos, npos, ir.MethodSym(r.gd, recv.Type, sym), typ)
	name := fn.Nname

	if r.hasTypeParams() {
		name.Func.SetDupok(true)
		if r.dict.shaped {
			typ = shapeSig(r.gd, name.Func, r.dict)
			setType(name, typ)
		}
	}

	rext.funcExt(name, sym)

	meth := types.NewField(name.Func.Pos(), sym, typ)
	meth.Nname = name
	meth.SetNointerface(name.Func.Pragma&ir.Nointerface != 0)

	return meth
}

func (r *reader) qualifiedIdent() (pkg *types.Pkg, sym *types.Sym) {
	r.Sync(pkgbits.SyncSym)
	pkg = r.pkg()
	if name := r.String(); name != "" {
		sym = pkg.Lookup(name)
	}
	return
}

func (r *reader) localIdent() *types.Sym {
	r.Sync(pkgbits.SyncLocalIdent)
	pkg := r.pkg()
	if name := r.String(); name != "" {
		return pkg.Lookup(name)
	}
	return nil
}

func (r *reader) selector() *types.Sym {
	r.Sync(pkgbits.SyncSelector)
	pkg := r.pkg()
	name := r.String()
	if types.IsExported(name) {
		pkg = types.LocalPkg(r.gd)
	}
	return pkg.Lookup(name)
}

func (r *reader) hasTypeParams() bool {
	return r.dict.hasTypeParams()
}

func (dict *readerDict) hasTypeParams() bool {
	return dict != nil && len(dict.targs) != 0
}

// @@@ Compiler extensions

func (r *reader) funcExt(name *ir.Name, method *types.Sym) {
	r.Sync(pkgbits.SyncFuncExt)

	fn := name.Func

	// XXX: Workaround because linker doesn't know how to copy Pos.
	if !fn.Pos().IsKnown() {
		fn.SetPos(name.Pos())
	}

	// Normally, we only compile local functions, which saves redundant compilation work.
	// n.Defn is not nil for local functions, and is nil for imported function. But for
	// generic functions, we might have an instantiation that no other package has seen before.
	// So we need to be conservative and compile it again.
	//
	// That's why name.Defn is set here, so ir.VisitFuncsBottomUp can analyze function.
	// TODO(mdempsky,cuonglm): find a cleaner way to handle this.
	if name.Sym().Pkg == types.LocalPkg(r.gd) || r.hasTypeParams() {
		name.Defn = fn
	}

	fn.Pragma = r.pragmaFlag()

	r.linkname(name)

	if buildcfg.GOARCH == "wasm" {
		importmod := r.String()
		importname := r.String()
		exportname := r.String()

		if importmod != "" && importname != "" {
			fn.WasmImport = &ir.WasmImport{
				Module: importmod,
				Name:   importname,
			}
		}
		if exportname != "" {
			if method != nil {
				r.gd.ErrorfAt(fn.Pos(), 0, "cannot use //go:wasmexport on a method")
			}
			fn.WasmExport = &ir.WasmExport{Name: exportname}
		}
	}

	if r.Bool() {
		assert(r.gd, name.Defn == nil)

		fn.ABI = obj.ABI(r.Uint64())

		// Escape analysis. One note per RecvParam. If the
		// GdReturnOutBuf pragma was set above, RecvParams() already
		// has the outBufs appended — the writer wrote matching
		// N+outBufs notes. If the pragma is clear, RecvParams() is
		// stock and the writer wrote N notes.
		for _, f := range name.Type().RecvParams() {
			f.Note = r.String()
		}

		if r.Bool() {
			fn.Inl = &ir.Inline{
				Cost:            int32(r.Len()),
				CanDelayResults: r.Bool(),
			}
			if buildcfg.Experiment.NewInliner {
				fn.Inl.Properties = r.String()
			}
		}
	} else {
		r.addBody(name.Func, method)
	}
	r.Sync(pkgbits.SyncEOF)
}

func (r *reader) typeExt(name *ir.Name) {
	r.Sync(pkgbits.SyncTypeExt)

	typ := name.Type()

	if r.hasTypeParams() {
		// Mark type as fully instantiated to ensure the type descriptor is written
		// out as DUPOK and method wrappers are generated even for imported types.
		typ.SetIsFullyInstantiated(true)
		// HasShape should be set if any type argument is or has a shape type.
		for _, targ := range r.dict.targs {
			if targ.HasShape() {
				typ.SetHasShape(true)
				break
			}
		}
	}

	name.SetPragma(r.pragmaFlag())

	typecheck.SetBaseTypeIndex(r.gd, typ, r.Int64(), r.Int64())
}

func (r *reader) varExt(name *ir.Name) {
	r.Sync(pkgbits.SyncVarExt)
	r.linkname(name)
}

func (r *reader) linkname(name *ir.Name) {
	assert(r.gd, name.Op() == ir.ONAME)
	r.Sync(pkgbits.SyncLinkname)

	if idx := r.Int64(); idx >= 0 {
		lsym := name.Linksym()
		lsym.SymIdx = int32(idx)
		lsym.Set(obj.AttrIndexed, true)
	} else {
		linkname := r.String()
		sym := name.Sym()
		sym.Linkname = linkname
		if sym.Pkg == types.LocalPkg(r.gd) && linkname != "" {
			// Mark linkname in the current package. We don't mark the
			// ones that are imported and propagated (e.g. through
			// inlining or instantiation, which are marked in their
			// corresponding packages). So we can tell in which package
			// the linkname is used (pulled), and the linker can
			// make a decision for allowing or disallowing it.
			sym.Linksym(r.gd).Set(obj.AttrLinkname, true)
		}
	}
}

func (r *reader) pragmaFlag() ir.PragmaFlag {
	r.Sync(pkgbits.SyncPragma)
	return ir.PragmaFlag(r.Int())
}

// @@@ Function bodies

// bodyReader tracks where the serialized IR for a local or imported,
// generic function's body can be found. Per-Invocation; see objReader.
func bodyReader(gd *base.Invocation) map[*ir.Func]pkgReaderIndex {
	m, _ := gd.NoderBodyReader.(map[*ir.Func]pkgReaderIndex)
	if m == nil {
		m = map[*ir.Func]pkgReaderIndex{}
		gd.NoderBodyReader = m
	}
	return m
}

// importBodyReader tracks where the serialized IR for an imported,
// static (i.e., non-generic) function body can be read.
func importBodyReader(gd *base.Invocation) map[*types.Sym]pkgReaderIndex {
	m, _ := gd.NoderImportBodyReader.(map[*types.Sym]pkgReaderIndex)
	if m == nil {
		m = map[*types.Sym]pkgReaderIndex{}
		gd.NoderImportBodyReader = m
	}
	return m
}

// bodyReaderFor returns the pkgReaderIndex for reading fn's
// serialized IR, and whether one was found.
func bodyReaderFor(gd *base.Invocation, fn *ir.Func) (pri pkgReaderIndex, ok bool) {
	if fn.Nname.Defn != nil {
		pri, ok = bodyReader(gd)[fn]
		gd.AssertfAt(ok, gd.Pos, "must have bodyReader for %v", fn) // must always be available
	} else {
		pri, ok = importBodyReader(gd)[fn.Sym()]
	}
	return
}

// todoDicts / todoBodies hold lists of dictionaries and function bodies
// that still need to be constructed during a readBodies pass. Both live
// on *base.Invocation so concurrent / sequential compile invocations
// don't share them.

func todoDictsAppend(gd *base.Invocation, f func()) {
	td, _ := gd.NoderTodoDicts.([]func())
	gd.NoderTodoDicts = append(td, f)
}

func todoBodiesAppend(gd *base.Invocation, fn *ir.Func) {
	tb, _ := gd.NoderTodoBodies.([]*ir.Func)
	gd.NoderTodoBodies = append(tb, fn)
}

// addBody reads a function body reference from the element bitstream,
// and associates it with fn.
func (r *reader) addBody(fn *ir.Func, method *types.Sym) {
	// addBody should only be called for local functions or imported
	// generic functions; see comment in funcExt.
	assert(r.gd, fn.Nname.Defn != nil)

	idx := r.Reloc(pkgbits.SectionBody)

	pri := pkgReaderIndex{r.p, idx, r.dict, method, r.gd, nil}
	bodyReader(r.gd)[fn] = pri

	if r.curfn == nil {
		todoBodiesAppend(r.gd, fn)
		return
	}

	pri.funcBody(fn)
}

func (pri pkgReaderIndex) funcBody(fn *ir.Func) {
	r := pri.asReader(pkgbits.SectionBody, pkgbits.SyncFuncBody)
	r.funcBody(fn)
}

// funcBody reads a function body definition from the element
// bitstream, and populates fn with it.
func (r *reader) funcBody(fn *ir.Func) {
	r.curfn = fn
	r.closureVars = fn.ClosureVars
	if len(r.closureVars) != 0 && r.hasTypeParams() {
		r.dictParam = r.closureVars[len(r.closureVars)-1] // dictParam is last; see reader.funcLit
	}

	ir.WithFunc(r.gd, fn, func() {
		r.declareParams()

		if r.syntheticBody(fn.Pos()) {
			return
		}

		if !r.Bool() {
			return
		}

		body := r.stmts()
		if body == nil {
			body = []ir.Node{typecheck.Stmt(r.gd, ir.NewBlockStmt(r.gd, src.NoXPos, nil))}
		}
		fn.Body = body
		fn.Endlineno = r.pos()
	})

	r.marker.WriteTo(fn)
}

// syntheticBody adds a synthetic body to r.curfn if appropriate, and
// reports whether it did.
func (r *reader) syntheticBody(pos src.XPos) bool {
	if r.synthetic != nil {
		r.synthetic(pos, r)
		return true
	}

	// If this function has type parameters and isn't shaped, then we
	// just tail call its corresponding shaped variant.
	if r.hasTypeParams() && !r.dict.shaped {
		r.callShaped(pos)
		return true
	}

	return false
}

// callShaped emits a tail call to r.shapedFn, passing along the
// arguments to the current function.
func (r *reader) callShaped(pos src.XPos) {
	shapedObj := r.dict.shapedObj
	assert(r.gd, shapedObj != nil)

	var shapedFn ir.Node
	if r.methodSym == nil {
		// Instantiating a generic function; shapedObj is the shaped
		// function itself.
		assert(r.gd, shapedObj.Op() == ir.ONAME && shapedObj.Class == ir.PFUNC)
		shapedFn = shapedObj
	} else {
		// Instantiating a generic type's method; shapedObj is the shaped
		// type, so we need to select it's corresponding method.
		shapedFn = shapedMethodExpr(r.gd, pos, shapedObj, r.methodSym)
	}

	params := r.syntheticArgs()

	// Construct the arguments list: receiver (if any), then runtime
	// dictionary, and finally normal parameters.
	//
	// Note: For simplicity, shaped methods are added as normal methods
	// on their shaped types. So existing code (e.g., packages ir and
	// typecheck) expects the shaped type to appear as the receiver
	// parameter (or first parameter, as a method expression). Hence
	// putting the dictionary parameter after that is the least invasive
	// solution at the moment.
	var args ir.Nodes
	if r.methodSym != nil {
		args.Append(params[0])
		params = params[1:]
	}
	args.Append(typecheck.Expr(r.gd, ir.NewAddrExpr(r.gd, pos, r.p.dictNameOf(r.dict))))
	args.Append(params...)

	r.syntheticTailCall(pos, shapedFn, args)
}

// syntheticArgs returns the recvs and params arguments passed to the
// current function. Phase G.2.1 outBuf params are filtered out — they
// belong to the *current* function's extended ABI; the destination's
// FillOutBufArgs in tcCall will re-append nils matching the
// destination's outBuf count, which may differ from r.curfn's.
func (r *reader) syntheticArgs() ir.Nodes {
	sig := r.curfn.Nname.Type()
	all := r.curfn.Dcl[:sig.NumRecvs()+sig.NumParams()]
	if sig.NumOutBufs() == 0 {
		return ir.ToNodes(all)
	}
	out := make([]*ir.Name, 0, len(all))
	nRecv := sig.NumRecvs()
	for i, n := range all {
		if i >= nRecv {
			p := sig.Params()[i-nRecv]
			if p.IsOutBufParam() {
				continue
			}
		}
		out = append(out, n)
	}
	return ir.ToNodes(out)
}

// syntheticTailCall emits a tail call to fn, passing the given
// arguments list.
func (r *reader) syntheticTailCall(pos src.XPos, fn ir.Node, args ir.Nodes) {
	// Mark the function as a wrapper so it doesn't show up in stack
	// traces.
	r.curfn.SetWrapper(true)

	call := typecheck.Call(r.gd, pos, fn, args, fn.Type().IsVariadic()).(*ir.CallExpr)

	var stmt ir.Node
	if fn.Type().NumResults() != 0 {
		stmt = typecheck.Stmt(r.gd, ir.NewReturnStmt(r.gd, pos, []ir.Node{call}))
	} else {
		stmt = call
	}
	r.curfn.Body.Append(stmt)
}

// dictNameOf returns the runtime dictionary corresponding to dict.
func (pr *pkgReader) dictNameOf(dict *readerDict) *ir.Name {
	pos := pr.gd.AutogeneratedPos

	// Check that we only instantiate runtime dictionaries with real types.
	pr.gd.AssertfAt(!dict.shaped, pos, "runtime dictionary of shaped object %v", dict.baseSym)

	sym := dict.baseSym.Pkg.Lookup(objabi.GlobalDictPrefix + "." + dict.baseSym.Name)
	if sym.Def != nil {
		return sym.Def.(*ir.Name)
	}

	name := ir.NewNameAt(pr.gd, pos, sym, dict.varType())
	name.Class = ir.PEXTERN
	sym.Def = name // break cycles with mutual subdictionaries

	lsym := name.Linksym()
	ot := 0

	assertOffset := func(section string, offset int) {
		pr.gd.AssertfAt(ot == offset*types.PtrSize, pos, "writing section %v at offset %v, but it should be at %v*%v", section, ot, offset, types.PtrSize)
	}

	assertOffset("type param method exprs", dict.typeParamMethodExprsOffset())
	for _, info := range dict.typeParamMethodExprs {
		typeParam := dict.targs[info.typeParamIdx]
		method := typecheck.NewMethodExpr(pr.gd, pos, typeParam, info.method)

		rsym := method.FuncName(pr.gd).Linksym()
		assert(pr.gd, rsym.ABI() == obj.ABIInternal) // must be ABIInternal; see ir.OCFUNC in ssagen/ssa.go

		ot = objw.SymPtr(pr.gd, lsym, ot, rsym, 0)
	}

	assertOffset("subdictionaries", dict.subdictsOffset())
	for _, info := range dict.subdicts {
		explicits := pr.typListIdx(info.explicits, dict)

		// Careful: Due to subdictionary cycles, name may not be fully
		// initialized yet.
		name := pr.objDictName(info.idx, dict.targs, explicits)

		ot = objw.SymPtr(pr.gd, lsym, ot, name.Linksym(), 0)
	}

	assertOffset("rtypes", dict.rtypesOffset())
	for _, info := range dict.rtypes {
		typ := pr.typIdx(info, dict, true)
		ot = objw.SymPtr(pr.gd, lsym, ot, reflectdata.TypeLinksym(pr.gd, typ), 0)

		// TODO(mdempsky): Double check this.
		reflectdata.MarkTypeUsedInInterface(pr.gd, typ, lsym)
	}

	// For each (typ, iface) pair, we write the *runtime.itab pointer
	// for the pair. For pairs that don't actually require an itab
	// (i.e., typ is an interface, or iface is an empty interface), we
	// write a nil pointer instead. This is wasteful, but rare in
	// practice (e.g., instantiating a type parameter with an interface
	// type).
	assertOffset("itabs", dict.itabsOffset())
	for _, info := range dict.itabs {
		typ := pr.typIdx(info.typ, dict, true)
		iface := pr.typIdx(info.iface, dict, true)

		if !typ.IsInterface() && iface.IsInterface() && !iface.IsEmptyInterface() {
			ot = objw.SymPtr(pr.gd, lsym, ot, reflectdata.ITabLsym(pr.gd, typ, iface), 0)
		} else {
			ot += types.PtrSize
		}

		// TODO(mdempsky): Double check this.
		reflectdata.MarkTypeUsedInInterface(pr.gd, typ, lsym)
		reflectdata.MarkTypeUsedInInterface(pr.gd, iface, lsym)
	}

	objw.Global(pr.gd, lsym, int32(ot), obj.DUPOK|obj.RODATA)

	return name
}

// typeParamMethodExprsOffset returns the offset of the runtime
// dictionary's type parameter method expressions section, in words.
func (dict *readerDict) typeParamMethodExprsOffset() int {
	return 0
}

// subdictsOffset returns the offset of the runtime dictionary's
// subdictionary section, in words.
func (dict *readerDict) subdictsOffset() int {
	return dict.typeParamMethodExprsOffset() + len(dict.typeParamMethodExprs)
}

// rtypesOffset returns the offset of the runtime dictionary's rtypes
// section, in words.
func (dict *readerDict) rtypesOffset() int {
	return dict.subdictsOffset() + len(dict.subdicts)
}

// itabsOffset returns the offset of the runtime dictionary's itabs
// section, in words.
func (dict *readerDict) itabsOffset() int {
	return dict.rtypesOffset() + len(dict.rtypes)
}

// numWords returns the total number of words that comprise dict's
// runtime dictionary variable.
func (dict *readerDict) numWords() int64 {
	return int64(dict.itabsOffset() + len(dict.itabs))
}

// varType returns the type of dict's runtime dictionary variable.
func (dict *readerDict) varType() *types.Type {
	return types.NewArray(types.Types[types.TUINTPTR], dict.numWords())
}

func (r *reader) declareParams() {
	r.curfn.DeclareParams(r.gd, !r.funarghack)

	for _, name := range r.curfn.Dcl {
		if name.Sym().Name == dictParamName {
			r.dictParam = name
			continue
		}
		// gd Phase G: synthesised .outBufK params don't have a
		// writer-side addLocal entry (the writer serialises from
		// types2 which has no outBufs). Skip them here so the
		// sync-marker stream stays aligned with the writer.
		if strings.HasPrefix(name.Sym().Name, types.OutBufNamePrefix) {
			continue
		}

		r.addLocal(name)
	}
}

func (r *reader) addLocal(name *ir.Name) {
	if r.synthetic == nil {
		r.Sync(pkgbits.SyncAddLocal)
		if r.p.SyncMarkers() {
			want := r.Int()
			if have := len(r.locals); have != want {
				r.gd.FatalfAt(name.Pos(), "locals table has desynced")
			}
		}
		r.varDictIndex(name)
	}

	r.locals = append(r.locals, name)
}

func (r *reader) useLocal() *ir.Name {
	r.Sync(pkgbits.SyncUseObjLocal)
	if r.Bool() {
		return r.locals[r.Len()]
	}
	return r.closureVars[r.Len()]
}

func (r *reader) openScope() {
	r.Sync(pkgbits.SyncOpenScope)
	pos := r.pos()

	if r.gd.Flag.Dwarf {
		r.scopeVars = append(r.scopeVars, len(r.curfn.Dcl))
		r.marker.Push(r.gd, pos)
	}
}

func (r *reader) closeScope() {
	r.Sync(pkgbits.SyncCloseScope)
	r.lastCloseScopePos = r.pos()

	r.closeAnotherScope()
}

// closeAnotherScope is like closeScope, but it reuses the same mark
// position as the last closeScope call. This is useful for "for" and
// "if" statements, as their implicit blocks always end at the same
// position as an explicit block.
func (r *reader) closeAnotherScope() {
	r.Sync(pkgbits.SyncCloseAnotherScope)

	if r.gd.Flag.Dwarf {
		scopeVars := r.scopeVars[len(r.scopeVars)-1]
		r.scopeVars = r.scopeVars[:len(r.scopeVars)-1]

		// Quirkish: noder decides which scopes to keep before
		// typechecking, whereas incremental typechecking during IR
		// construction can result in new autotemps being allocated. To
		// produce identical output, we ignore autotemps here for the
		// purpose of deciding whether to retract the scope.
		//
		// This is important for net/http/fcgi, because it contains:
		//
		//	var body io.ReadCloser
		//	if len(content) > 0 {
		//		body, req.pw = io.Pipe()
		//	} else { … }
		//
		// Notably, io.Pipe is inlinable, and inlining it introduces a ~R0
		// variable at the call site.
		//
		// Noder does not preserve the scope where the io.Pipe() call
		// resides, because it doesn't contain any declared variables in
		// source. So the ~R0 variable ends up being assigned to the
		// enclosing scope instead.
		//
		// However, typechecking this assignment also introduces
		// autotemps, because io.Pipe's results need conversion before
		// they can be assigned to their respective destination variables.
		//
		// TODO(mdempsky): We should probably just keep all scopes, and
		// let dwarfgen take care of pruning them instead.
		retract := true
		for _, n := range r.curfn.Dcl[scopeVars:] {
			if !n.AutoTemp() {
				retract = false
				break
			}
		}

		if retract {
			// no variables were declared in this scope, so we can retract it.
			r.marker.Unpush(r.gd)
		} else {
			r.marker.Pop(r.gd, r.lastCloseScopePos)
		}
	}
}

// @@@ Statements

func (r *reader) stmt() ir.Node {
	return block(r.gd, r.stmts())
}

func block(gd *base.Invocation, stmts []ir.Node) ir.Node {
	switch len(stmts) {
	case 0:
		return nil
	case 1:
		return stmts[0]
	default:
		return ir.NewBlockStmt(gd, stmts[0].Pos(), stmts)
	}
}

func (r *reader) stmts() ir.Nodes {
	assert(r.gd, r.gd.CurFunc == r.curfn)
	var res ir.Nodes

	r.Sync(pkgbits.SyncStmts)
	for {
		tag := codeStmt(r.Code(pkgbits.SyncStmt1))
		if tag == stmtEnd {
			r.Sync(pkgbits.SyncStmtsEnd)
			return res
		}

		if n := r.stmt1(tag, &res); n != nil {
			res.Append(typecheck.Stmt(r.gd, n))
		}
	}
}

func (r *reader) stmt1(tag codeStmt, out *ir.Nodes) ir.Node {
	var label *types.Sym
	if n := len(*out); n > 0 {
		if ls, ok := (*out)[n-1].(*ir.LabelStmt); ok {
			label = ls.Label
		}
	}

	switch tag {
	default:
		panic("unexpected statement")

	case stmtAssign:
		pos := r.pos()
		names, lhs := r.assignList()
		rhs := r.multiExpr()

		if len(rhs) == 0 {
			for _, name := range names {
				as := ir.NewAssignStmt(r.gd, pos, name, nil)
				as.PtrInit().Append(ir.NewDecl(r.gd, pos, ir.ODCL, name))
				out.Append(typecheck.Stmt(r.gd, as))
			}
			return nil
		}

		if len(lhs) == 1 && len(rhs) == 1 {
			n := ir.NewAssignStmt(r.gd, pos, lhs[0], rhs[0])
			n.Def = r.initDefn(n, names)
			return n
		}

		n := ir.NewAssignListStmt(r.gd, pos, ir.OAS2, lhs, rhs)
		n.Def = r.initDefn(n, names)
		return n

	case stmtAssignOp:
		op := r.op()
		lhs := r.expr()
		pos := r.pos()
		rhs := r.expr()
		return ir.NewAssignOpStmt(r.gd, pos, op, lhs, rhs)

	case stmtIncDec:
		op := r.op()
		lhs := r.expr()
		pos := r.pos()
		n := ir.NewAssignOpStmt(r.gd, pos, op, lhs, ir.NewOne(r.gd, pos, lhs.Type()))
		n.IncDec = true
		return n

	case stmtBlock:
		out.Append(r.blockStmt()...)
		return nil

	case stmtBranch:
		pos := r.pos()
		op := r.op()
		sym := r.optLabel()
		return ir.NewBranchStmt(r.gd, pos, op, sym)

	case stmtCall:
		pos := r.pos()
		op := r.op()
		call := r.expr()
		stmt := ir.NewGoDeferStmt(r.gd, pos, op, call)
		if op == ir.ODEFER {
			x := r.optExpr()
			if x != nil {
				stmt.DeferAt = x.(ir.Expr)
			}
		}
		return stmt

	case stmtExpr:
		return r.expr()

	case stmtFor:
		return r.forStmt(label)

	case stmtIf:
		return r.ifStmt()

	case stmtLabel:
		pos := r.pos()
		sym := r.label()
		return ir.NewLabelStmt(r.gd, pos, sym)

	case stmtReturn:
		pos := r.pos()
		results := r.multiExpr()
		return ir.NewReturnStmt(r.gd, pos, results)

	case stmtSelect:
		return r.selectStmt(label)

	case stmtSend:
		pos := r.pos()
		ch := r.expr()
		value := r.expr()
		return ir.NewSendStmt(r.gd, pos, ch, value)

	case stmtSwitch:
		return r.switchStmt(label)
	}
}

func (r *reader) assignList() ([]*ir.Name, []ir.Node) {
	lhs := make([]ir.Node, r.Len())
	var names []*ir.Name

	for i := range lhs {
		expr, def := r.assign()
		lhs[i] = expr
		if def {
			names = append(names, expr.(*ir.Name))
		}
	}

	return names, lhs
}

// assign returns an assignee expression. It also reports whether the
// returned expression is a newly declared variable.
func (r *reader) assign() (ir.Node, bool) {
	switch tag := codeAssign(r.Code(pkgbits.SyncAssign)); tag {
	default:
		panic("unhandled assignee expression")

	case assignBlank:
		return typecheck.AssignExpr(r.gd, ir.BlankNode), false

	case assignDef:
		pos := r.pos()
		setBasePos(r.gd, pos) // test/fixedbugs/issue49767.go depends on r.gd.Pos being set for the r.typ() call here, ugh
		name := r.curfn.NewLocal(r.gd, pos, r.localIdent(), r.typ())
		r.addLocal(name)
		return name, true

	case assignExpr:
		return r.expr(), false
	}
}

func (r *reader) blockStmt() []ir.Node {
	r.Sync(pkgbits.SyncBlockStmt)
	r.openScope()
	stmts := r.stmts()
	r.closeScope()
	return stmts
}

func (r *reader) forStmt(label *types.Sym) ir.Node {
	r.Sync(pkgbits.SyncForStmt)

	r.openScope()

	if r.Bool() {
		pos := r.pos()
		rang := ir.NewRangeStmt(r.gd, pos, nil, nil, nil, nil, false)
		rang.Label = label

		names, lhs := r.assignList()
		if len(lhs) >= 1 {
			rang.Key = lhs[0]
			if len(lhs) >= 2 {
				rang.Value = lhs[1]
			}
		}
		rang.Def = r.initDefn(rang, names)

		rang.X = r.expr()
		if rang.X.Type().IsMap() {
			rang.RType = r.rtype(pos)
		}
		if rang.Key != nil && !ir.IsBlank(rang.Key) {
			rang.KeyTypeWord, rang.KeySrcRType = r.convRTTI(pos)
		}
		if rang.Value != nil && !ir.IsBlank(rang.Value) {
			rang.ValueTypeWord, rang.ValueSrcRType = r.convRTTI(pos)
		}

		rang.Body = r.blockStmt()
		rang.DistinctVars = r.Bool()
		r.closeAnotherScope()

		return rang
	}

	pos := r.pos()
	init := r.stmt()
	cond := r.optExpr()
	post := r.stmt()
	body := r.blockStmt()
	perLoopVars := r.Bool()
	r.closeAnotherScope()

	if ir.IsConst(cond, constant.Bool) && !ir.BoolVal(cond) {
		return init // simplify "for init; false; post { ... }" into "init"
	}

	stmt := ir.NewForStmt(r.gd, pos, init, cond, post, body, perLoopVars)
	stmt.Label = label
	return stmt
}

func (r *reader) ifStmt() ir.Node {
	r.Sync(pkgbits.SyncIfStmt)
	r.openScope()
	pos := r.pos()
	init := r.stmts()
	cond := r.expr()
	staticCond := r.Int()
	var then, els []ir.Node
	if staticCond >= 0 {
		then = r.blockStmt()
	} else {
		r.lastCloseScopePos = r.pos()
	}
	if staticCond <= 0 {
		els = r.stmts()
	}
	r.closeAnotherScope()

	if staticCond != 0 {
		// We may have removed a dead return statement, which can trip up
		// later passes (#62211). To avoid confusion, we instead flatten
		// the if statement into a block.

		if cond.Op() != ir.OLITERAL {
			init.Append(typecheck.Stmt(r.gd, ir.NewAssignStmt(r.gd, pos, ir.BlankNode, cond))) // for side effects
		}
		init.Append(then...)
		init.Append(els...)
		return block(r.gd, init)
	}

	n := ir.NewIfStmt(r.gd, pos, cond, then, els)
	n.SetInit(init)
	return n
}

func (r *reader) selectStmt(label *types.Sym) ir.Node {
	r.Sync(pkgbits.SyncSelectStmt)

	pos := r.pos()
	clauses := make([]*ir.CommClause, r.Len())
	for i := range clauses {
		if i > 0 {
			r.closeScope()
		}
		r.openScope()

		pos := r.pos()
		comm := r.stmt()
		body := r.stmts()

		// "case i = <-c: ..." may require an implicit conversion (e.g.,
		// see fixedbugs/bug312.go). Currently, typecheck throws away the
		// implicit conversion and relies on it being reinserted later,
		// but that would lose any explicit RTTI operands too. To preserve
		// RTTI, we rewrite this as "case tmp := <-c: i = tmp; ...".
		if as, ok := comm.(*ir.AssignStmt); ok && as.Op() == ir.OAS && !as.Def {
			if conv, ok := as.Y.(*ir.ConvExpr); ok && conv.Op() == ir.OCONVIFACE {
				r.gd.AssertfAt(conv.Implicit(), conv.Pos(), "expected implicit conversion: %v", conv)

				recv := conv.X
				r.gd.AssertfAt(recv.Op() == ir.ORECV, recv.Pos(), "expected receive expression: %v", recv)

				tmp := r.temp(pos, recv.Type())

				// Replace comm with `tmp := <-c`.
				tmpAs := ir.NewAssignStmt(r.gd, pos, tmp, recv)
				tmpAs.Def = true
				tmpAs.PtrInit().Append(ir.NewDecl(r.gd, pos, ir.ODCL, tmp))
				comm = tmpAs

				// Change original assignment to `i = tmp`, and prepend to body.
				conv.X = tmp
				body = append([]ir.Node{as}, body...)
			}
		}

		// multiExpr will have desugared a comma-ok receive expression
		// into a separate statement. However, the rest of the compiler
		// expects comm to be the OAS2RECV statement itself, so we need to
		// shuffle things around to fit that pattern.
		if as2, ok := comm.(*ir.AssignListStmt); ok && as2.Op() == ir.OAS2 {
			init := ir.TakeInit(as2.Rhs[0])
			r.gd.AssertfAt(len(init) == 1 && init[0].Op() == ir.OAS2RECV, as2.Pos(), "unexpected assignment: %+v", as2)

			comm = init[0]
			body = append([]ir.Node{as2}, body...)
		}

		clauses[i] = ir.NewCommStmt(r.gd, pos, comm, body)
	}
	if len(clauses) > 0 {
		r.closeScope()
	}
	n := ir.NewSelectStmt(r.gd, pos, clauses)
	n.Label = label
	return n
}

func (r *reader) switchStmt(label *types.Sym) ir.Node {
	r.Sync(pkgbits.SyncSwitchStmt)

	r.openScope()
	pos := r.pos()
	init := r.stmt()

	var tag ir.Node
	var ident *ir.Ident
	var iface *types.Type
	if r.Bool() {
		pos := r.pos()
		if r.Bool() {
			ident = ir.NewIdent(r.pos(), r.localIdent())
		}
		x := r.expr()
		iface = x.Type()
		tag = ir.NewTypeSwitchGuard(r.gd, pos, ident, x)
	} else {
		tag = r.optExpr()
	}

	clauses := make([]*ir.CaseClause, r.Len())
	for i := range clauses {
		if i > 0 {
			r.closeScope()
		}
		r.openScope()

		pos := r.pos()
		var cases, rtypes []ir.Node
		if iface != nil {
			cases = make([]ir.Node, r.Len())
			if len(cases) == 0 {
				cases = nil // TODO(mdempsky): Unclear if this matters.
			}
			for i := range cases {
				if r.Bool() { // case nil
					cases[i] = typecheck.Expr(r.gd, types.BuiltinPkg(r.gd).Lookup("nil").Def.(*ir.NilExpr))
				} else {
					cases[i] = r.exprType()
				}
			}
		} else {
			cases = r.exprList()

			// For `switch { case any(true): }` (e.g., issue 3980 in
			// test/switch.go), the backend still creates a mixed bool/any
			// comparison, and we need to explicitly supply the RTTI for the
			// comparison.
			//
			// TODO(mdempsky): Change writer.go to desugar "switch {" into
			// "switch true {", which we already handle correctly.
			if tag == nil {
				for i, cas := range cases {
					if cas.Type().IsEmptyInterface() {
						for len(rtypes) < i {
							rtypes = append(rtypes, nil)
						}
						rtypes = append(rtypes, reflectdata.TypePtrAt(r.gd, cas.Pos(), types.Types[types.TBOOL]))
					}
				}
			}
		}

		clause := ir.NewCaseStmt(r.gd, pos, cases, nil)
		clause.RTypes = rtypes

		if ident != nil {
			name := r.curfn.NewLocal(r.gd, r.pos(), ident.Sym(), r.typ())
			r.addLocal(name)
			clause.Var = name
			name.Defn = tag
		}

		clause.Body = r.stmts()
		clauses[i] = clause
	}
	if len(clauses) > 0 {
		r.closeScope()
	}
	r.closeScope()

	n := ir.NewSwitchStmt(r.gd, pos, tag, clauses)
	n.Label = label
	if init != nil {
		n.SetInit([]ir.Node{init})
	}
	return n
}

func (r *reader) label() *types.Sym {
	r.Sync(pkgbits.SyncLabel)
	name := r.String()
	if r.inlCall != nil && name != "_" {
		name = fmt.Sprintf("~%s·%d", name, r.gd.NoderInlgen)
	}
	return typecheck.Lookup(r.gd, name)
}

func (r *reader) optLabel() *types.Sym {
	r.Sync(pkgbits.SyncOptLabel)
	if r.Bool() {
		return r.label()
	}
	return nil
}

// initDefn marks the given names as declared by defn and populates
// its Init field with ODCL nodes. It then reports whether any names
// were so declared, which can be used to initialize defn.Def.
func (r *reader) initDefn(defn ir.InitNode, names []*ir.Name) bool {
	if len(names) == 0 {
		return false
	}

	init := make([]ir.Node, len(names))
	for i, name := range names {
		name.Defn = defn
		init[i] = ir.NewDecl(r.gd, name.Pos(), ir.ODCL, name)
	}
	defn.SetInit(init)
	return true
}

// @@@ Expressions

// expr reads and returns a typechecked expression.
func (r *reader) expr() (res ir.Node) {
	defer func() {
		if res != nil && res.Typecheck() == 0 {
			r.gd.FatalfAt(res.Pos(), "%v missed typecheck", res)
		}
	}()

	switch tag := codeExpr(r.Code(pkgbits.SyncExpr)); tag {
	default:
		panic("unhandled expression")

	case exprLocal:
		return typecheck.Expr(r.gd, r.useLocal())

	case exprGlobal:
		// Callee instead of Expr allows builtins
		// TODO(mdempsky): Handle builtins directly in exprCall, like method calls?
		return typecheck.Callee(r.gd, r.obj())

	case exprFuncInst:
		origPos, pos := r.origPos()
		wrapperFn, baseFn, dictPtr := r.funcInst(pos)
		if wrapperFn != nil {
			return wrapperFn
		}
		return r.curry(origPos, false, baseFn, dictPtr, nil)

	case exprConst:
		pos := r.pos()
		typ := r.typ()
		val := FixValue(r.gd, typ, r.Value())
		return ir.NewBasicLit(r.gd, pos, typ, val)

	case exprZero:
		pos := r.pos()
		typ := r.typ()
		return ir.NewZero(r.gd, pos, typ)

	case exprCompLit:
		return r.compLit()

	case exprFuncLit:
		return r.funcLit()

	case exprFieldVal:
		x := r.expr()
		pos := r.pos()
		sym := r.selector()

		return typecheck.XDotField(r.gd, pos, x, sym)

	case exprMethodVal:
		recv := r.expr()
		origPos, pos := r.origPos()
		wrapperFn, baseFn, dictPtr := r.methodExpr()

		// For simple wrapperFn values, the existing machinery for creating
		// and deduplicating wrapperFn value wrappers still works fine.
		if wrapperFn, ok := wrapperFn.(*ir.SelectorExpr); ok && wrapperFn.Op() == ir.OMETHEXPR {
			// The receiver expression we constructed may have a shape type.
			// For example, in fixedbugs/issue54343.go, `New[int]()` is
			// constructed as `New[go.shape.int](&.dict.New[int])`, which
			// has type `*T[go.shape.int]`, not `*T[int]`.
			//
			// However, the method we want to select here is `(*T[int]).M`,
			// not `(*T[go.shape.int]).M`, so we need to manually convert
			// the type back so that the OXDOT resolves correctly.
			//
			// TODO(mdempsky): Logically it might make more sense for
			// exprCall to take responsibility for setting a non-shaped
			// result type, but this is the only place where we care
			// currently. And only because existing ir.OMETHVALUE backend
			// code relies on n.X.Type() instead of n.Selection.Recv().Type
			// (because the latter is types.FakeRecvType() in the case of
			// interface method values).
			//
			if recv.Type().HasShape() {
				typ := wrapperFn.Type().Param(0).Type
				if !types.Identical(typ, recv.Type()) {
					r.gd.FatalfAt(wrapperFn.Pos(), "receiver %L does not match %L", recv, wrapperFn)
				}
				recv = typecheck.Expr(r.gd, ir.NewConvExpr(r.gd, recv.Pos(), ir.OCONVNOP, typ, recv))
			}

			n := typecheck.XDotMethod(r.gd, pos, recv, wrapperFn.Sel, false)

			// As a consistency check here, we make sure "n" selected the
			// same method (represented by a types.Field) that wrapperFn
			// selected. However, for anonymous receiver types, there can be
			// multiple such types.Field instances (#58563). So we may need
			// to fallback to making sure Sym and Type (including the
			// receiver parameter's type) match.
			if n.Selection != wrapperFn.Selection {
				assert(r.gd, n.Selection.Sym == wrapperFn.Selection.Sym)
				assert(r.gd, types.Identical(n.Selection.Type, wrapperFn.Selection.Type))
				assert(r.gd, types.Identical(n.Selection.Type.Recv().Type, wrapperFn.Selection.Type.Recv().Type))
			}

			wrapper := methodValueWrapper{
				rcvr:   n.X.Type(),
				method: n.Selection,
			}

			if r.importedDef() {
				appendHaveMethodValueWrapper(r.gd, wrapper)
			} else {
				appendNeedMethodValueWrapper(r.gd, wrapper)
			}
			return n
		}

		// For more complicated method expressions, we construct a
		// function literal wrapper.
		return r.curry(origPos, true, baseFn, recv, dictPtr)

	case exprMethodExpr:
		recv := r.typ()

		implicits := make([]int, r.Len())
		for i := range implicits {
			implicits[i] = r.Len()
		}
		var deref, addr bool
		if r.Bool() {
			deref = true
		} else if r.Bool() {
			addr = true
		}

		origPos, pos := r.origPos()
		wrapperFn, baseFn, dictPtr := r.methodExpr()

		// If we already have a wrapper and don't need to do anything with
		// it, we can just return the wrapper directly.
		//
		// N.B., we use implicits/deref/addr here as the source of truth
		// rather than types.Identical, because the latter can be confused
		// by tricky promoted methods (e.g., typeparam/mdempsky/21.go).
		if wrapperFn != nil && len(implicits) == 0 && !deref && !addr {
			if !types.Identical(recv, wrapperFn.Type().Param(0).Type) {
				r.gd.FatalfAt(pos, "want receiver type %v, but have method %L", recv, wrapperFn)
			}
			return wrapperFn
		}

		// Otherwise, if the wrapper function is a static method
		// expression (OMETHEXPR) and the receiver type is unshaped, then
		// we can rely on a statically generated wrapper being available.
		if method, ok := wrapperFn.(*ir.SelectorExpr); ok && method.Op() == ir.OMETHEXPR && !recv.HasShape() {
			return typecheck.NewMethodExpr(r.gd, pos, recv, method.Sel)
		}

		return r.methodExprWrap(origPos, recv, implicits, deref, addr, baseFn, dictPtr)

	case exprIndex:
		x := r.expr()
		pos := r.pos()
		index := r.expr()
		n := typecheck.Expr(r.gd, ir.NewIndexExpr(r.gd, pos, x, index))
		switch n.Op() {
		case ir.OINDEXMAP:
			n := n.(*ir.IndexExpr)
			n.RType = r.rtype(pos)
		}
		return n

	case exprSlice:
		x := r.expr()
		pos := r.pos()
		var index [3]ir.Node
		for i := range index {
			index[i] = r.optExpr()
		}
		op := ir.OSLICE
		if index[2] != nil {
			op = ir.OSLICE3
		}
		return typecheck.Expr(r.gd, ir.NewSliceExpr(r.gd, pos, op, x, index[0], index[1], index[2]))

	case exprAssert:
		x := r.expr()
		pos := r.pos()
		typ := r.exprType()
		srcRType := r.rtype(pos)

		// TODO(mdempsky): Always emit ODYNAMICDOTTYPE for uniformity?
		if typ, ok := typ.(*ir.DynamicType); ok && typ.Op() == ir.ODYNAMICTYPE {
			assert := ir.NewDynamicTypeAssertExpr(r.gd, pos, ir.ODYNAMICDOTTYPE, x, typ.RType)
			assert.SrcRType = srcRType
			assert.ITab = typ.ITab
			return typed(typ.Type(), assert)
		}
		return typecheck.Expr(r.gd, ir.NewTypeAssertExpr(r.gd, pos, x, typ.Type()))

	case exprUnaryOp:
		op := r.op()
		pos := r.pos()
		x := r.expr()

		switch op {
		case ir.OADDR:
			return typecheck.Expr(r.gd, typecheck.NodAddrAt(r.gd, pos, x))
		case ir.ODEREF:
			return typecheck.Expr(r.gd, ir.NewStarExpr(r.gd, pos, x))
		}
		return typecheck.Expr(r.gd, ir.NewUnaryExpr(r.gd, pos, op, x))

	case exprBinaryOp:
		op := r.op()
		x := r.expr()
		pos := r.pos()
		y := r.expr()

		switch op {
		case ir.OANDAND, ir.OOROR:
			return typecheck.Expr(r.gd, ir.NewLogicalExpr(r.gd, pos, op, x, y))
		case ir.OLSH, ir.ORSH:
			// Untyped rhs of non-constant shift, e.g. x << 1.0.
			// If we have a constant value, it must be an int >= 0.
			if ir.IsConstNode(y) {
				val := constant.ToInt(y.Val())
				assert(r.gd, val.Kind() == constant.Int && constant.Sign(val) >= 0)
			}
		}
		return typecheck.Expr(r.gd, ir.NewBinaryExpr(r.gd, pos, op, x, y))

	case exprRecv:
		x := r.expr()
		pos := r.pos()
		for i, n := 0, r.Len(); i < n; i++ {
			x = Implicit(typecheck.DotField(r.gd, pos, x, r.Len()))
		}
		if r.Bool() { // needs deref
			x = Implicit(Deref(r.gd, pos, x.Type().Elem(), x))
		} else if r.Bool() { // needs addr
			x = Implicit(Addr(r.gd, pos, x))
		}
		return x

	case exprCall:
		var fun ir.Node
		var args ir.Nodes
		if r.Bool() { // method call
			recv := r.expr()
			_, method, dictPtr := r.methodExpr()

			if recv.Type().IsInterface() && method.Op() == ir.OMETHEXPR {
				method := method.(*ir.SelectorExpr)

				// The compiler backend (e.g., devirtualization) handle
				// OCALLINTER/ODOTINTER better than OCALLFUNC/OMETHEXPR for
				// interface calls, so we prefer to continue constructing
				// calls that way where possible.
				//
				// There are also corner cases where semantically it's perhaps
				// significant; e.g., fixedbugs/issue15975.go, #38634, #52025.

				fun = typecheck.XDotMethod(r.gd, method.Pos(), recv, method.Sel, true)
			} else {
				if recv.Type().IsInterface() {
					// N.B., this happens currently for typeparam/issue51521.go
					// and typeparam/typeswitch3.go.
					if r.gd.Flag.LowerM != 0 {
						r.gd.WarnfAt(method.Pos(), "imprecise interface call")
					}
				}

				fun = method
				args.Append(recv)
			}
			if dictPtr != nil {
				args.Append(dictPtr)
			}
		} else if r.Bool() { // call to instanced function
			pos := r.pos()
			_, shapedFn, dictPtr := r.funcInst(pos)
			fun = shapedFn
			args.Append(dictPtr)
		} else {
			fun = r.expr()
		}
		pos := r.pos()
		args.Append(r.multiExpr()...)
		dots := r.Bool()
		n := typecheck.Call(r.gd, pos, fun, args, dots)
		switch n.Op() {
		case ir.OAPPEND:
			n := n.(*ir.CallExpr)
			n.RType = r.rtype(pos)
			// For append(a, b...), we don't need the implicit conversion. The typechecker already
			// ensured that a and b are both slices with the same base type, or []byte and string.
			if n.IsDDD {
				if conv, ok := n.Args[1].(*ir.ConvExpr); ok && conv.Op() == ir.OCONVNOP && conv.Implicit() {
					n.Args[1] = conv.X
				}
			}
		case ir.OCOPY:
			n := n.(*ir.BinaryExpr)
			n.RType = r.rtype(pos)
		case ir.ODELETE:
			n := n.(*ir.CallExpr)
			n.RType = r.rtype(pos)
		case ir.OUNSAFESLICE:
			n := n.(*ir.BinaryExpr)
			n.RType = r.rtype(pos)
		}
		return n

	case exprMake:
		pos := r.pos()
		typ := r.exprType()
		extra := r.exprs()
		n := typecheck.Expr(r.gd, ir.NewCallExpr(r.gd, pos, ir.OMAKE, nil, append([]ir.Node{typ}, extra...))).(*ir.MakeExpr)
		n.RType = r.rtype(pos)
		return n

	case exprNew:
		pos := r.pos()
		if r.Bool() {
			// new(expr) -> tmp := expr; &tmp
			x := r.expr()
			x = typecheck.DefaultLit(r.gd, x, nil) // See TODO in exprConvert case.
			var init ir.Nodes
			addr := ir.NewAddrExpr(r.gd, pos, r.tempCopy(pos, x, &init))
			addr.SetInit(init)
			return typecheck.Expr(r.gd, addr)
		}
		// new(T)
		return typecheck.Expr(r.gd, ir.NewUnaryExpr(r.gd, pos, ir.ONEW, r.exprType()))

	case exprSizeof:
		return ir.NewUintptr(r.gd, r.pos(), r.typ().Size())

	case exprAlignof:
		return ir.NewUintptr(r.gd, r.pos(), r.typ().Alignment())

	case exprOffsetof:
		pos := r.pos()
		typ := r.typ()
		types.CalcSize(typ)

		var offset int64
		for i := r.Len(); i >= 0; i-- {
			field := typ.Field(r.Len())
			offset += field.Offset
			typ = field.Type
		}

		return ir.NewUintptr(r.gd, pos, offset)

	case exprReshape:
		typ := r.typ()
		x := r.expr()

		if types.IdenticalStrict(x.Type(), typ) {
			return x
		}

		// Comparison expressions are constructed as "untyped bool" still.
		//
		// TODO(mdempsky): It should be safe to reshape them here too, but
		// maybe it's better to construct them with the proper type
		// instead.
		if x.Type() == types.UntypedBool && typ.IsBoolean() {
			return x
		}

		r.gd.AssertfAt(x.Type().HasShape() || typ.HasShape(), x.Pos(), "%L and %v are not shape types", x, typ)
		r.gd.AssertfAt(types.Identical(x.Type(), typ), x.Pos(), "%L is not shape-identical to %v", x, typ)

		// We use ir.HasUniquePos here as a check that x only appears once
		// in the AST, so it's okay for us to call SetType without
		// breaking any other uses of it.
		//
		// Notably, any ONAMEs should already have the exactly right shape
		// type and been caught by types.IdenticalStrict above.
		r.gd.AssertfAt(ir.HasUniquePos(r.gd, x), x.Pos(), "cannot call SetType(%v) on %L", typ, x)

		if r.gd.Debug.Reshape != 0 {
			r.gd.WarnfAt(x.Pos(), "reshaping %L to %v", x, typ)
		}

		x.SetType(typ)
		return x

	case exprConvert:
		implicit := r.Bool()
		typ := r.typ()
		pos := r.pos()
		typeWord, srcRType := r.convRTTI(pos)
		dstTypeParam := r.Bool()
		identical := r.Bool()
		x := r.expr()

		// TODO(mdempsky): Stop constructing expressions of untyped type.
		x = typecheck.DefaultLit(r.gd, x, typ)

		ce := ir.NewConvExpr(r.gd, pos, ir.OCONV, typ, x)
		ce.TypeWord, ce.SrcRType = typeWord, srcRType
		if implicit {
			ce.SetImplicit(true)
		}
		n := typecheck.Expr(r.gd, ce)

		// Conversions between non-identical, non-empty interfaces always
		// requires a runtime call, even if they have identical underlying
		// interfaces. This is because we create separate itab instances
		// for each unique interface type, not merely each unique
		// interface shape.
		//
		// However, due to shape types, typecheck.Expr might mistakenly
		// think a conversion between two non-empty interfaces are
		// identical and set ir.OCONVNOP, instead of ir.OCONVIFACE. To
		// ensure we update the itab field appropriately, we force it to
		// ir.OCONVIFACE instead when shape types are involved.
		//
		// TODO(mdempsky): Are there other places we might get this wrong?
		// Should this be moved down into typecheck.{Assign,Convert}op?
		// This would be a non-issue if itabs were unique for each
		// *underlying* interface type instead.
		if !identical {
			if n, ok := n.(*ir.ConvExpr); ok && n.Op() == ir.OCONVNOP && n.Type().IsInterface() && !n.Type().IsEmptyInterface() && (n.Type().HasShape() || n.X.Type().HasShape()) {
				n.SetOp(ir.OCONVIFACE)
			}
		}

		// spec: "If the type is a type parameter, the constant is converted
		// into a non-constant value of the type parameter."
		if dstTypeParam && ir.IsConstNode(n) {
			// Wrap in an OCONVNOP node to ensure result is non-constant.
			n = Implicit(ir.NewConvExpr(r.gd, pos, ir.OCONVNOP, n.Type(), n))
			n.SetTypecheck(1)
		}
		return n

	case exprRuntimeBuiltin:
		builtin := typecheck.LookupRuntime(r.gd, r.String())
		return builtin
	}
}

// funcInst reads an instantiated function reference, and returns
// three (possibly nil) expressions related to it:
//
// baseFn is always non-nil: it's either a function of the appropriate
// type already, or it has an extra dictionary parameter as the first
// parameter.
//
// If dictPtr is non-nil, then it's a dictionary argument that must be
// passed as the first argument to baseFn.
//
// If wrapperFn is non-nil, then it's either the same as baseFn (if
// dictPtr is nil), or it's semantically equivalent to currying baseFn
// to pass dictPtr. (wrapperFn is nil when dictPtr is an expression
// that needs to be computed dynamically.)
//
// For callers that are creating a call to the returned function, it's
// best to emit a call to baseFn, and include dictPtr in the arguments
// list as appropriate.
//
// For callers that want to return the function without invoking it,
// they may return wrapperFn if it's non-nil; but otherwise, they need
// to create their own wrapper.
func (r *reader) funcInst(pos src.XPos) (wrapperFn, baseFn, dictPtr ir.Node) {
	// Like in methodExpr, I'm pretty sure this isn't needed.
	var implicits []*types.Type
	if r.dict != nil {
		implicits = r.dict.targs
	}

	if r.Bool() { // dynamic subdictionary
		idx := r.Len()
		info := r.dict.subdicts[idx]
		explicits := r.p.typListIdx(info.explicits, r.dict)

		baseFn = r.p.objIdx(info.idx, implicits, explicits, true).(*ir.Name)

		// TODO(mdempsky): Is there a more robust way to get the
		// dictionary pointer type here?
		dictPtrType := baseFn.Type().Param(0).Type
		dictPtr = typecheck.Expr(r.gd, ir.NewConvExpr(r.gd, pos, ir.OCONVNOP, dictPtrType, r.dictWord(pos, r.dict.subdictsOffset()+idx)))

		return
	}

	info := r.objInfo()
	explicits := r.p.typListIdx(info.explicits, r.dict)

	wrapperFn = r.p.objIdx(info.idx, implicits, explicits, false).(*ir.Name)
	baseFn = r.p.objIdx(info.idx, implicits, explicits, true).(*ir.Name)

	dictName := r.p.objDictName(info.idx, implicits, explicits)
	dictPtr = typecheck.Expr(r.gd, ir.NewAddrExpr(r.gd, pos, dictName))

	return
}

func (pr *pkgReader) objDictName(idx index, implicits, explicits []*types.Type) *ir.Name {
	rname := pr.newReader(pkgbits.SectionName, idx, pkgbits.SyncObject1)
	_, sym := rname.qualifiedIdent()
	tag := pkgbits.CodeObj(rname.Code(pkgbits.SyncCodeObj))

	if tag == pkgbits.ObjStub {
		assert(pr.gd, !sym.IsBlank())
		if pri, ok := objReader(pr.gd)[sym]; ok {
			return pri.pr.objDictName(pri.idx, nil, explicits)
		}
		pr.gd.Fatalf("unresolved stub: %v", sym)
	}

	dict, err := pr.objDictIdx(sym, idx, implicits, explicits, false)
	if err != nil {
		pr.gd.Fatalf("%v", err)
	}

	return pr.dictNameOf(dict)
}

// curry returns a function literal that calls fun with arg0 and
// (optionally) arg1, accepting additional arguments to the function
// literal as necessary to satisfy fun's signature.
//
// If nilCheck is true and arg0 is an interface value, then it's
// checked to be non-nil as an initial step at the point of evaluating
// the function literal itself.
func (r *reader) curry(origPos src.XPos, ifaceHack bool, fun ir.Node, arg0, arg1 ir.Node) ir.Node {
	var captured ir.Nodes
	captured.Append(fun, arg0)
	if arg1 != nil {
		captured.Append(arg1)
	}

	params, results := syntheticSig(r.gd, fun.Type())
	params = params[len(captured)-1:] // skip curried parameters
	typ := types.NewSignature(r.gd, nil, params, results)

	addBody := func(pos src.XPos, r *reader, captured []ir.Node) {
		fun := captured[0]

		var args ir.Nodes
		args.Append(captured[1:]...)
		args.Append(r.syntheticArgs()...)

		r.syntheticTailCall(pos, fun, args)
	}

	return r.syntheticClosure(origPos, typ, ifaceHack, captured, addBody)
}

// methodExprWrap returns a function literal that changes method's
// first parameter's type to recv, and uses implicits/deref/addr to
// select the appropriate receiver parameter to pass to method.
func (r *reader) methodExprWrap(origPos src.XPos, recv *types.Type, implicits []int, deref, addr bool, method, dictPtr ir.Node) ir.Node {
	var captured ir.Nodes
	captured.Append(method)

	params, results := syntheticSig(r.gd, method.Type())

	// Change first parameter to recv.
	params[0].Type = recv

	// If we have a dictionary pointer argument to pass, then omit the
	// underlying method expression's dictionary parameter from the
	// returned signature too.
	if dictPtr != nil {
		captured.Append(dictPtr)
		params = append(params[:1], params[2:]...)
	}

	typ := types.NewSignature(r.gd, nil, params, results)

	addBody := func(pos src.XPos, r *reader, captured []ir.Node) {
		fn := captured[0]
		args := r.syntheticArgs()

		// Rewrite first argument based on implicits/deref/addr.
		{
			arg := args[0]
			for _, ix := range implicits {
				arg = Implicit(typecheck.DotField(r.gd, pos, arg, ix))
			}
			if deref {
				arg = Implicit(Deref(r.gd, pos, arg.Type().Elem(), arg))
			} else if addr {
				arg = Implicit(Addr(r.gd, pos, arg))
			}
			args[0] = arg
		}

		// Insert dictionary argument, if provided.
		if dictPtr != nil {
			newArgs := make([]ir.Node, len(args)+1)
			newArgs[0] = args[0]
			newArgs[1] = captured[1]
			copy(newArgs[2:], args[1:])
			args = newArgs
		}

		r.syntheticTailCall(pos, fn, args)
	}

	return r.syntheticClosure(origPos, typ, false, captured, addBody)
}

// syntheticClosure constructs a synthetic function literal for
// currying dictionary arguments. origPos is the position used for the
// closure, which must be a non-inlined position. typ is the function
// literal's signature type.
//
// captures is a list of expressions that need to be evaluated at the
// point of function literal evaluation and captured by the function
// literal. If ifaceHack is true and captures[1] is an interface type,
// it's checked to be non-nil after evaluation.
//
// addBody is a callback function to populate the function body. The
// list of captured values passed back has the captured variables for
// use within the function literal, corresponding to the expressions
// in captures.
func (r *reader) syntheticClosure(origPos src.XPos, typ *types.Type, ifaceHack bool, captures ir.Nodes, addBody func(pos src.XPos, r *reader, captured []ir.Node)) ir.Node {
	// isSafe reports whether n is an expression that we can safely
	// defer to evaluating inside the closure instead, to avoid storing
	// them into the closure.
	//
	// In practice this is always (and only) the wrappee function.
	isSafe := func(n ir.Node) bool {
		if n.Op() == ir.ONAME && n.(*ir.Name).Class == ir.PFUNC {
			return true
		}
		if n.Op() == ir.OMETHEXPR {
			return true
		}

		return false
	}

	fn := r.inlClosureFunc(origPos, typ, ir.OCLOSURE)
	fn.SetWrapper(true)

	clo := fn.OClosure
	inlPos := clo.Pos()

	var init ir.Nodes
	for i, n := range captures {
		if isSafe(n) {
			continue // skip capture; can reference directly
		}

		tmp := r.tempCopy(inlPos, n, &init)
		ir.NewClosureVar(r.gd, origPos, fn, tmp)

		// We need to nil check interface receivers at the point of method
		// value evaluation, ugh.
		if ifaceHack && i == 1 && n.Type().IsInterface() {
			check := ir.NewUnaryExpr(r.gd, inlPos, ir.OCHECKNIL, ir.NewUnaryExpr(r.gd, inlPos, ir.OITAB, tmp))
			init.Append(typecheck.Stmt(r.gd, check))
		}
	}

	pri := pkgReaderIndex{gd: r.gd, synthetic: func(pos src.XPos, r *reader) {
		captured := make([]ir.Node, len(captures))
		next := 0
		for i, n := range captures {
			if isSafe(n) {
				captured[i] = n
			} else {
				captured[i] = r.closureVars[next]
				next++
			}
		}
		assert(r.gd, next == len(r.closureVars))

		addBody(origPos, r, captured)
	}}
	bodyReader(r.gd)[fn] = pri
	pri.funcBody(fn)

	return ir.InitExpr(r.gd, init, clo)
}

// syntheticSig duplicates and returns the params and results lists
// for sig, but renaming anonymous parameters so they can be assigned
// ir.Names.
func syntheticSig(gd *base.Invocation, sig *types.Type) (params, results []*types.Field) {
	clone := func(params []*types.Field) []*types.Field {
		res := make([]*types.Field, len(params))
		for i, param := range params {
			// TODO(mdempsky): It would be nice to preserve the original
			// parameter positions here instead, but at least
			// typecheck.NewMethodType replaces them with gd.Pos, making
			// them useless. Worse, the positions copied from gd.Pos may
			// have inlining contexts, which we definitely don't want here
			// (e.g., #54625).
			res[i] = types.NewField(gd.AutogeneratedPos, param.Sym, param.Type)
			res[i].SetIsDDD(param.IsDDD())
		}
		return res
	}

	return clone(sig.Params()), clone(sig.Results())
}

func (r *reader) optExpr() ir.Node {
	if r.Bool() {
		return r.expr()
	}
	return nil
}

// methodExpr reads a method expression reference, and returns three
// (possibly nil) expressions related to it:
//
// baseFn is always non-nil: it's either a function of the appropriate
// type already, or it has an extra dictionary parameter as the second
// parameter (i.e., immediately after the promoted receiver
// parameter).
//
// If dictPtr is non-nil, then it's a dictionary argument that must be
// passed as the second argument to baseFn.
//
// If wrapperFn is non-nil, then it's either the same as baseFn (if
// dictPtr is nil), or it's semantically equivalent to currying baseFn
// to pass dictPtr. (wrapperFn is nil when dictPtr is an expression
// that needs to be computed dynamically.)
//
// For callers that are creating a call to the returned method, it's
// best to emit a call to baseFn, and include dictPtr in the arguments
// list as appropriate.
//
// For callers that want to return a method expression without
// invoking it, they may return wrapperFn if it's non-nil; but
// otherwise, they need to create their own wrapper.
func (r *reader) methodExpr() (wrapperFn, baseFn, dictPtr ir.Node) {
	recv := r.typ()
	sig0 := r.typ()
	pos := r.pos()
	sym := r.selector()

	// Signature type to return (i.e., recv prepended to the method's
	// normal parameters list).
	sig := typecheck.NewMethodType(r.gd, sig0, recv)

	if r.Bool() { // type parameter method expression
		idx := r.Len()
		word := r.dictWord(pos, r.dict.typeParamMethodExprsOffset()+idx)

		// TODO(mdempsky): If the type parameter was instantiated with an
		// interface type (i.e., embed.IsInterface()), then we could
		// return the OMETHEXPR instead and save an indirection.

		// We wrote the method expression's entry point PC into the
		// dictionary, but for Go `func` values we need to return a
		// closure (i.e., pointer to a structure with the PC as the first
		// field). Because method expressions don't have any closure
		// variables, we pun the dictionary entry as the closure struct.
		fn := typecheck.Expr(r.gd, ir.NewConvExpr(r.gd, pos, ir.OCONVNOP, sig, ir.NewAddrExpr(r.gd, pos, word)))
		return fn, fn, nil
	}

	// TODO(mdempsky): I'm pretty sure this isn't needed: implicits is
	// only relevant to locally defined types, but they can't have
	// (non-promoted) methods.
	var implicits []*types.Type
	if r.dict != nil {
		implicits = r.dict.targs
	}

	if r.Bool() { // dynamic subdictionary
		idx := r.Len()
		info := r.dict.subdicts[idx]
		explicits := r.p.typListIdx(info.explicits, r.dict)

		shapedObj := r.p.objIdx(info.idx, implicits, explicits, true).(*ir.Name)
		shapedFn := shapedMethodExpr(r.gd, pos, shapedObj, sym)

		// TODO(mdempsky): Is there a more robust way to get the
		// dictionary pointer type here?
		dictPtrType := shapedFn.Type().Param(1).Type
		dictPtr := typecheck.Expr(r.gd, ir.NewConvExpr(r.gd, pos, ir.OCONVNOP, dictPtrType, r.dictWord(pos, r.dict.subdictsOffset()+idx)))

		return nil, shapedFn, dictPtr
	}

	if r.Bool() { // static dictionary
		info := r.objInfo()
		explicits := r.p.typListIdx(info.explicits, r.dict)

		shapedObj := r.p.objIdx(info.idx, implicits, explicits, true).(*ir.Name)
		shapedFn := shapedMethodExpr(r.gd, pos, shapedObj, sym)

		dict := r.p.objDictName(info.idx, implicits, explicits)
		dictPtr := typecheck.Expr(r.gd, ir.NewAddrExpr(r.gd, pos, dict))

		// Check that dictPtr matches shapedFn's dictionary parameter.
		if !types.Identical(dictPtr.Type(), shapedFn.Type().Param(1).Type) {
			r.gd.FatalfAt(pos, "dict %L, but shaped method %L", dict, shapedFn)
		}

		// For statically known instantiations, we can take advantage of
		// the stenciled wrapper.
		r.gd.AssertfAt(!recv.HasShape(), pos, "shaped receiver %v", recv)
		wrapperFn := typecheck.NewMethodExpr(r.gd, pos, recv, sym)
		r.gd.AssertfAt(types.Identical(sig, wrapperFn.Type()), pos, "wrapper %L does not have type %v", wrapperFn, sig)

		return wrapperFn, shapedFn, dictPtr
	}

	// Simple method expression; no dictionary needed.
	r.gd.AssertfAt(!recv.HasShape() || recv.IsInterface(), pos, "shaped receiver %v", recv)
	fn := typecheck.NewMethodExpr(r.gd, pos, recv, sym)
	return fn, fn, nil
}

// shapedMethodExpr returns the specified method on the given shaped
// type.
func shapedMethodExpr(gd *base.Invocation, pos src.XPos, obj *ir.Name, sym *types.Sym) *ir.SelectorExpr {
	assert(gd, obj.Op() == ir.OTYPE)

	typ := obj.Type()
	assert(gd, typ.HasShape())

	method := func() *types.Field {
		for _, method := range typ.Methods() {
			if method.Sym == sym {
				return method
			}
		}

		gd.FatalfAt(pos, "failed to find method %v in shaped type %v", sym, typ)
		panic("unreachable")
	}()

	// Construct an OMETHEXPR node.
	recv := method.Type.Recv().Type
	return typecheck.NewMethodExpr(gd, pos, recv, sym)
}

func (r *reader) multiExpr() []ir.Node {
	r.Sync(pkgbits.SyncMultiExpr)

	if r.Bool() { // N:1
		pos := r.pos()
		expr := r.expr()

		results := make([]ir.Node, r.Len())
		as := ir.NewAssignListStmt(r.gd, pos, ir.OAS2, nil, []ir.Node{expr})
		as.Def = true
		for i := range results {
			tmp := r.temp(pos, r.typ())
			tmp.Defn = as
			as.PtrInit().Append(ir.NewDecl(r.gd, pos, ir.ODCL, tmp))
			as.Lhs.Append(tmp)

			res := ir.Node(tmp)
			if r.Bool() {
				n := ir.NewConvExpr(r.gd, pos, ir.OCONV, r.typ(), res)
				n.TypeWord, n.SrcRType = r.convRTTI(pos)
				n.SetImplicit(true)
				res = typecheck.Expr(r.gd, n)
			}
			results[i] = res
		}

		// TODO(mdempsky): Could use ir.InlinedCallExpr instead?
		results[0] = ir.InitExpr(r.gd, []ir.Node{typecheck.Stmt(r.gd, as)}, results[0])
		return results
	}

	// N:N
	exprs := make([]ir.Node, r.Len())
	if len(exprs) == 0 {
		return nil
	}
	for i := range exprs {
		exprs[i] = r.expr()
	}
	return exprs
}

// temp returns a new autotemp of the specified type.
func (r *reader) temp(pos src.XPos, typ *types.Type) *ir.Name {
	return typecheck.TempAt(r.gd, pos, r.curfn, typ)
}

// tempCopy declares and returns a new autotemp initialized to the
// value of expr.
func (r *reader) tempCopy(pos src.XPos, expr ir.Node, init *ir.Nodes) *ir.Name {
	tmp := r.temp(pos, expr.Type())

	init.Append(typecheck.Stmt(r.gd, ir.NewDecl(r.gd, pos, ir.ODCL, tmp)))

	assign := ir.NewAssignStmt(r.gd, pos, tmp, expr)
	assign.Def = true
	init.Append(typecheck.Stmt(r.gd, ir.NewAssignStmt(r.gd, pos, tmp, expr)))

	tmp.Defn = assign

	return tmp
}

func (r *reader) compLit() ir.Node {
	r.Sync(pkgbits.SyncCompLit)
	pos := r.pos()
	typ0 := r.typ()

	typ := typ0
	if typ.IsPtr() {
		typ = typ.Elem()
	}
	if typ.Kind() == types.TFORW {
		r.gd.FatalfAt(pos, "unresolved composite literal type: %v", typ)
	}
	var rtype ir.Node
	if typ.IsMap() {
		rtype = r.rtype(pos)
	}
	isStruct := typ.Kind() == types.TSTRUCT

	elems := make([]ir.Node, r.Len())
	for i := range elems {
		elemp := &elems[i]

		if isStruct {
			sk := ir.NewStructKeyExpr(r.gd, r.pos(), typ.Field(r.Len()), nil)
			*elemp, elemp = sk, &sk.Value
		} else if r.Bool() {
			kv := ir.NewKeyExpr(r.gd, r.pos(), r.expr(), nil)
			*elemp, elemp = kv, &kv.Value
		}

		*elemp = r.expr()
	}

	lit := typecheck.Expr(r.gd, ir.NewCompLitExpr(r.gd, pos, ir.OCOMPLIT, typ, elems))
	if rtype != nil {
		lit := lit.(*ir.CompLitExpr)
		lit.RType = rtype
	}
	if typ0.IsPtr() {
		lit = typecheck.Expr(r.gd, typecheck.NodAddrAt(r.gd, pos, lit))
		lit.SetType(typ0)
	}
	return lit
}

func (r *reader) funcLit() ir.Node {
	r.Sync(pkgbits.SyncFuncLit)

	// The underlying function declaration (including its parameters'
	// positions, if any) need to remain the original, uninlined
	// positions. This is because we track inlining-context on nodes so
	// we can synthesize the extra implied stack frames dynamically when
	// generating tracebacks, whereas those stack frames don't make
	// sense *within* the function literal. (Any necessary inlining
	// adjustments will have been applied to the call expression
	// instead.)
	//
	// This is subtle, and getting it wrong leads to cycles in the
	// inlining tree, which lead to infinite loops during stack
	// unwinding (#46234, #54625).
	//
	// Note that we *do* want the inline-adjusted position for the
	// OCLOSURE node, because that position represents where any heap
	// allocation of the closure is credited (#49171).
	r.suppressInlPos++
	origPos := r.pos()
	sig := r.signature(nil)
	r.suppressInlPos--
	why := ir.OCLOSURE
	if r.Bool() {
		why = ir.ORANGE
	}

	fn := r.inlClosureFunc(origPos, sig, why)

	fn.ClosureVars = make([]*ir.Name, 0, r.Len())
	for len(fn.ClosureVars) < cap(fn.ClosureVars) {
		// TODO(mdempsky): I think these should be original positions too
		// (i.e., not inline-adjusted).
		ir.NewClosureVar(r.gd, r.pos(), fn, r.useLocal())
	}
	if param := r.dictParam; param != nil {
		// If we have a dictionary parameter, capture it too. For
		// simplicity, we capture it last and unconditionally.
		ir.NewClosureVar(r.gd, param.Pos(), fn, param)
	}

	r.addBody(fn, nil)

	return fn.OClosure
}

// inlClosureFunc constructs a new closure function, but correctly
// handles inlining.
func (r *reader) inlClosureFunc(origPos src.XPos, sig *types.Type, why ir.Op) *ir.Func {
	curfn := r.inlCaller
	if curfn == nil {
		curfn = r.curfn
	}

	// TODO(mdempsky): Remove hard-coding of typecheck.Target.
	return ir.NewClosureFunc(r.gd, origPos, r.inlPos(origPos), why, sig, curfn, typecheck.Target(r.gd))
}

func (r *reader) exprList() []ir.Node {
	r.Sync(pkgbits.SyncExprList)
	return r.exprs()
}

func (r *reader) exprs() []ir.Node {
	r.Sync(pkgbits.SyncExprs)
	nodes := make([]ir.Node, r.Len())
	if len(nodes) == 0 {
		return nil // TODO(mdempsky): Unclear if this matters.
	}
	for i := range nodes {
		nodes[i] = r.expr()
	}
	return nodes
}

// dictWord returns an expression to return the specified
// uintptr-typed word from the dictionary parameter.
func (r *reader) dictWord(pos src.XPos, idx int) ir.Node {
	r.gd.AssertfAt(r.dictParam != nil, pos, "expected dictParam in %v", r.curfn)
	return typecheck.Expr(r.gd, ir.NewIndexExpr(r.gd, pos, r.dictParam, ir.NewInt(r.gd, pos, int64(idx))))
}

// rttiWord is like dictWord, but converts it to *byte (the type used
// internally to represent *runtime._type and *runtime.itab).
func (r *reader) rttiWord(pos src.XPos, idx int) ir.Node {
	return typecheck.Expr(r.gd, ir.NewConvExpr(r.gd, pos, ir.OCONVNOP, types.NewPtr(types.Types[types.TUINT8]), r.dictWord(pos, idx)))
}

// rtype reads a type reference from the element bitstream, and
// returns an expression of type *runtime._type representing that
// type.
func (r *reader) rtype(pos src.XPos) ir.Node {
	_, rtype := r.rtype0(pos)
	return rtype
}

func (r *reader) rtype0(pos src.XPos) (typ *types.Type, rtype ir.Node) {
	r.Sync(pkgbits.SyncRType)
	if r.Bool() { // derived type
		idx := r.Len()
		info := r.dict.rtypes[idx]
		typ = r.p.typIdx(info, r.dict, true)
		rtype = r.rttiWord(pos, r.dict.rtypesOffset()+idx)
		return
	}

	typ = r.typ()
	rtype = reflectdata.TypePtrAt(r.gd, pos, typ)
	return
}

// varDictIndex populates name.DictIndex if name is a derived type.
func (r *reader) varDictIndex(name *ir.Name) {
	if r.Bool() {
		idx := 1 + r.dict.rtypesOffset() + r.Len()
		if int(uint16(idx)) != idx {
			r.gd.FatalfAt(name.Pos(), "DictIndex overflow for %v: %v", name, idx)
		}
		name.DictIndex = uint16(idx)
	}
}

// itab returns a (typ, iface) pair of types.
//
// typRType and ifaceRType are expressions that evaluate to the
// *runtime._type for typ and iface, respectively.
//
// If typ is a concrete type and iface is a non-empty interface type,
// then itab is an expression that evaluates to the *runtime.itab for
// the pair. Otherwise, itab is nil.
func (r *reader) itab(pos src.XPos) (typ *types.Type, typRType ir.Node, iface *types.Type, ifaceRType ir.Node, itab ir.Node) {
	typ, typRType = r.rtype0(pos)
	iface, ifaceRType = r.rtype0(pos)

	idx := -1
	if r.Bool() {
		idx = r.Len()
	}

	if !typ.IsInterface() && iface.IsInterface() && !iface.IsEmptyInterface() {
		if idx >= 0 {
			itab = r.rttiWord(pos, r.dict.itabsOffset()+idx)
		} else {
			r.gd.AssertfAt(!typ.HasShape(), pos, "%v is a shape type", typ)
			r.gd.AssertfAt(!iface.HasShape(), pos, "%v is a shape type", iface)

			lsym := reflectdata.ITabLsym(r.gd, typ, iface)
			itab = typecheck.LinksymAddr(r.gd, pos, lsym, types.Types[types.TUINT8])
		}
	}

	return
}

// convRTTI returns expressions appropriate for populating an
// ir.ConvExpr's TypeWord and SrcRType fields, respectively.
func (r *reader) convRTTI(pos src.XPos) (typeWord, srcRType ir.Node) {
	r.Sync(pkgbits.SyncConvRTTI)
	src, srcRType0, dst, dstRType, itab := r.itab(pos)
	if !dst.IsInterface() {
		return
	}

	// See reflectdata.ConvIfaceTypeWord.
	switch {
	case dst.IsEmptyInterface():
		if !src.IsInterface() {
			typeWord = srcRType0 // direct eface construction
		}
	case !src.IsInterface():
		typeWord = itab // direct iface construction
	default:
		typeWord = dstRType // convI2I
	}

	// See reflectdata.ConvIfaceSrcRType.
	if !src.IsInterface() {
		srcRType = srcRType0
	}

	return
}

func (r *reader) exprType() ir.Node {
	r.Sync(pkgbits.SyncExprType)
	pos := r.pos()

	var typ *types.Type
	var rtype, itab ir.Node

	if r.Bool() {
		// non-empty interface
		typ, rtype, _, _, itab = r.itab(pos)
		if !typ.IsInterface() {
			rtype = nil // TODO(mdempsky): Leave set?
		}
	} else {
		typ, rtype = r.rtype0(pos)

		if !r.Bool() { // not derived
			return ir.TypeNode(r.gd, typ)
		}
	}

	dt := ir.NewDynamicType(r.gd, pos, rtype)
	dt.ITab = itab
	dt = typed(typ, dt).(*ir.DynamicType)
	if st := dt.ToStatic(); st != nil {
		return st
	}
	return dt
}

func (r *reader) op() ir.Op {
	r.Sync(pkgbits.SyncOp)
	return ir.Op(r.Len())
}

// @@@ Package initialization

func (r *reader) pkgInit(self *types.Pkg, target *ir.Package) {
	cgoPragmas := make([][]string, r.Len())
	for i := range cgoPragmas {
		cgoPragmas[i] = r.Strings()
	}
	target.CgoPragmas = cgoPragmas

	r.pkgInitOrder(target)

	r.pkgDecls(target)

	r.Sync(pkgbits.SyncEOF)
}

// pkgInitOrder creates a synthetic init function to handle any
// package-scope initialization statements.
func (r *reader) pkgInitOrder(target *ir.Package) {
	initOrder := make([]ir.Node, r.Len())
	if len(initOrder) == 0 {
		return
	}

	// Make a function that contains all the initialization statements.
	pos := r.gd.AutogeneratedPos
	r.gd.Pos = pos

	fn := ir.NewFunc(r.gd, pos, pos, typecheck.Lookup(r.gd, "init"), types.NewSignature(r.gd, nil, nil, nil))
	fn.SetIsPackageInit(true)
	fn.SetInlinabilityChecked(true) // suppress useless "can inline" diagnostics

	typecheck.DeclFunc(r.gd, fn)
	r.curfn = fn

	for i := range initOrder {
		lhs := make([]ir.Node, r.Len())
		for j := range lhs {
			lhs[j] = r.obj()
		}
		rhs := r.expr()
		pos := lhs[0].Pos()

		var as ir.Node
		if len(lhs) == 1 {
			as = typecheck.Stmt(r.gd, ir.NewAssignStmt(r.gd, pos, lhs[0], rhs))
		} else {
			as = typecheck.Stmt(r.gd, ir.NewAssignListStmt(r.gd, pos, ir.OAS2, lhs, []ir.Node{rhs}))
		}

		for _, v := range lhs {
			v.(*ir.Name).Defn = as
		}

		initOrder[i] = as
	}

	fn.Body = initOrder

	typecheck.FinishFuncBody(r.gd)
	r.curfn = nil
	r.locals = nil

	// Outline (if legal/profitable) global map inits.
	staticinit.OutlineMapInits(r.gd, fn)

	target.Inits = append(target.Inits, fn)
}

func (r *reader) pkgDecls(target *ir.Package) {
	r.Sync(pkgbits.SyncDecls)
	for {
		switch code := codeDecl(r.Code(pkgbits.SyncDecl)); code {
		default:
			panic(fmt.Sprintf("unhandled decl: %v", code))

		case declEnd:
			return

		case declFunc:
			names := r.pkgObjs(target)
			assert(r.gd, len(names) == 1)
			target.Funcs = append(target.Funcs, names[0].Func)

		case declMethod:
			typ := r.typ()
			sym := r.selector()

			method := typecheck.Lookdot1(r.gd, nil, sym, typ, typ.Methods(), 0)
			target.Funcs = append(target.Funcs, method.Nname.(*ir.Name).Func)

		case declVar:
			names := r.pkgObjs(target)

			if n := r.Len(); n > 0 {
				assert(r.gd, len(names) == 1)
				embeds := make([]ir.Embed, n)
				for i := range embeds {
					embeds[i] = ir.Embed{Pos: r.pos(), Patterns: r.Strings()}
				}
				names[0].Embed = &embeds
				target.Embeds = append(target.Embeds, names[0])
			}

		case declOther:
			r.pkgObjs(target)
		}
	}
}

func (r *reader) pkgObjs(target *ir.Package) []*ir.Name {
	r.Sync(pkgbits.SyncDeclNames)
	nodes := make([]*ir.Name, r.Len())
	for i := range nodes {
		r.Sync(pkgbits.SyncDeclName)

		name := r.obj().(*ir.Name)
		nodes[i] = name

		sym := name.Sym()
		if sym.IsBlank() {
			continue
		}

		switch name.Class {
		default:
			r.gd.FatalfAt(name.Pos(), "unexpected class: %v", name.Class)

		case ir.PEXTERN:
			target.Externs = append(target.Externs, name)

		case ir.PFUNC:
			assert(r.gd, name.Type().Recv() == nil)

			// TODO(mdempsky): Cleaner way to recognize init?
			if strings.HasPrefix(sym.Name, "init.") {
				target.Inits = append(target.Inits, name.Func)
			}
		}

		if r.gd.Ctxt.Flag_dynlink && types.LocalPkg(r.gd).Name == "main" && types.IsExported(sym.Name) && name.Op() == ir.ONAME {
			assert(r.gd, !sym.OnExportList())
			target.PluginExports = append(target.PluginExports, name)
			sym.SetOnExportList(true)
		}

		if r.gd.Flag.AsmHdr != "" && (name.Op() == ir.OLITERAL || name.Op() == ir.OTYPE) {
			assert(r.gd, !sym.Asm())
			target.AsmHdrDecls = append(target.AsmHdrDecls, name)
			sym.SetAsm(true)
		}
	}

	return nodes
}

// @@@ Inlining

// unifiedHaveInlineBody reports whether we have the function body for
// fn, so we can inline it.
func unifiedHaveInlineBody(gd *base.Invocation, fn *ir.Func) bool {
	if fn.Inl == nil {
		return false
	}

	_, ok := bodyReader(gd)[fn]
	if fn.Nname.Defn == nil {
		_, ok = importBodyReader(gd)[fn.Sym()]
	}
	return ok
}

// (gd.NoderInlgen is the per-Invocation counter for inlined-call name
// minting; previously a package-level var.)

// unifiedInlineCall implements inline.NewInline by re-reading the function
// body from its Unified IR export data.
func unifiedInlineCall(gd *base.Invocation, callerfn *ir.Func, call *ir.CallExpr, fn *ir.Func, inlIndex int) *ir.InlinedCallExpr {
	pri, ok := bodyReaderFor(gd, fn)
	if !ok {
		gd.FatalfAt(call.Pos(), "cannot inline call to %v: missing inline body", fn)
	}

	if !fn.Inl.HaveDcl {
		expandInline(gd, fn, pri)
	}

	r := pri.asReader(pkgbits.SectionBody, pkgbits.SyncFuncBody)

	tmpfn := ir.NewFunc(gd, fn.Pos(), fn.Nname.Pos(), callerfn.Sym(), fn.Type())

	r.curfn = tmpfn

	r.inlCaller = callerfn
	r.inlCall = call
	r.inlFunc = fn
	r.inlTreeIndex = inlIndex
	r.inlPosBases = make(map[*src.PosBase]*src.PosBase)
	r.funarghack = true

	r.closureVars = make([]*ir.Name, len(r.inlFunc.ClosureVars))
	for i, cv := range r.inlFunc.ClosureVars {
		// TODO(mdempsky): It should be possible to support this case, but
		// for now we rely on the inliner avoiding it.
		if cv.Outer.Curfn != callerfn {
			gd.FatalfAt(call.Pos(), "inlining closure call across frames")
		}
		r.closureVars[i] = cv.Outer
	}
	if len(r.closureVars) != 0 && r.hasTypeParams() {
		r.dictParam = r.closureVars[len(r.closureVars)-1] // dictParam is last; see reader.funcLit
	}

	r.declareParams()

	var inlvars, retvars []*ir.Name
	{
		sig := r.curfn.Type()
		endParams := sig.NumRecvs() + sig.NumParams()
		endResults := endParams + sig.NumResults()

		inlvars = r.curfn.Dcl[:endParams]
		retvars = r.curfn.Dcl[endParams:endResults]
	}

	r.delayResults = fn.Inl.CanDelayResults

	r.retlabel = typecheck.AutoLabel(gd, ".i")
	gd.NoderInlgen++

	init := ir.TakeInit(call)

	// For normal function calls, the function callee expression
	// may contain side effects. Make sure to preserve these,
	// if necessary (#42703).
	if call.Op() == ir.OCALLFUNC {
		inline.CalleeEffects(r.gd, &init, call.Fun)
	}

	var args ir.Nodes
	if call.Op() == ir.OCALLMETH {
		gd.FatalfAt(call.Pos(), "OCALLMETH missed by typecheck")
	}
	args.Append(call.Args...)

	// gd Phase G: the caller may have extended call.Args with
	// trailing nil outBuf args (FillOutBufArgs). Inlining binds
	// call.Args 1:1 to the callee's inlvars (Dcl[:endParams]),
	// which only covers the user-declared params. Drop whatever
	// surplus args were appended — they're always outBuf nils that
	// the inlined body doesn't reference.
	//
	// Compute the surplus from the actual arg count vs the callee's
	// stock NumParams: this handles both direct calls (where
	// FillOutBufArgs added nils for the callee's outBufs) and
	// function-value calls where the caller saw a different Type
	// instance without the GdReturnOutBuf flag and so DIDN'T add
	// nils.
	stockParams := fn.Type().NumRecvs() + fn.Type().NumParams()
	if extra := len(args) - stockParams; extra > 0 {
		args = args[:len(args)-extra]
	}

	// Create assignment to declare and initialize inlvars.
	as2 := ir.NewAssignListStmt(gd, call.Pos(), ir.OAS2, ir.ToNodes(inlvars), args)
	as2.Def = true
	var as2init ir.Nodes
	for _, name := range inlvars {
		if ir.IsBlank(name) {
			continue
		}
		// TODO(mdempsky): Use inlined position of name.Pos() instead?
		as2init.Append(ir.NewDecl(gd, call.Pos(), ir.ODCL, name))
		name.Defn = as2
	}
	as2.SetInit(as2init)
	init.Append(typecheck.Stmt(gd, as2))

	if !r.delayResults {
		// If not delaying retvars, declare and zero initialize the
		// result variables now.
		for _, name := range retvars {
			// TODO(mdempsky): Use inlined position of name.Pos() instead?
			init.Append(ir.NewDecl(gd, call.Pos(), ir.ODCL, name))
			ras := ir.NewAssignStmt(gd, call.Pos(), name, nil)
			init.Append(typecheck.Stmt(gd, ras))
		}
	}

	// Add an inline mark just before the inlined body.
	// This mark is inline in the code so that it's a reasonable spot
	// to put a breakpoint. Not sure if that's really necessary or not
	// (in which case it could go at the end of the function instead).
	// Note issue 28603.
	init.Append(ir.NewInlineMarkStmt(gd, call.Pos().WithIsStmt(), int64(r.inlTreeIndex)))

	ir.WithFunc(gd, r.curfn, func() {
		if !r.syntheticBody(call.Pos()) {
			assert(gd, r.Bool()) // have body

			r.curfn.Body = r.stmts()
			r.curfn.Endlineno = r.pos()
		}

		// TODO(mdempsky): This shouldn't be necessary. Inlining might
		// read in new function/method declarations, which could
		// potentially be recursively inlined themselves; but we shouldn't
		// need to read in the non-inlined bodies for the declarations
		// themselves. But currently it's an easy fix to #50552.
		readBodies(r.gd, typecheck.Target(r.gd), true)

		// Replace any "return" statements within the function body.
		var edit func(ir.Node) ir.Node
		edit = func(n ir.Node) ir.Node {
			if ret, ok := n.(*ir.ReturnStmt); ok {
				n = typecheck.Stmt(gd, r.inlReturn(ret, retvars))
			}
			ir.EditChildren(n, edit)
			return n
		}
		edit(r.curfn)
	})

	body := r.curfn.Body

	// Reparent any declarations into the caller function.
	for _, name := range r.curfn.Dcl {
		name.Curfn = callerfn

		if name.Class != ir.PAUTO {
			name.SetPos(r.inlPos(name.Pos()))
			name.SetInlFormal(true)
			name.Class = ir.PAUTO
		} else {
			name.SetInlLocal(true)
		}
	}
	callerfn.Dcl = append(callerfn.Dcl, r.curfn.Dcl...)

	body.Append(ir.NewLabelStmt(gd, call.Pos(), r.retlabel))

	res := ir.NewInlinedCallExpr(gd, call.Pos(), body, ir.ToNodes(retvars))
	res.SetInit(init)
	res.SetType(call.Type())
	res.SetTypecheck(1)

	// Inlining shouldn't add any functions to todoBodies.
	tb, _ := gd.NoderTodoBodies.([]*ir.Func)
	assert(gd, len(tb) == 0)

	return res
}

// inlReturn returns a statement that can substitute for the given
// return statement when inlining.
func (r *reader) inlReturn(ret *ir.ReturnStmt, retvars []*ir.Name) *ir.BlockStmt {
	pos := r.inlCall.Pos()

	block := ir.TakeInit(ret)

	if results := ret.Results; len(results) != 0 {
		assert(r.gd, len(retvars) == len(results))

		as2 := ir.NewAssignListStmt(r.gd, pos, ir.OAS2, ir.ToNodes(retvars), ret.Results)

		if r.delayResults {
			for _, name := range retvars {
				// TODO(mdempsky): Use inlined position of name.Pos() instead?
				block.Append(ir.NewDecl(r.gd, pos, ir.ODCL, name))
				name.Defn = as2
			}
		}

		block.Append(as2)
	}

	block.Append(ir.NewBranchStmt(r.gd, pos, ir.OGOTO, r.retlabel))
	return ir.NewBlockStmt(r.gd, pos, block)
}

// expandInline reads in an extra copy of IR to populate
// fn.Inl.Dcl.
func expandInline(gd *base.Invocation, fn *ir.Func, pri pkgReaderIndex) {
	// TODO(mdempsky): Remove this function. It's currently needed by
	// dwarfgen/dwarf.go:preInliningDcls, which requires fn.Inl.Dcl to
	// create abstract function DIEs. But we should be able to provide it
	// with the same information some other way.

	fndcls := len(fn.Dcl)
	topdcls := len(typecheck.Target(gd).Funcs)

	tmpfn := ir.NewFunc(gd, fn.Pos(), fn.Nname.Pos(), fn.Sym(), fn.Type())
	tmpfn.ClosureVars = fn.ClosureVars

	{
		r := pri.asReader(pkgbits.SectionBody, pkgbits.SyncFuncBody)

		// Don't change parameter's Sym/Nname fields.
		r.funarghack = true

		r.funcBody(tmpfn)
	}

	// Move tmpfn's params to fn.Inl.Dcl, and reparent under fn.
	for _, name := range tmpfn.Dcl {
		name.Curfn = fn
	}
	fn.Inl.Dcl = tmpfn.Dcl
	fn.Inl.HaveDcl = true

	// Double check that we didn't change fn.Dcl by accident.
	assert(gd, fndcls == len(fn.Dcl))

	// typecheck.Stmts may have added function literals to
	// typecheck.Target.Decls. Remove them again so we don't risk trying
	// to compile them multiple times.
	typecheck.Target(gd).Funcs = typecheck.Target(gd).Funcs[:topdcls]
}

// @@@ Method wrappers
//
// Here we handle constructing "method wrappers," alternative entry
// points that adapt methods to different calling conventions. Given a
// user-declared method "func (T) M(i int) bool { ... }", there are a
// few wrappers we may need to construct:
//
//	- Implicit dereferencing. Methods declared with a value receiver T
//	  are also included in the method set of the pointer type *T, so
//	  we need to construct a wrapper like "func (recv *T) M(i int)
//	  bool { return (*recv).M(i) }".
//
//	- Promoted methods. If struct type U contains an embedded field of
//	  type T or *T, we need to construct a wrapper like "func (recv U)
//	  M(i int) bool { return recv.T.M(i) }".
//
//	- Method values. If x is an expression of type T, then "x.M" is
//	  roughly "tmp := x; func(i int) bool { return tmp.M(i) }".
//
// At call sites, we always prefer to call the user-declared method
// directly, if known, so wrappers are only needed for indirect calls
// (for example, interface method calls that can't be devirtualized).
// Consequently, we can save some compile time by skipping
// construction of wrappers that are never needed.
//
// Alternatively, because the linker doesn't care which compilation
// unit constructed a particular wrapper, we can instead construct
// them as needed. However, if a wrapper is needed in multiple
// downstream packages, we may end up needing to compile it multiple
// times, costing us more compile time and object file size. (We mark
// the wrappers as DUPOK, so the linker doesn't complain about the
// duplicate symbols.)
//
// The current heuristics we use to balance these trade offs are:
//
//	- For a (non-parameterized) defined type T, we construct wrappers
//	  for *T and any promoted methods on T (and *T) in the same
//	  compilation unit as the type declaration.
//
//	- For a parameterized defined type, we construct wrappers in the
//	  compilation units in which the type is instantiated. We
//	  similarly handle wrappers for anonymous types with methods and
//	  compilation units where their type literals appear in source.
//
//	- Method value expressions are relatively uncommon, so we
//	  construct their wrappers in the compilation units that they
//	  appear in.
//
// Finally, as an opportunistic compile-time optimization, if we know
// a wrapper was constructed in any imported package's compilation
// unit, then we skip constructing a duplicate one. However, currently
// this is only done on a best-effort basis.

// needWrapperTypes / haveWrapperTypes / need|haveMethodValueWrappers
// hold per-compile wrapper-generation queues. Stored on
// *base.Invocation; lazy-init typed slices at append time.

func appendNeedWrapperType(gd *base.Invocation, t *types.Type) {
	s, _ := gd.NoderNeedWrapperTypes.([]*types.Type)
	gd.NoderNeedWrapperTypes = append(s, t)
}

func appendHaveWrapperType(gd *base.Invocation, t *types.Type) {
	s, _ := gd.NoderHaveWrapperTypes.([]*types.Type)
	gd.NoderHaveWrapperTypes = append(s, t)
}

func appendNeedMethodValueWrapper(gd *base.Invocation, w methodValueWrapper) {
	s, _ := gd.NoderNeedMethodValueWrappers.([]methodValueWrapper)
	gd.NoderNeedMethodValueWrappers = append(s, w)
}

func appendHaveMethodValueWrapper(gd *base.Invocation, w methodValueWrapper) {
	s, _ := gd.NoderHaveMethodValueWrappers.([]methodValueWrapper)
	gd.NoderHaveMethodValueWrappers = append(s, w)
}

type methodValueWrapper struct {
	rcvr   *types.Type
	method *types.Field
}

// needWrapper records that wrapper methods may be needed at link
// time.
func (r *reader) needWrapper(typ *types.Type) {
	if typ.IsPtr() {
		return
	}

	// Special case: runtime must define error even if imported packages mention it (#29304).
	forceNeed := typ == types.ErrorType && r.gd.Ctxt.Pkgpath == "runtime"

	// If a type was found in an imported package, then we can assume
	// that package (or one of its transitive dependencies) already
	// generated method wrappers for it.
	if r.importedDef() && !forceNeed {
		appendHaveWrapperType(r.gd, typ)
	} else {
		appendNeedWrapperType(r.gd, typ)
	}
}

// importedDef reports whether r is reading from an imported and
// non-generic element.
//
// If a type was found in an imported package, then we can assume that
// package (or one of its transitive dependencies) already generated
// method wrappers for it.
//
// Exception: If we're instantiating an imported generic type or
// function, we might be instantiating it with type arguments not
// previously seen before.
//
// TODO(mdempsky): Distinguish when a generic function or type was
// instantiated in an imported package so that we can add types to
// haveWrapperTypes instead.
func (r *reader) importedDef() bool {
	return r.p != localPkgReader(r.gd) && !r.hasTypeParams()
}

// MakeWrappers constructs all wrapper methods needed for the target
// compilation unit.
func MakeWrappers(gd *base.Invocation, target *ir.Package) {
	// always generate a wrapper for error.Error (#29304)
	appendNeedWrapperType(gd, types.ErrorType)

	seen := make(map[string]*types.Type)

	if have, _ := gd.NoderHaveWrapperTypes.([]*types.Type); have != nil {
		for _, typ := range have {
			wrapType(gd, typ, target, seen, false)
		}
		gd.NoderHaveWrapperTypes = nil
	}

	if need, _ := gd.NoderNeedWrapperTypes.([]*types.Type); need != nil {
		for _, typ := range need {
			wrapType(gd, typ, target, seen, true)
		}
		gd.NoderNeedWrapperTypes = nil
	}

	if have, _ := gd.NoderHaveMethodValueWrappers.([]methodValueWrapper); have != nil {
		for _, wrapper := range have {
			wrapMethodValue(gd, wrapper.rcvr, wrapper.method, target, false)
		}
		gd.NoderHaveMethodValueWrappers = nil
	}

	if need, _ := gd.NoderNeedMethodValueWrappers.([]methodValueWrapper); need != nil {
		for _, wrapper := range need {
			wrapMethodValue(gd, wrapper.rcvr, wrapper.method, target, true)
		}
		gd.NoderNeedMethodValueWrappers = nil
	}
}

func wrapType(gd *base.Invocation, typ *types.Type, target *ir.Package, seen map[string]*types.Type, needed bool) {
	key := typ.LinkString()
	if prev := seen[key]; prev != nil {
		if !types.Identical(typ, prev) {
			gd.Fatalf("collision: types %v and %v have link string %q", typ, prev, key)
		}
		return
	}
	seen[key] = typ

	if !needed {
		// Only called to add to 'seen'.
		return
	}

	if !typ.IsInterface() {
		typecheck.CalcMethods(typ)
	}
	for _, meth := range typ.AllMethods() {
		if meth.Sym.IsBlank() || !meth.IsMethod() {
			gd.FatalfAt(meth.Pos, "invalid method: %v", meth)
		}

		methodWrapper(gd, 0, typ, meth, target)

		// For non-interface types, we also want *T wrappers.
		if !typ.IsInterface() {
			methodWrapper(gd, 1, typ, meth, target)

			// For not-in-heap types, *T is a scalar, not pointer shaped,
			// so the interface wrappers use **T.
			if typ.NotInHeap() {
				methodWrapper(gd, 2, typ, meth, target)
			}
		}
	}
}

func methodWrapper(gd *base.Invocation, derefs int, tbase *types.Type, method *types.Field, target *ir.Package) {
	wrapper := tbase
	for i := 0; i < derefs; i++ {
		wrapper = types.NewPtr(wrapper)
	}

	sym := ir.MethodSym(gd, wrapper, method.Sym)
	gd.Assertf(!types.SiggenIn(gd, sym), "already generated wrapper %v", sym)
	types.SetSiggenIn(gd, sym)

	wrappee := method.Type.Recv().Type
	if types.Identical(wrapper, wrappee) ||
		!types.IsMethodApplicable(wrapper, method) ||
		!reflectdata.NeedEmit(gd, tbase) {
		return
	}

	// TODO(mdempsky): Use method.Pos instead?
	pos := gd.AutogeneratedPos

	fn := newWrapperFunc(gd, pos, sym, wrapper, method)

	var recv ir.Node = fn.Nname.Type().Recv().Nname.(*ir.Name)

	// For simple *T wrappers around T methods, panicwrap produces a
	// nicer panic message.
	if wrapper.IsPtr() && types.Identical(wrapper.Elem(), wrappee) {
		cond := ir.NewBinaryExpr(gd, pos, ir.OEQ, recv, types.BuiltinPkg(gd).Lookup("nil").Def.(ir.Node))
		then := []ir.Node{ir.NewCallExpr(gd, pos, ir.OCALL, typecheck.LookupRuntime(gd, "panicwrap"), nil)}
		fn.Body.Append(ir.NewIfStmt(gd, pos, cond, then, nil))
	}

	// typecheck will add one implicit deref, if necessary,
	// but not-in-heap types require more for their **T wrappers.
	for i := 1; i < derefs; i++ {
		recv = Implicit(ir.NewStarExpr(gd, pos, recv))
	}

	addTailCall(gd, pos, fn, recv, method)

	finishWrapperFunc(gd, fn, target)
}

func wrapMethodValue(gd *base.Invocation, recvType *types.Type, method *types.Field, target *ir.Package, needed bool) {
	sym := ir.MethodSymSuffix(gd, recvType, method.Sym, "-fm")
	if sym.Uniq() {
		return
	}
	sym.SetUniq(true)

	// TODO(mdempsky): Use method.Pos instead?
	pos := gd.AutogeneratedPos

	fn := newWrapperFunc(gd, pos, sym, nil, method)
	sym.Def = fn.Nname

	// Declare and initialize variable holding receiver.
	recv := ir.NewHiddenParam(gd, pos, fn, typecheck.Lookup(gd, ".this"), recvType)

	if !needed {
		return
	}

	addTailCall(gd, pos, fn, recv, method)

	finishWrapperFunc(gd, fn, target)
}

func newWrapperFunc(gd *base.Invocation, pos src.XPos, sym *types.Sym, wrapper *types.Type, method *types.Field) *ir.Func {
	sig := newWrapperType(wrapper, method)
	fn := ir.NewFunc(gd, pos, pos, sym, sig)
	fn.DeclareParams(gd, true)
	fn.SetDupok(true) // TODO(mdempsky): Leave unset for local, non-generic wrappers?

	return fn
}

func finishWrapperFunc(gd *base.Invocation, fn *ir.Func, target *ir.Package) {
	ir.WithFunc(gd, fn, func() {
		typecheck.Stmts(gd, fn.Body)
	})

	// We generate wrappers after the global inlining pass,
	// so we're responsible for applying inlining ourselves here.
	// TODO(prattmic): plumb PGO.
	interleaved.DevirtualizeAndInlineFunc(gd, fn, nil)

	// The body of wrapper function after inlining may reveal new ir.OMETHVALUE node,
	// we don't know whether wrapper function has been generated for it or not, so
	// generate one immediately here.
	//
	// Further, after CL 492017, function that construct closures is allowed to be inlined,
	// even though the closure itself can't be inline. So we also need to visit body of any
	// closure that we see when visiting body of the wrapper function.
	ir.VisitFuncAndClosures(fn, func(n ir.Node) {
		if n, ok := n.(*ir.SelectorExpr); ok && n.Op() == ir.OMETHVALUE {
			wrapMethodValue(gd, n.X.Type(), n.Selection, target, true)
		}
	})

	fn.Nname.Defn = fn
	target.Funcs = append(target.Funcs, fn)
}

// newWrapperType returns a copy of the given signature type, but with
// the receiver parameter type substituted with recvType.
// If recvType is nil, newWrapperType returns a signature
// without a receiver parameter.
func newWrapperType(recvType *types.Type, method *types.Field) *types.Type {
	clone := func(params []*types.Field) []*types.Field {
		res := make([]*types.Field, len(params))
		for i, param := range params {
			res[i] = types.NewField(param.Pos, param.Sym, param.Type)
			res[i].SetIsDDD(param.IsDDD())
		}
		return res
	}

	sig := method.Type

	var recv *types.Field
	if recvType != nil {
		recv = types.NewField(sig.Recv().Pos, sig.Recv().Sym, recvType)
	}
	params := clone(sig.Params())
	results := clone(sig.Results())

	// gd Phase G.2.1: preserve method.Type's exact form (extended or
	// not) — addTailCall forwards args 1:1, so the wrapper sig must
	// have the same param count as method.Type. NewSignature would
	// re-extend if cloned params lack outBuf names, producing a
	// mismatch with a stock method.Type.
	return types.NewSignatureAsIs(recv, params, results, sig.GdReturnOutBuf())
}

func addTailCall(gd *base.Invocation, pos src.XPos, fn *ir.Func, recv ir.Node, method *types.Field) {
	sig := fn.Nname.Type()
	args := make([]ir.Node, sig.NumParams())
	for i, param := range sig.Params() {
		args[i] = param.Nname.(*ir.Name)
	}

	dot := typecheck.XDotMethod(gd, pos, recv, method.Sym, true)
	// gd Phase G.2.1: drop the wrapper's own outBuf args; tcCall's
	// FillOutBufArgs re-appends nils matching dot's actual NumOutBufs
	// (which may differ from the wrapper's sig — e.g. typechecker
	// re-built method type for the receiver's method set). Filter by
	// IsOutBufParam so we keep variadic slices (which sit AFTER the
	// outBuf cluster in the param list) intact.
	if sig.NumOutBufs() > 0 {
		filtered := args[:0]
		for i, p := range sig.Params() {
			if p.IsOutBufParam() {
				continue
			}
			filtered = append(filtered, args[i])
		}
		args = filtered
	}
	call := typecheck.Call(gd, pos, dot, args, method.Type.IsVariadic()).(*ir.CallExpr)

	if recv.Type() != nil && recv.Type().IsPtr() && method.Type.Recv().Type.IsPtr() &&
		method.Embedded != 0 && !types.IsInterfaceMethod(method.Type) &&
		!unifiedHaveInlineBody(gd, ir.MethodExprName(dot).Func) &&
		!(gd.Ctxt.Arch.Name == "ppc64le" && gd.Ctxt.Flag_dynlink) {
		if gd.Debug.TailCall != 0 {
			gd.WarnfAt(fn.Nname.Type().Recv().Type.Elem().Pos(), "tail call emitted for the method %v wrapper", method.Nname)
		}
		// Prefer OTAILCALL to reduce code size (except the case when the called method can be inlined).
		fn.Body.Append(ir.NewTailCallStmt(gd, pos, call))
		return
	}

	fn.SetWrapper(true)

	if method.Type.NumResults() == 0 {
		fn.Body.Append(call)
		return
	}

	ret := ir.NewReturnStmt(gd, pos, nil)
	ret.Results = []ir.Node{call}
	fn.Body.Append(ret)
}

func setBasePos(gd *base.Invocation, pos src.XPos) {
	// Set the position for any error messages we might print (e.g. too large types).
	gd.Pos = pos
}

// dictParamName is the name of the synthetic dictionary parameter
// added to shaped functions.
//
// N.B., this variable name is known to Delve:
// https://github.com/go-delve/delve/blob/cb91509630529e6055be845688fd21eb89ae8714/pkg/proc/eval.go#L28
const dictParamName = typecheck.LocalDictName

// shapeSig returns a copy of fn's signature, except adding a
// dictionary parameter and promoting the receiver parameter (if any)
// to a normal parameter.
//
// The parameter types.Fields are all copied too, so their Nname
// fields can be initialized for use by the shape function.
func shapeSig(gd *base.Invocation, fn *ir.Func, dict *readerDict) *types.Type {
	sig := fn.Nname.Type()
	oldRecv := sig.Recv()

	var recv *types.Field
	if oldRecv != nil {
		recv = types.NewField(oldRecv.Pos, oldRecv.Sym, oldRecv.Type)
	}

	params := make([]*types.Field, 1+sig.NumParams())
	params[0] = types.NewField(fn.Pos(), fn.Sym().Pkg.Lookup(dictParamName), types.NewPtr(dict.varType()))
	for i, param := range sig.Params() {
		d := types.NewField(param.Pos, param.Sym, param.Type)
		d.SetIsDDD(param.IsDDD())
		params[1+i] = d
	}

	results := make([]*types.Field, sig.NumResults())
	for i, result := range sig.Results() {
		results[i] = types.NewField(result.Pos, result.Sym, result.Type)
	}

	return types.NewSignature(gd, recv, params, results)
}
