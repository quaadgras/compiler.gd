// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflectdata

import (
	"encoding/binary"
	"fmt"
	"internal/abi"
	"slices"
	"sort"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/bitvec"
	"cmd/compile/internal/compare"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/rttype"
	"cmd/compile/internal/staticdata"
	"cmd/compile/internal/typebits"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/src"
)

type ptabEntry struct {
	s *types.Sym
	t *types.Type
}

// runtime interface and reflection data structures live on Invocation:
// gd.ReflectdataSignatMu / .ReflectdataSignatSet (lazy-init) /
// .ReflectdataSignatSlice / .ReflectdataGcsymMu / .ReflectdataGcsymSet
// (lazy-init). Helpers below hide the type assertions.

func signatSet(gd *base.Invocation) map[*types.Type]struct{} {
	m, _ := gd.ReflectdataSignatSet.(map[*types.Type]struct{})
	if m == nil {
		m = make(map[*types.Type]struct{})
		gd.ReflectdataSignatSet = m
	}
	return m
}

func gcsymSet(gd *base.Invocation) map[*types.Type]struct{} {
	m, _ := gd.ReflectdataGcsymSet.(map[*types.Type]struct{})
	if m == nil {
		m = make(map[*types.Type]struct{})
		gd.ReflectdataGcsymSet = m
	}
	return m
}

type typeSig struct {
	name  *types.Sym
	isym  *obj.LSym
	tsym  *obj.LSym
	type_ *types.Type
	mtype *types.Type
	// origType is the concrete method's unmodified signature (f.Type
	// as it appears on the defining type). type_ and mtype are
	// computed via NewMethodType which runs before escape analysis
	// and therefore captures stale (empty) param.Note values — the
	// gd escape-bits mask reader needs the updated Notes the escape
	// pass writes later on origType's fields.
	origType *types.Type
	// methodFn is the *ir.Func for the method when the method has a
	// compiled body in the current package. Used to pull the
	// per-argument escape-bits mask (fn.EscMask) populated by the
	// escape-analysis pass. nil when the method comes from an
	// imported package (notes travel via origType.Params()[].Note in
	// that case).
	methodFn *ir.Func
}

// methodFieldEscMask returns the per-argument escape mask for the
// method represented by sig, preferring the locally-compiled Func's
// EscMask (which was populated by escape analysis) and falling back to
// decoding the imported notes on origType when the method lives in
// another package.
func methodFieldEscMask(sig *typeSig) uint64 {
	if sig == nil {
		return 0
	}
	if sig.methodFn != nil {
		// fn.EscMask encodes bit k+1 for the k-th RecvParam
		// (receiver at bit 1, first real arg at bit 2, ...).
		// The iface-dispatch mask hides the receiver: bit k+1 is
		// the k-th non-receiver arg. Shift right by one and clear
		// the newly-exposed low bit (which was "receiver escapes",
		// not meaningful at the dispatch site — the receiver is
		// always materialized to match the interface shape).
		return (sig.methodFn.EscMask >> 1) & ^uint64(1)
	}
	if sig.origType != nil {
		return computeMethodEscMask(sig.origType)
	}
	return 0
}

// pendingItabMasks records per-method itab EscMask slots that
// writeITab has reserved but whose value must be filled in only after
// escape analysis has populated ir.Func.EscMask. writeITab runs during
// noder (well before escape) to materialize static itabs referenced
// from generated data, so reading fn.EscMask at that point returns 0.
// We defer the actual value emission to FinalizeItabMasks, called
// after escape.Funcs by gc.Main.
type pendingItabMask struct {
	lsym   *obj.LSym
	offset int
	fn     *ir.Func // nil for imported methods; mask stays 0
	// origType captures the concrete method signature at writeITab
	// time. When fn is nil (imported method) we re-derive the mask
	// from origType.Params[].Note at finalize time — by then the
	// importer has had time to propagate upstream escape tags.
	origType *types.Type
}

// pendingItabMasks lives on Invocation (gd.ReflectdataPendingItabMasks)
// — per-compile EscMask install queue. See FinalizeItabMasks.
func pendingItabMasks(gd *base.Invocation) []pendingItabMask {
	s, _ := gd.ReflectdataPendingItabMasks.([]pendingItabMask)
	return s
}

// FinalizeItabMasks writes the per-method escape-bits mask into every
// itab symbol that writeITab previously reserved slot space for. Must
// run after escape analysis completes so that fn.EscMask reflects the
// real per-parameter escape profile. See doc/gd/escape-bits.md §6a.
//
// ir.Func.Esc() value 3 corresponds to escFuncTagged (see
// cmd/compile/internal/escape/escape.go) — the "we ran escape
// analysis and populated notes / EscMask for this fn" marker. Reading
// fn.EscMask on a fn that was never analyzed (e.g. imported-package
// method wrapper accessible via f.Nname but whose body lives
// elsewhere) returns the zero value — which means "nothing escapes"
// in our encoding, the opposite of the pessimistic default. Fall back
// to origType's imported notes in that case so we don't accidentally
// clear escape bits we actually need.
const escFuncTagged = 3

func FinalizeItabMasks(gd *base.Invocation) {
	for _, p := range pendingItabMasks(gd) {
		// Phase F4 install. Write a SymPtr reloc to the synth funcsym
		// with bit 0 set (dynamic-mask discriminator). The PkgIdxSelf
		// hash this introduces is incompatible with content-addressable
		// itab dedup; writeITab drops AttrContentAddressable for the
		// entire itab when install is enabled, so the linker dedups by
		// name (DUPOK) instead. Disable via `-d=gdforwarderdisable=1`.
		if gd.Debug.GdForwarderDisable == 0 && p.fn != nil && p.fn.GdForwarder != nil && p.fn.GdForwarder.SyntheticComputeFn != nil {
			if hasPointerBearingArg(p.fn) {
				cfSym := staticdata.FuncLinksym(gd, p.fn.GdForwarder.SyntheticComputeFn.Nname)
				objw.SymPtr(gd, p.lsym, p.offset, cfSym, 1)
				continue
			}
		}
		var mask uint64
		switch {
		case p.fn != nil && p.fn.Esc() >= escFuncTagged:
			// fn.EscMask carries receiver at bit 1 and args at
			// bits 2.. — the iface dispatch mask hides the
			// receiver, so shift right once and clear the newly
			// exposed low bit (which was "receiver escapes",
			// meaningless at the dispatch site).
			mask = (p.fn.EscMask >> 1) & ^uint64(1)
		case p.origType != nil:
			mask = computeMethodEscMask(p.origType)
		}
		objw.UintN(gd, p.lsym, p.offset, mask, 8)
	}
	gd.ReflectdataPendingItabMasks = nil
}

// hasPointerBearingArg reports whether fn has at least one
// non-receiver param of pointer kind (or unsafe.Pointer / chan /
// map / iface / func — any type that can carry an EscCandidate
// pointer). Used by Phase F4 to gate install: forwarder methods
// with no pointer-bearing args have no candidate args at the wrap
// site, so the mask is never read, and installing the synth's
// funcsym there is dead but expensive.
func hasPointerBearingArg(fn *ir.Func) bool {
	if fn == nil || fn.Type() == nil {
		return false
	}
	for _, p := range fn.Type().Params() {
		t := p.Type
		if t == nil {
			continue
		}
		switch t.Kind() {
		case types.TPTR, types.TUNSAFEPTR, types.TINTER, types.TFUNC, types.TMAP, types.TCHAN, types.TSLICE:
			return true
		}
		// Struct-by-value with pointer-bearing fields counts too:
		// the wrap routes such structs through tagHole when any
		// inner field is pointer-shaped.
		if t.HasPointers() {
			return true
		}
	}
	return false
}

func commonSize() int { return int(rttype.Type.Size()) } // Sizeof(runtime._type{})

func uncommonSize(gd *base.Invocation, t *types.Type) int { // Sizeof(runtime.uncommontype{})
	if t.Sym() == nil && len(methods(gd, t)) == 0 {
		return 0
	}
	return int(rttype.UncommonType.Size())
}

func makefield(name string, t *types.Type) *types.Field {
	sym := (*types.Pkg)(nil).Lookup(name)
	return types.NewField(src.NoXPos, sym, t)
}

// computeMethodEscMask returns the gd escape-bits mask for a method,
// skipping the receiver so bit k+1 aligns with the k-th argument the
// iface dispatch site sees. Bit 0 stays reserved.
func computeMethodEscMask(sig *types.Type) uint64 {
	if sig == nil || sig.Kind() != types.TFUNC {
		return 0
	}
	var mask uint64
	for i, f := range sig.Params() {
		if i >= 63 {
			break
		}
		note := f.Note
		if !strings.HasPrefix(note, "esc:") {
			mask |= 1 << uint(i+1)
			continue
		}
		if len(note) >= 5 && note[4] != 0 {
			mask |= 1 << uint(i+1)
		}
	}
	return mask
}

// methods returns the methods of the non-interface type t, sorted by name.
// Generates stub functions as needed.
func methods(gd *base.Invocation, t *types.Type) []*typeSig {
	if t.HasShape() {
		// Shape types have no methods.
		return nil
	}
	// method type
	mt := types.ReceiverBaseType(t)

	if mt == nil {
		return nil
	}
	typecheck.CalcMethods(mt)

	// make list of methods for t,
	// generating code if necessary.
	var ms []*typeSig
	for _, f := range mt.AllMethods() {
		if f.Sym == nil {
			gd.Fatalf("method with no sym on %v", mt)
		}
		if !f.IsMethod() {
			gd.Fatalf("non-method on %v method %v %v", mt, f.Sym, f)
		}
		if f.Type.Recv() == nil {
			gd.Fatalf("receiver with no type on %v method %v %v", mt, f.Sym, f)
		}
		if f.Nointerface() && !t.IsFullyInstantiated() {
			// Skip creating method wrappers if f is nointerface. But, if
			// t is an instantiated type, we still have to call
			// methodWrapper, because methodWrapper generates the actual
			// generic method on the type as well.
			continue
		}

		// get receiver type for this particular method.
		// if pointer receiver but non-pointer t and
		// this is not an embedded pointer inside a struct,
		// method does not apply.
		if !types.IsMethodApplicable(t, f) {
			continue
		}

		var mfn *ir.Func
		if f.Nname != nil {
			if name, ok := f.Nname.(*ir.Name); ok {
				mfn = name.Func
			}
		}
		sig := &typeSig{
			name:     f.Sym,
			isym:     methodWrapper(gd, t, f, true),
			tsym:     methodWrapper(gd, t, f, false),
			type_:    typecheck.NewMethodType(gd, f.Type, t),
			mtype:    typecheck.NewMethodType(gd, f.Type, nil),
			origType: f.Type,
			methodFn: mfn,
		}
		if f.Nointerface() {
			// In the case of a nointerface method on an instantiated
			// type, don't actually append the typeSig.
			continue
		}
		ms = append(ms, sig)
	}

	return ms
}

// imethods returns the methods of the interface type t, sorted by name.
func imethods(gd *base.Invocation, t *types.Type) []*typeSig {
	var methods []*typeSig
	for _, f := range t.AllMethods() {
		if f.Type.Kind() != types.TFUNC || f.Sym == nil {
			continue
		}
		if f.Sym.IsBlank() {
			gd.Fatalf("unexpected blank symbol in interface method set")
		}
		if n := len(methods); n > 0 {
			last := methods[n-1]
			if types.CompareSyms(last.name, f.Sym) >= 0 {
				gd.Fatalf("sigcmp vs sortinter %v %v", last.name, f.Sym)
			}
		}

		sig := &typeSig{
			name:  f.Sym,
			mtype: f.Type,
			type_: typecheck.NewMethodType(gd, f.Type, nil),
		}
		methods = append(methods, sig)

		// NOTE(rsc): Perhaps an oversight that
		// IfaceType.Method is not in the reflect data.
		// Generate the method body, so that compiled
		// code can refer to it.
		methodWrapper(gd, t, f, false)
	}

	return methods
}

func dimportpath(gd *base.Invocation, p *types.Pkg) {
	if p.Pathsym != nil {
		return
	}

	if p == types.LocalPkg(gd) && gd.Ctxt.Pkgpath == "" {
		panic("missing pkgpath")
	}

	// If we are compiling the runtime package, there are two runtime packages around
	// -- localpkg and Pkgs.Runtime. We don't want to produce import path symbols for
	// both of them, so just produce one for localpkg.
	if gd.Ctxt.Pkgpath == "runtime" && p == ir.Pkgs(gd).Runtime {
		return
	}

	s := gd.Ctxt.Lookup("type:.importpath." + p.Prefix + ".")
	ot := dnameData(gd, s, 0, p.Path, "", nil, false, false)
	objw.Global(gd, s, int32(ot), obj.DUPOK|obj.RODATA)
	s.Set(obj.AttrContentAddressable, true)
	p.Pathsym = s
}

func dgopkgpath(gd *base.Invocation, c rttype.Cursor, pkg *types.Pkg) {
	c = c.Field("Bytes")
	if pkg == nil {
		c.WritePtr(nil)
		return
	}

	dimportpath(gd, pkg)
	c.WritePtr(pkg.Pathsym)
}

// dgopkgpathOff writes an offset relocation to the pkg path symbol to c.
func dgopkgpathOff(gd *base.Invocation, c rttype.Cursor, pkg *types.Pkg) {
	if pkg == nil {
		c.WriteInt32(0)
		return
	}

	dimportpath(gd, pkg)
	c.WriteSymPtrOff(pkg.Pathsym, false)
}

// dnameField dumps a reflect.name for a struct field.
func dnameField(gd *base.Invocation, c rttype.Cursor, spkg *types.Pkg, ft *types.Field) {
	if !types.IsExported(ft.Sym.Name) && ft.Sym.Pkg != spkg {
		gd.Fatalf("package mismatch for %v", ft.Sym)
	}
	nsym := dname(gd, ft.Sym.Name, ft.Note, nil, types.IsExported(ft.Sym.Name), ft.Embedded != 0)
	c.Field("Bytes").WritePtr(nsym)
}

// dnameData writes the contents of a reflect.name into s at offset ot.
func dnameData(gd *base.Invocation, s *obj.LSym, ot int, name, tag string, pkg *types.Pkg, exported, embedded bool) int {
	if len(name) >= 1<<29 {
		gd.Fatalf("name too long: %d %s...", len(name), name[:1024])
	}
	if len(tag) >= 1<<29 {
		gd.Fatalf("tag too long: %d %s...", len(tag), tag[:1024])
	}
	var nameLen [binary.MaxVarintLen64]byte
	nameLenLen := binary.PutUvarint(nameLen[:], uint64(len(name)))
	var tagLen [binary.MaxVarintLen64]byte
	tagLenLen := binary.PutUvarint(tagLen[:], uint64(len(tag)))

	// Encode name and tag. See reflect/type.go for details.
	var bits byte
	l := 1 + nameLenLen + len(name)
	if exported {
		bits |= 1 << 0
	}
	if len(tag) > 0 {
		l += tagLenLen + len(tag)
		bits |= 1 << 1
	}
	if pkg != nil {
		bits |= 1 << 2
	}
	if embedded {
		bits |= 1 << 3
	}
	b := make([]byte, l)
	b[0] = bits
	copy(b[1:], nameLen[:nameLenLen])
	copy(b[1+nameLenLen:], name)
	if len(tag) > 0 {
		tb := b[1+nameLenLen+len(name):]
		copy(tb, tagLen[:tagLenLen])
		copy(tb[tagLenLen:], tag)
	}

	ot = int(s.WriteBytes(gd.Ctxt, int64(ot), b))

	if pkg != nil {
		c := rttype.NewCursor(gd, s, int64(ot), types.Types[types.TUINT32])
		dgopkgpathOff(gd, c, pkg)
		ot += 4
	}

	return ot
}

// dname creates a reflect.name for a struct field or method.
// (Counter on Invocation: see base/invocation.go Dnamecount.)
func dname(gd *base.Invocation, name, tag string, pkg *types.Pkg, exported, embedded bool) *obj.LSym {
	// Write out data as "type:." to signal two things to the
	// linker, first that when dynamically linking, the symbol
	// should be moved to a relro section, and second that the
	// contents should not be decoded as a type.
	sname := "type:.namedata."
	if pkg == nil {
		// In the common case, share data with other packages.
		if name == "" {
			if exported {
				sname += "-noname-exported." + tag
			} else {
				sname += "-noname-unexported." + tag
			}
		} else {
			if exported {
				sname += name + "." + tag
			} else {
				sname += name + "-" + tag
			}
		}
	} else {
		// TODO(mdempsky): We should be able to share these too (except
		// maybe when dynamic linking).
		sname = fmt.Sprintf("%s%s.%d", sname, types.LocalPkg(gd).Prefix, gd.Dnamecount)
		gd.Dnamecount++
	}
	if embedded {
		sname += ".embedded"
	}
	s := gd.Ctxt.Lookup(sname)
	if len(s.P) > 0 {
		return s
	}
	ot := dnameData(gd, s, 0, name, tag, pkg, exported, embedded)
	objw.Global(gd, s, int32(ot), obj.DUPOK|obj.RODATA)
	s.Set(obj.AttrContentAddressable, true)
	return s
}

// dextratype dumps the fields of a runtime.uncommontype.
// dataAdd is the offset in bytes after the header where the
// backing array of the []method field should be written.
func dextratype(gd *base.Invocation, lsym *obj.LSym, off int64, t *types.Type, dataAdd int) {
	m := methods(gd, t)
	if t.Sym() == nil && len(m) == 0 {
		gd.Fatalf("extra requested of type with no extra info %v", t)
	}
	noff := types.RoundUp(off, int64(types.PtrSize))
	if noff != off {
		gd.Fatalf("unexpected alignment in dextratype for %v", t)
	}

	for _, a := range m {
		writeType(gd, a.type_)
	}

	c := rttype.NewCursor(gd, lsym, off, rttype.UncommonType)
	dgopkgpathOff(gd, c.Field("PkgPath"), typePkg(t))

	dataAdd += uncommonSize(gd, t)
	mcount := len(m)
	if mcount != int(uint16(mcount)) {
		gd.Fatalf("too many methods on %v: %d", t, mcount)
	}
	xcount := sort.Search(mcount, func(i int) bool { return !types.IsExported(m[i].name.Name) })
	if dataAdd != int(uint32(dataAdd)) {
		gd.Fatalf("methods are too far away on %v: %d", t, dataAdd)
	}

	c.Field("Mcount").WriteUint16(uint16(mcount))
	c.Field("Xcount").WriteUint16(uint16(xcount))
	c.Field("Moff").WriteUint32(uint32(dataAdd))
	// Note: there is an unused uint32 field here.

	// Write the backing array for the []method field.
	array := rttype.NewArrayCursor(gd, lsym, off+int64(dataAdd), rttype.Method, mcount)
	for i, a := range m {
		exported := types.IsExported(a.name.Name)
		var pkg *types.Pkg
		if !exported && a.name.Pkg != typePkg(t) {
			pkg = a.name.Pkg
		}
		nsym := dname(gd, a.name.Name, "", pkg, exported, false)

		e := array.Elem(i)
		e.Field("Name").WriteSymPtrOff(nsym, false)
		dmethodptrOff(e.Field("Mtyp"), writeType(gd, a.mtype))
		dmethodptrOff(e.Field("Ifn"), a.isym)
		dmethodptrOff(e.Field("Tfn"), a.tsym)
	}
}

func typePkg(t *types.Type) *types.Pkg {
	tsym := t.Sym()
	if tsym == nil {
		switch t.Kind() {
		case types.TARRAY, types.TSLICE, types.TPTR, types.TCHAN:
			if t.Elem() != nil {
				tsym = t.Elem().Sym()
			}
		}
	}
	// gd: BuiltinPkg is per-Invocation (different pointer per gd);
	// compare by Path which is stable.
	if tsym != nil && tsym.Pkg != nil && tsym.Pkg.Path != "go.builtin" {
		return tsym.Pkg
	}
	return nil
}

func dmethodptrOff(c rttype.Cursor, x *obj.LSym) {
	c.WriteInt32(0)
	c.Reloc(obj.Reloc{Type: objabi.R_METHODOFF, Sym: x})
}

var kinds = []abi.Kind{
	types.TINT:        abi.Int,
	types.TUINT:       abi.Uint,
	types.TINT8:       abi.Int8,
	types.TUINT8:      abi.Uint8,
	types.TINT16:      abi.Int16,
	types.TUINT16:     abi.Uint16,
	types.TINT32:      abi.Int32,
	types.TUINT32:     abi.Uint32,
	types.TINT64:      abi.Int64,
	types.TUINT64:     abi.Uint64,
	types.TUINTPTR:    abi.Uintptr,
	types.TFLOAT32:    abi.Float32,
	types.TFLOAT64:    abi.Float64,
	types.TBOOL:       abi.Bool,
	types.TSTRING:     abi.String,
	types.TPTR:        abi.Pointer,
	types.TSTRUCT:     abi.Struct,
	types.TINTER:      abi.Interface,
	types.TCHAN:       abi.Chan,
	types.TMAP:        abi.Map,
	types.TARRAY:      abi.Array,
	types.TSLICE:      abi.Slice,
	types.TFUNC:       abi.Func,
	types.TCOMPLEX64:  abi.Complex64,
	types.TCOMPLEX128: abi.Complex128,
	types.TUNSAFEPTR:  abi.UnsafePointer,
}

func ABIKindOfType(t *types.Type) abi.Kind {
	return kinds[t.Kind()]
}

var (
	memhashvarlen  *obj.LSym
	memequalvarlen *obj.LSym
)

// dcommontype dumps the contents of a reflect.rtype (runtime._type) to c.
func dcommontype(gd *base.Invocation, c rttype.Cursor, t *types.Type) {
	types.CalcSize(t)
	eqfunc := geneq(gd, t)

	sptrWeak := true
	var sptr *obj.LSym
	if !t.IsPtr() || t.IsPtrElem() {
		tptr := types.NewPtr(t)
		if t.Sym() != nil || methods(gd, tptr) != nil {
			sptrWeak = false
		}
		sptr = writeType(gd, tptr)
	}

	gcsym, onDemand, ptrdata := dgcsym(gd, t, true, true)
	if !onDemand {
		delete(gcsymSet(gd), t)
	}

	// ../../../../reflect/type.go:/^type.rtype
	// actual type structure
	//	type rtype struct {
	//		size          uintptr
	//		ptrdata       uintptr
	//		hash          uint32
	//		tflag         tflag
	//		align         uint8
	//		fieldAlign    uint8
	//		kind          uint8
	//		equal         func(unsafe.Pointer, unsafe.Pointer) bool
	//		gcdata        *byte
	//		str           nameOff
	//		ptrToThis     typeOff
	//	}
	c.Field("Size_").WriteUintptr(uint64(t.Size()))
	c.Field("PtrBytes").WriteUintptr(uint64(ptrdata))
	c.Field("Hash").WriteUint32(types.TypeHash(t))

	var tflag abi.TFlag
	if uncommonSize(gd, t) != 0 {
		tflag |= abi.TFlagUncommon
	}
	if t.Sym() != nil && t.Sym().Name != "" {
		tflag |= abi.TFlagNamed
	}
	if compare.IsRegularMemory(t) {
		tflag |= abi.TFlagRegularMemory
	}
	if onDemand {
		tflag |= abi.TFlagGCMaskOnDemand
	}

	exported := false
	p := t.NameString()
	// If we're writing out type T,
	// we are very likely to write out type *T as well.
	// Use the string "*T"[1:] for "T", so that the two
	// share storage. This is a cheap way to reduce the
	// amount of space taken up by reflect strings.
	if !strings.HasPrefix(p, "*") {
		p = "*" + p
		tflag |= abi.TFlagExtraStar
		if t.Sym() != nil {
			exported = types.IsExported(t.Sym().Name)
		}
	} else {
		if t.Elem() != nil && t.Elem().Sym() != nil {
			exported = types.IsExported(t.Elem().Sym().Name)
		}
	}
	if types.IsDirectIface(t) {
		tflag |= abi.TFlagDirectIface
	}
	if types.IsInlineIface(t) {
		tflag |= abi.TFlagInlineIface
	}
	if types.IsSpreadIface(t) {
		tflag |= abi.TFlagSpreadIface
	}

	if tflag != abi.TFlag(uint8(tflag)) {
		// this should optimize away completely
		panic("Unexpected change in size of abi.TFlag")
	}
	c.Field("TFlag").WriteUint8(uint8(tflag))

	// runtime (and common sense) expects alignment to be a power of two.
	i := int(uint8(t.Alignment()))

	if i == 0 {
		i = 1
	}
	if i&(i-1) != 0 {
		gd.Fatalf("invalid alignment %d for %v", uint8(t.Alignment()), t)
	}
	c.Field("Align_").WriteUint8(uint8(t.Alignment()))
	c.Field("FieldAlign_").WriteUint8(uint8(t.Alignment()))

	c.Field("Kind_").WriteUint8(uint8(ABIKindOfType(t)))

	c.Field("Equal").WritePtr(eqfunc)
	c.Field("GCData").WritePtr(gcsym)

	nsym := dname(gd, p, "", nil, exported, false)
	c.Field("Str").WriteSymPtrOff(nsym, false)
	c.Field("PtrToThis").WriteSymPtrOff(sptr, sptrWeak)
}

// TrackSym returns the symbol for tracking use of field/method f, assumed
// to be a member of struct/interface type t.
func TrackSym(gd *base.Invocation, t *types.Type, f *types.Field) *obj.LSym {
	return gd.PkgLinksym("go:track", t.LinkString()+"."+f.Sym.Name, obj.ABI0)
}

func TypeSymPrefix(gd *base.Invocation, prefix string, t *types.Type) *types.Sym {
	p := prefix + "." + t.LinkString()
	s := types.TypeSymLookup(gd, p)

	// This function is for looking up type-related generated functions
	// (e.g. eq and hash). Make sure they are indeed generated.
	gd.ReflectdataSignatMu.Lock()
	NeedRuntimeType(gd, t)
	gd.ReflectdataSignatMu.Unlock()

	//print("algsym: %s -> %+S\n", p, s);

	return s
}

func TypeSym(gd *base.Invocation, t *types.Type) *types.Sym {
	if t == nil || (t.IsPtr() && t.Elem() == nil) || t.IsUntyped() {
		gd.Fatalf("TypeSym %v", t)
	}
	if t.Kind() == types.TFUNC && t.Recv() != nil {
		gd.Fatalf("misuse of method type: %v", t)
	}
	s := types.TypeSym(gd, t)
	gd.ReflectdataSignatMu.Lock()
	NeedRuntimeType(gd, t)
	gd.ReflectdataSignatMu.Unlock()
	return s
}

func TypeLinksymPrefix(gd *base.Invocation, prefix string, t *types.Type) *obj.LSym {
	return TypeSymPrefix(gd, prefix, t).Linksym(gd)
}

func TypeLinksymLookup(gd *base.Invocation, name string) *obj.LSym {
	return types.TypeSymLookup(gd, name).Linksym(gd)
}

func TypeLinksym(gd *base.Invocation, t *types.Type) *obj.LSym {
	lsym := TypeSym(gd, t).Linksym(gd)
	gd.ReflectdataSignatMu.Lock()
	if lsym.Extra == nil {
		ti := lsym.NewTypeInfo()
		ti.Type = t
	}
	gd.ReflectdataSignatMu.Unlock()
	return lsym
}

// TypePtrAt returns an expression that evaluates to the
// *runtime._type value for t.
func TypePtrAt(gd *base.Invocation, pos src.XPos, t *types.Type) *ir.AddrExpr {
	return typecheck.LinksymAddr(gd, pos, TypeLinksym(gd, t), types.Types[types.TUINT8])
}

// ITabLsym returns the LSym representing the itab for concrete type typ implementing
// interface iface. A dummy tab will be created in the unusual case where typ doesn't
// implement iface. Normally, this wouldn't happen, because the typechecker would
// have reported a compile-time error. This situation can only happen when the
// destination type of a type assert or a type in a type switch is parameterized, so
// it may sometimes, but not always, be a type that can't implement the specified
// interface.
func ITabLsym(gd *base.Invocation, typ, iface *types.Type) *obj.LSym {
	return itabLsym(gd, typ, iface, true)
}

func itabLsym(gd *base.Invocation, typ, iface *types.Type, allowNonImplement bool) *obj.LSym {
	s, existed := ir.Pkgs(gd).Itab.LookupOK(typ.LinkString() + "," + iface.LinkString())
	lsym := s.Linksym(gd)
	gd.ReflectdataSignatMu.Lock()
	if lsym.Extra == nil {
		ii := lsym.NewItabInfo()
		ii.Type = typ
	}
	gd.ReflectdataSignatMu.Unlock()

	if !existed {
		writeITab(gd, lsym, typ, iface, allowNonImplement)
	}
	return lsym
}

// ITabAddrAt returns an expression that evaluates to the
// *runtime.itab value for concrete type typ implementing interface
// iface.
func ITabAddrAt(gd *base.Invocation, pos src.XPos, typ, iface *types.Type) *ir.AddrExpr {
	lsym := itabLsym(gd, typ, iface, false)
	return typecheck.LinksymAddr(gd, pos, lsym, types.Types[types.TUINT8])
}

// needkeyupdate reports whether map updates with t as a key
// need the key to be updated.
func needkeyupdate(gd *base.Invocation, t *types.Type) bool {
	switch t.Kind() {
	case types.TBOOL, types.TINT, types.TUINT, types.TINT8, types.TUINT8, types.TINT16, types.TUINT16, types.TINT32, types.TUINT32,
		types.TINT64, types.TUINT64, types.TUINTPTR, types.TPTR, types.TUNSAFEPTR, types.TCHAN:
		return false

	case types.TFLOAT32, types.TFLOAT64, types.TCOMPLEX64, types.TCOMPLEX128, // floats and complex can be +0/-0
		types.TINTER,
		types.TSTRING: // strings might have smaller backing stores
		return true

	case types.TARRAY:
		return needkeyupdate(gd, t.Elem())

	case types.TSTRUCT:
		for _, t1 := range t.Fields() {
			if needkeyupdate(gd, t1.Type) {
				return true
			}
		}
		return false

	default:
		gd.Fatalf("bad type for map key: %v", t)
		return true
	}
}

// hashMightPanic reports whether the hash of a map key of type t might panic.
func hashMightPanic(t *types.Type) bool {
	switch t.Kind() {
	case types.TINTER:
		return true

	case types.TARRAY:
		return hashMightPanic(t.Elem())

	case types.TSTRUCT:
		for _, t1 := range t.Fields() {
			if hashMightPanic(t1.Type) {
				return true
			}
		}
		return false

	default:
		return false
	}
}

// formalType replaces predeclared aliases with real types.
// They've been separate internally to make error messages
// better, but we have to merge them in the reflect tables.
func formalType(t *types.Type) *types.Type {
	switch t {
	case types.AnyType, types.ByteType, types.RuneType:
		return types.Types[t.Kind()]
	}
	return t
}

func writeType(gd *base.Invocation, t *types.Type) *obj.LSym {
	t = formalType(t)
	if t.IsUntyped() {
		gd.Fatalf("writeType %v", t)
	}

	s := types.TypeSym(gd, t)
	lsym := s.Linksym(gd)

	// special case (look for runtime below):
	// when compiling package runtime,
	// emit the type structures for int, float, etc.
	tbase := t
	if t.IsPtr() && t.Sym() == nil && t.Elem().Sym() != nil {
		tbase = t.Elem()
	}
	if tbase.Kind() == types.TFORW {
		gd.Fatalf("unresolved defined type: %v", tbase)
	}

	// This is a fake type we generated for our builtin pseudo-runtime
	// package. We'll emit a description for the real type while
	// compiling package runtime, so we don't need or want to emit one
	// from this fake type.
	if sym := tbase.Sym(); sym != nil && sym.Pkg == ir.Pkgs(gd).Runtime {
		return lsym
	}

	if s.Siggen() {
		return lsym
	}
	s.SetSiggen(true)

	if !tbase.HasShape() {
		TypeLinksym(gd, t) // ensure lsym.Extra is set
	}

	if !NeedEmit(gd, tbase) {
		if i := typecheck.BaseTypeIndex(gd, t); i >= 0 {
			lsym.Pkg = tbase.Sym().Pkg.Prefix
			lsym.SymIdx = int32(i)
			lsym.Set(obj.AttrIndexed, true)
		}

		// TODO(mdempsky): Investigate whether this still happens.
		// If we know we don't need to emit code for a type,
		// we should have a link-symbol index for it.
		// See also TODO in NeedEmit.
		return lsym
	}

	// Type layout                          Written by               Marker
	// +--------------------------------+                            - 0
	// | abi/internal.Type              |   dcommontype
	// +--------------------------------+                            - A
	// | additional type-dependent      |   code in the switch below
	// | fields, e.g.                   |
	// | abi/internal.ArrayType.Len     |
	// +--------------------------------+                            - B
	// | internal/abi.UncommonType      |   dextratype
	// | This section is optional,      |
	// | if type has a name or methods  |
	// +--------------------------------+                            - C
	// | variable-length data           |   code in the switch below
	// | referenced by                  |
	// | type-dependent fields, e.g.    |
	// | abi/internal.StructType.Fields |
	// | dataAdd = size of this section |
	// +--------------------------------+                            - D
	// | method list, if any            |   dextratype
	// +--------------------------------+                            - E

	// UncommonType section is included if we have a name or a method.
	extra := t.Sym() != nil || len(methods(gd, t)) != 0

	// Decide the underlying type of the descriptor, and remember
	// the size we need for variable-length data.
	var rt *types.Type
	dataAdd := 0
	switch t.Kind() {
	default:
		rt = rttype.Type
	case types.TARRAY:
		rt = rttype.ArrayType
	case types.TSLICE:
		rt = rttype.SliceType
	case types.TCHAN:
		rt = rttype.ChanType
	case types.TFUNC:
		rt = rttype.FuncType
		dataAdd = (t.NumRecvs() + t.NumParams() + t.NumResults()) * types.PtrSize
	case types.TINTER:
		rt = rttype.InterfaceType
		dataAdd = len(imethods(gd, t)) * int(rttype.IMethod.Size())
	case types.TMAP:
		rt = rttype.MapType
	case types.TPTR:
		rt = rttype.PtrType
		// TODO: use rttype.Type for Elem() is ANY?
	case types.TSTRUCT:
		rt = rttype.StructType
		dataAdd = t.NumFields() * int(rttype.StructField.Size())
	}

	// Compute offsets of each section.
	B := rt.Size()
	C := B
	if extra {
		C = B + rttype.UncommonType.Size()
	}
	D := C + int64(dataAdd)
	E := D + int64(len(methods(gd, t)))*rttype.Method.Size()

	// Write the runtime._type
	c := rttype.NewCursor(gd, lsym, 0, rt)
	if rt == rttype.Type {
		dcommontype(gd, c, t)
	} else {
		dcommontype(gd, c.Field("Type"), t)
	}

	// Write additional type-specific data
	// (Both the fixed size and variable-sized sections.)
	switch t.Kind() {
	case types.TARRAY:
		// internal/abi.ArrayType
		s1 := writeType(gd, t.Elem())
		t2 := types.NewSlice(t.Elem())
		s2 := writeType(gd, t2)
		c.Field("Elem").WritePtr(s1)
		c.Field("Slice").WritePtr(s2)
		c.Field("Len").WriteUintptr(uint64(t.NumElem()))

	case types.TSLICE:
		// internal/abi.SliceType
		s1 := writeType(gd, t.Elem())
		c.Field("Elem").WritePtr(s1)

	case types.TCHAN:
		// internal/abi.ChanType
		s1 := writeType(gd, t.Elem())
		c.Field("Elem").WritePtr(s1)
		c.Field("Dir").WriteInt(int64(t.ChanDir()))

	case types.TFUNC:
		// internal/abi.FuncType
		//
		// gd Phase G.2.1: emit the EXTENDED view (including outBuf
		// slots). funcLayout / reflectcall need the full ABI to
		// build matching frames. reflect's user-facing methods
		// (NumIn, In, Call's arg-count check) filter outBufs via
		// FuncType.NumOutBufs/NumUserIn, gated on the top bit of
		// InCount (abi.PhaseGExtendedFlag).
		for _, t1 := range t.RecvParamsResults() {
			writeType(gd, t1.Type)
		}
		inCount := t.NumRecvs() + t.NumParams()
		outCount := t.NumResults()
		if t.IsVariadic() {
			outCount |= 1 << 15
		}
		// Set the Phase G extended flag in the top bit of InCount
		// so reflect can tell stock sigs (gate-off) from extended
		// sigs even when they both have pointer results.
		encodedInCount := uint16(inCount)
		if t.GdReturnOutBuf() {
			encodedInCount |= 1 << 15
		}

		c.Field("InCount").WriteUint16(encodedInCount)
		c.Field("OutCount").WriteUint16(uint16(outCount))

		// Array of rtype pointers follows funcType.
		typs := t.RecvParamsResults()
		array := rttype.NewArrayCursor(gd, lsym, C, types.Types[types.TUNSAFEPTR], len(typs))
		for i, t1 := range typs {
			array.Elem(i).WritePtr(writeType(gd, t1.Type))
		}

	case types.TINTER:
		// internal/abi.InterfaceType
		m := imethods(gd, t)
		n := len(m)
		for _, a := range m {
			writeType(gd, a.type_)
		}

		var tpkg *types.Pkg
		if t.Sym() != nil && t != types.Types[t.Kind()] && t != types.ErrorType {
			tpkg = t.Sym().Pkg
		}
		dgopkgpath(gd, c.Field("PkgPath"), tpkg)
		c.Field("Methods").WriteSlice(lsym, C, int64(n), int64(n))

		array := rttype.NewArrayCursor(gd, lsym, C, rttype.IMethod, n)
		for i, a := range m {
			exported := types.IsExported(a.name.Name)
			var pkg *types.Pkg
			if !exported && a.name.Pkg != tpkg {
				pkg = a.name.Pkg
			}
			nsym := dname(gd, a.name.Name, "", pkg, exported, false)

			e := array.Elem(i)
			e.Field("Name").WriteSymPtrOff(nsym, false)
			e.Field("Typ").WriteSymPtrOff(writeType(gd, a.type_), false)
		}

	case types.TMAP:
		writeMapType(gd, t, lsym, c)

	case types.TPTR:
		// internal/abi.PtrType
		if t.Elem().Kind() == types.TANY {
			gd.Fatalf("bad pointer base type")
		}

		s1 := writeType(gd, t.Elem())
		c.Field("Elem").WritePtr(s1)

	case types.TSTRUCT:
		// internal/abi.StructType
		fields := t.Fields()
		for _, t1 := range fields {
			writeType(gd, t1.Type)
		}

		// All non-exported struct field names within a struct
		// type must originate from a single package. By
		// identifying and recording that package within the
		// struct type descriptor, we can omit that
		// information from the field descriptors.
		var spkg *types.Pkg
		for _, f := range fields {
			if !types.IsExported(f.Sym.Name) {
				spkg = f.Sym.Pkg
				break
			}
		}

		dgopkgpath(gd, c.Field("PkgPath"), spkg)
		c.Field("Fields").WriteSlice(lsym, C, int64(len(fields)), int64(len(fields)))

		array := rttype.NewArrayCursor(gd, lsym, C, rttype.StructField, len(fields))
		for i, f := range fields {
			e := array.Elem(i)
			dnameField(gd, e.Field("Name"), spkg, f)
			e.Field("Typ").WritePtr(writeType(gd, f.Type))
			e.Field("Offset").WriteUintptr(uint64(f.Offset))
		}
	}

	// Write the extra info, if any.
	if extra {
		dextratype(gd, lsym, B, t, dataAdd)
	}

	// Note: DUPOK is required to ensure that we don't end up with more
	// than one type descriptor for a given type, if the type descriptor
	// can be defined in multiple packages, that is, unnamed types,
	// instantiated types and shape types.
	dupok := 0
	if tbase.Sym() == nil || tbase.IsFullyInstantiated() || tbase.HasShape() {
		dupok = obj.DUPOK
	}

	objw.Global(gd, lsym, int32(E), int16(dupok|obj.RODATA))

	// The linker will leave a table of all the typelinks for
	// types in the binary, so the runtime can find them.
	//
	// When buildmode=shared, all types are in typelinks so the
	// runtime can deduplicate type pointers.
	keep := gd.Ctxt.Flag_dynlink
	if !keep && t.Sym() == nil {
		// For an unnamed type, we only need the link if the type can
		// be created at run time by reflect.PointerTo and similar
		// functions. If the type exists in the program, those
		// functions must return the existing type structure rather
		// than creating a new one.
		switch t.Kind() {
		case types.TPTR, types.TARRAY, types.TCHAN, types.TFUNC, types.TMAP, types.TSLICE, types.TSTRUCT:
			keep = true
		}
	}
	// Do not put Noalg types in typelinks.  See issue #22605.
	if types.TypeHasNoAlg(t) {
		keep = false
	}
	lsym.Set(obj.AttrMakeTypelink, keep)

	return lsym
}

// InterfaceMethodOffset returns the offset of the i-th method in the interface
// type descriptor, ityp.
func InterfaceMethodOffset(gd *base.Invocation, ityp *types.Type, i int64) int64 {
	// interface type descriptor layout is struct {
	//   _type        // commonSize
	//   pkgpath      // 1 word
	//   []imethod    // 3 words (pointing to [...]imethod below)
	//   uncommontype // uncommonSize
	//   [...]imethod
	// }
	// The size of imethod is 8.
	return int64(commonSize()+4*types.PtrSize+uncommonSize(gd, ityp)) + i*8
}

// NeedRuntimeType ensures that a runtime type descriptor is emitted for t.
func NeedRuntimeType(gd *base.Invocation, t *types.Type) {
	set := signatSet(gd)
	if _, ok := set[t]; !ok {
		set[t] = struct{}{}
		ss, _ := gd.ReflectdataSignatSlice.([]typeAndStr)
		gd.ReflectdataSignatSlice = append(ss, typeAndStr{t: t, short: types.TypeSymName(t), regular: t.String()})
	}
}

func WriteRuntimeTypes(gd *base.Invocation) {
	// Process signatslice. Use a loop, as writeType adds
	// entries to signatslice while it is being processed.
	for {
		ss, _ := gd.ReflectdataSignatSlice.([]typeAndStr)
		if len(ss) == 0 {
			return
		}
		// Sort for reproducible builds.
		slices.SortFunc(ss, typesStrCmp)
		for _, ts := range ss {
			t := ts.t
			writeType(gd, t)
			if t.Sym() != nil {
				writeType(gd, types.NewPtr(t))
			}
		}
		// writeType may have appended; re-read and trim what we processed.
		cur, _ := gd.ReflectdataSignatSlice.([]typeAndStr)
		gd.ReflectdataSignatSlice = cur[len(ss):]
	}
}

func WriteGCSymbols(gd *base.Invocation) {
	// Emit GC data symbols.
	set := gcsymSet(gd)
	gcsyms := make([]typeAndStr, 0, len(set))
	for t := range set {
		gcsyms = append(gcsyms, typeAndStr{t: t, short: types.TypeSymName(t), regular: t.String()})
	}
	slices.SortFunc(gcsyms, typesStrCmp)
	for _, ts := range gcsyms {
		dgcsym(gd, ts.t, true, false)
	}
}

// writeITab writes the itab for concrete type typ implementing interface iface. If
// allowNonImplement is true, allow the case where typ does not implement iface, and just
// create a dummy itab with zeroed-out method entries.
func writeITab(gd *base.Invocation, lsym *obj.LSym, typ, iface *types.Type, allowNonImplement bool) {
	// TODO(mdempsky): Fix methodWrapper, geneq, and genhash (and maybe
	// others) to stop clobbering these.
	oldpos, oldfn := gd.Pos, ir.CurFunc(gd)
	defer func() { gd.Pos, gd.CurFunc = oldpos, oldfn }()

	if typ == nil || (typ.IsPtr() && typ.Elem() == nil) || typ.IsUntyped() || iface == nil || !iface.IsInterface() || iface.IsEmptyInterface() {
		gd.Fatalf("writeITab(gd, %v, %v)", typ, iface)
	}

	sigs := iface.AllMethods()
	entries := make([]*obj.LSym, 0, len(sigs))
	entrySigs := make([]*typeSig, 0, len(sigs))

	// both sigs and methods are sorted by name,
	// so we can find the intersection in a single pass
	for _, m := range methods(gd, typ) {
		if m.name == sigs[0].Sym {
			entries = append(entries, m.isym)
			entrySigs = append(entrySigs, m)
			if m.isym == nil {
				panic("NO ISYM")
			}
			sigs = sigs[1:]
			if len(sigs) == 0 {
				break
			}
		}
	}
	completeItab := len(sigs) == 0
	if !allowNonImplement && !completeItab {
		gd.Fatalf("incomplete itab")
	}

	// dump empty itab symbol into i.sym
	// type itab struct {
	//   inter  *interfacetype
	//   _type  *_type
	//   hash   uint32 // copy of _type.hash. Used for type switches.
	//   inline uint8  // gd: nonzero if _type fits in the iface inline slot
	//   _      [3]byte
	//   fun    [1]uintptr // variable sized. fun[0]==0 means _type does not implement inter.
	// }
	c := rttype.NewCursor(gd, lsym, 0, rttype.ITab)
	c.Field("Inter").WritePtr(writeType(gd, iface))
	c.Field("Type").WritePtr(writeType(gd, typ))
	c.Field("Hash").WriteUint32(types.TypeHash(typ)) // copy of type hash
	switch {
	case types.IsInlineIface(typ):
		c.Field("Inline").WriteUint8(uint8(abi.ITabInlineInline))
	case types.IsSpreadIface(typ):
		c.Field("Inline").WriteUint8(uint8(abi.ITabInlineSpread))
	}

	var delta int64
	c = c.Field("Fun")
	nmethods := len(entries)
	if !completeItab {
		// If typ doesn't implement iface, make method entries be zero.
		c.Elem(0).WriteUintptr(0)
		nmethods = 1 // single zero slot; mask tail still gets one uint64 below
	} else {
		var a rttype.ArrayCursor
		a, delta = c.ModifyArray(len(entries))
		for i, fn := range entries {
			a.Elem(i).WritePtrWeak(fn) // method pointer for each method
		}
	}

	// gd escape-bits: reserve one uint64 per method slot immediately
	// after Fun and populate each from the concrete method's params
	// (not RecvParams — the iface dispatch hides the receiver, so
	// bit k+1 maps to the k-th non-receiver arg the caller sees).
	// Matches the runtime layout consumed by runtime.maybeEscape
	// IfaceArg in src/runtime/escape_bits.go.
	maskOffset := rttype.ITab.Size() + delta
	for i := 0; i < nmethods; i++ {
		// Reserve the slot now (zeros); the real value is written
		// by FinalizeItabMasks after escape analysis runs. writeITab
		// is called during noder for statically-constructible itabs,
		// well before ir.Func.EscMask has been populated.
		objw.UintN(gd, lsym, int(maskOffset)+i*8, 0, 8)
		if completeItab && i < len(entrySigs) {
			sig := entrySigs[i]
			gd.ReflectdataPendingItabMasks = append(pendingItabMasks(gd), pendingItabMask{
				lsym:     lsym,
				offset:   int(maskOffset) + i*8,
				fn:       sig.methodFn,
				origType: sig.origType,
			})
		}
	}
	totalSize := rttype.ITab.Size() + delta + int64(nmethods)*8

	// Nothing writes static itabs, so they are read only.
	objw.Global(gd, lsym, int32(totalSize), int16(obj.DUPOK|obj.RODATA))
	// Phase F4 install puts a per-package SymPtr reloc into the
	// itab's mask tail (FinalizeItabMasks). The reloc target's
	// PkgIdxSelf hash is salted with the current package path
	// (cmd/internal/obj.objfile contentHash), making the same
	// logical itab compiled from different packages produce
	// different content hashes — breaking the linker's hashed-
	// def dedup and surfacing as "T from different scopes"
	// panics on type assertions where the duplicate itabs
	// reach different rtype lookups. F4 install is on by default;
	// fall back to plain DUPOK name-based dedup for itabs unless
	// the user explicitly disables F4 with -d=gdforwarderdisable=1.
	if gd.Debug.GdForwarderDisable != 0 {
		lsym.Set(obj.AttrContentAddressable, true)
	}
}

func WritePluginTable(gd *base.Invocation) {
	ptabs := typecheck.Target(gd).PluginExports
	if len(ptabs) == 0 {
		return
	}

	lsym := gd.Ctxt.Lookup("go:plugin.tabs")
	ot := 0
	for _, p := range ptabs {
		// Dump ptab symbol into go.pluginsym package.
		//
		// type ptab struct {
		//	name nameOff
		//	typ  typeOff // pointer to symbol
		// }
		nsym := dname(gd, p.Sym().Name, "", nil, true, false)
		t := p.Type()
		if p.Class != ir.PFUNC {
			t = types.NewPtr(t)
		}
		tsym := writeType(gd, t)
		ot = objw.SymPtrOff(gd, lsym, ot, nsym)
		ot = objw.SymPtrOff(gd, lsym, ot, tsym)
		// Plugin exports symbols as interfaces. Mark their types
		// as UsedInIface.
		tsym.Set(obj.AttrUsedInIface, true)
	}
	objw.Global(gd, lsym, int32(ot), int16(obj.RODATA))

	lsym = gd.Ctxt.Lookup("go:plugin.exports")
	ot = 0
	for _, p := range ptabs {
		ot = objw.SymPtr(gd, lsym, ot, p.Linksym(), 0)
	}
	objw.Global(gd, lsym, int32(ot), int16(obj.RODATA))
}

// writtenByWriteBasicTypes reports whether typ is written by WriteBasicTypes.
// WriteBasicTypes always writes pointer types; any pointer has been stripped off typ already.
func writtenByWriteBasicTypes(typ *types.Type) bool {
	if typ.Sym() == nil && typ.Kind() == types.TFUNC {
		// func(error) string
		if typ.NumRecvs() == 0 &&
			typ.NumParams() == 1 && typ.NumResults() == 1 &&
			typ.Param(0).Type == types.ErrorType &&
			typ.Result(0).Type == types.Types[types.TSTRING] {
			return true
		}
	}

	// Now we have left the basic types plus any and error, plus slices of them.
	// Strip the slice.
	if typ.Sym() == nil && typ.IsSlice() {
		typ = typ.Elem()
	}

	// Basic types. BuiltinPkg/UnsafePkg are per-Invocation; compare
	// by Path so a Sym from any gd matches.
	sym := typ.Sym()
	if sym != nil && sym.Pkg != nil && (sym.Pkg.Path == "go.builtin" || sym.Pkg.Path == "unsafe") {
		return true
	}
	// any or error
	return (sym == nil && typ.IsEmptyInterface()) || typ == types.ErrorType
}

func WriteBasicTypes(gd *base.Invocation) {
	// do basic types if compiling package runtime.
	// they have to be in at least one package,
	// and runtime is always loaded implicitly,
	// so this is as good as any.
	// another possible choice would be package main,
	// but using runtime means fewer copies in object files.
	// The code here needs to be in sync with writtenByWriteBasicTypes above.
	if gd.Ctxt.Pkgpath != "runtime" {
		return
	}

	// Note: always write NewPtr(t) because NeedEmit's caller strips the pointer.
	var list []*types.Type
	for i := types.Kind(1); i <= types.TBOOL; i++ {
		list = append(list, types.Types[i])
	}
	list = append(list,
		types.Types[types.TSTRING],
		types.Types[types.TUNSAFEPTR],
		types.AnyType,
		types.ErrorType)
	for _, t := range list {
		writeType(gd, types.NewPtr(t))
		writeType(gd, types.NewPtr(types.NewSlice(t)))
	}

	// emit type for func(error) string,
	// which is the type of an auto-generated wrapper.
	writeType(gd, types.NewPtr(types.NewSignature(gd, nil, []*types.Field{
		types.NewField(gd.Pos, nil, types.ErrorType),
	}, []*types.Field{
		types.NewField(gd.Pos, nil, types.Types[types.TSTRING]),
	})))
}

type typeAndStr struct {
	t       *types.Type
	short   string // "short" here means TypeSymName
	regular string
}

func typesStrCmp(a, b typeAndStr) int {
	// put named types before unnamed types
	if a.t.Sym() != nil && b.t.Sym() == nil {
		return -1
	}
	if a.t.Sym() == nil && b.t.Sym() != nil {
		return +1
	}

	if r := strings.Compare(a.short, b.short); r != 0 {
		return r
	}
	// When the only difference between the types is whether
	// they refer to byte or uint8, such as **byte vs **uint8,
	// the types' NameStrings can be identical.
	// To preserve deterministic sort ordering, sort these by String().
	//
	// TODO(mdempsky): This all seems suspect. Using LinkString would
	// avoid naming collisions, and there shouldn't be a reason to care
	// about "byte" vs "uint8": they share the same runtime type
	// descriptor anyway.
	if r := strings.Compare(a.regular, b.regular); r != 0 {
		return r
	}
	// Identical anonymous interfaces defined in different locations
	// will be equal for the above checks, but different in DWARF output.
	// Sort by source position to ensure deterministic order.
	// See issues 27013 and 30202.
	if a.t.Kind() == types.TINTER && len(a.t.AllMethods()) > 0 {
		if a.t.AllMethods()[0].Pos.Before(b.t.AllMethods()[0].Pos) {
			return -1
		}
		return +1
	}
	return 0
}

// GCSym returns a data symbol containing GC information for type t.
// GC information is always a bitmask, never a gc program.
// GCSym may be called in concurrent backend, so it does not emit the symbol
// content.
func GCSym(gd *base.Invocation, t *types.Type, onDemandAllowed bool) (lsym *obj.LSym, ptrdata int64) {
	// Record that we need to emit the GC symbol.
	gd.ReflectdataGcsymMu.Lock()
	set := gcsymSet(gd)
	if _, ok := set[t]; !ok {
		set[t] = struct{}{}
	}
	gd.ReflectdataGcsymMu.Unlock()

	lsym, _, ptrdata = dgcsym(gd, t, false, onDemandAllowed)
	return
}

// dgcsym returns a data symbol containing GC information for type t, along
// with a boolean reporting whether the gc mask should be computed on demand
// at runtime, and the ptrdata field to record in the reflect type information.
// When write is true, it writes the symbol data.
func dgcsym(gd *base.Invocation, t *types.Type, write, onDemandAllowed bool) (lsym *obj.LSym, onDemand bool, ptrdata int64) {
	ptrdata = types.PtrDataSize(t)
	if !onDemandAllowed || ptrdata/int64(types.PtrSize) <= abi.MaxPtrmaskBytes*8 {
		lsym = dgcptrmask(gd, t, write)
		return
	}

	onDemand = true
	lsym = dgcptrmaskOnDemand(gd, t, write)
	return
}

// dgcptrmask emits and returns the symbol containing a pointer mask for type t.
func dgcptrmask(gd *base.Invocation, t *types.Type, write bool) *obj.LSym {
	// Bytes we need for the ptrmask.
	n := (types.PtrDataSize(t)/int64(types.PtrSize) + 7) / 8
	// Runtime wants ptrmasks padded to a multiple of uintptr in size.
	n = (n + int64(types.PtrSize) - 1) &^ (int64(types.PtrSize) - 1)
	ptrmask := make([]byte, n)
	fillptrmask(gd, t, ptrmask)
	p := fmt.Sprintf("runtime.gcbits.%x", ptrmask)

	lsym := gd.Ctxt.Lookup(p)
	if write && !lsym.OnList() {
		for i, x := range ptrmask {
			objw.Uint8(gd, lsym, i, x)
		}
		objw.Global(gd, lsym, int32(len(ptrmask)), obj.DUPOK|obj.RODATA|obj.LOCAL)
		lsym.Set(obj.AttrContentAddressable, true)
	}
	return lsym
}

// fillptrmask fills in ptrmask with 1s corresponding to the
// word offsets in t that hold pointers.
// ptrmask is assumed to fit at least types.PtrDataSize(t)/PtrSize bits.
func fillptrmask(gd *base.Invocation, t *types.Type, ptrmask []byte) {
	if !t.HasPointers() {
		return
	}

	vec := bitvec.New(gd, 8*int32(len(ptrmask)))
	typebits.Set(t, 0, vec)

	nptr := types.PtrDataSize(t) / int64(types.PtrSize)
	for i := int64(0); i < nptr; i++ {
		if vec.Get(int32(i)) {
			ptrmask[i/8] |= 1 << (uint(i) % 8)
		}
	}
}

// dgcptrmaskOnDemand emits and returns the symbol that should be referenced by
// the GCData field of a type, for large types.
func dgcptrmaskOnDemand(gd *base.Invocation, t *types.Type, write bool) *obj.LSym {
	lsym := TypeLinksymPrefix(gd, ".gcmask", t)
	if write && !lsym.OnList() {
		// Note: contains a pointer, but a pointer to a
		// persistentalloc allocation. Starts with nil.
		objw.Uintptr(gd, lsym, 0, 0)
		objw.Global(gd, lsym, int32(types.PtrSize), obj.DUPOK|obj.NOPTR|obj.LOCAL) // TODO:bss?
	}
	return lsym
}

// ZeroAddr returns the address of a symbol with at least
// size bytes of zeros.
func ZeroAddr(gd *base.Invocation, size int64) ir.Node {
	if size >= 1<<31 {
		gd.Fatalf("map elem too big %d", size)
	}
	if gd.ReflectdataZeroSize < size {
		gd.ReflectdataZeroSize = size
	}
	lsym := gd.PkgLinksym("go:map", "zero", obj.ABI0)
	x := ir.NewLinksymExpr(gd, gd.Pos, lsym, types.Types[types.TUINT8])
	return typecheck.Expr(gd, typecheck.NodAddr(gd, x))
}

// NeedEmit reports whether typ is a type that we need to emit code
// for (e.g., runtime type descriptors, method wrappers).
func NeedEmit(gd *base.Invocation, typ *types.Type) bool {
	// TODO(mdempsky): Export data should keep track of which anonymous
	// and instantiated types were emitted, so at least downstream
	// packages can skip re-emitting them.
	//
	// Perhaps we can just generalize the linker-symbol indexing to
	// track the index of arbitrary types, not just defined types, and
	// use its presence to detect this. The same idea would work for
	// instantiated generic functions too.

	switch sym := typ.Sym(); {
	case writtenByWriteBasicTypes(typ):
		return gd.Ctxt.Pkgpath == "runtime"

	case sym == nil:
		// Anonymous type; possibly never seen before or ever again.
		// Need to emit to be safe (however, see TODO above).
		return true

	case sym.Pkg == types.LocalPkg(gd):
		// Local defined type; our responsibility.
		return true

	case typ.IsFullyInstantiated():
		// Instantiated type; possibly instantiated with unique type arguments.
		// Need to emit to be safe (however, see TODO above).
		return true

	case typ.HasShape():
		// Shape type; need to emit even though it lives in the .shape package.
		// TODO: make sure the linker deduplicates them (see dupok in writeType above).
		return true

	default:
		// Should have been emitted by an imported package.
		return false
	}
}

// Generate a wrapper function to convert from
// a receiver of type T to a receiver of type U.
// That is,
//
//	func (t T) M() {
//		...
//	}
//
// already exists; this function generates
//
//	func (u U) M() {
//		u.M()
//	}
//
// where the types T and U are such that u.M() is valid
// and calls the T.M method.
// The resulting function is for use in method tables.
//
//	rcvr - U
//	method - M func (t T)(), a TFIELD type struct
//
// Also wraps methods on instantiated generic types for use in itab entries.
// For an instantiated generic type G[int], we generate wrappers like:
// G[int] pointer shaped:
//
//	func (x G[int]) f(arg) {
//		.inst.G[int].f(dictionary, x, arg)
//	}
//
// G[int] not pointer shaped:
//
//	func (x *G[int]) f(arg) {
//		.inst.G[int].f(dictionary, *x, arg)
//	}
//
// These wrappers are always fully stenciled.
func methodWrapper(gd *base.Invocation, rcvr *types.Type, method *types.Field, forItab bool) *obj.LSym {
	if forItab && !types.IsDirectIface(rcvr) {
		rcvr = rcvr.PtrTo()
	}

	newnam := ir.MethodSym(gd, rcvr, method.Sym)
	lsym := newnam.Linksym(gd)

	// Unified IR creates its own wrappers.
	return lsym
}

// (gd.ReflectdataZeroSize is the per-Invocation high-water mark for
// the .L_zero RODATA buffer; the gd/obj.go finalizer emits it once.)

// MarkTypeUsedInInterface marks that type t is converted to an interface.
// This information is used in the linker in dead method elimination.
func MarkTypeUsedInInterface(gd *base.Invocation, t *types.Type, from *obj.LSym) {
	if t.HasShape() {
		// Shape types shouldn't be put in interfaces, so we shouldn't ever get here.
		gd.Fatalf("shape types have no methods %+v", t)
	}
	MarkTypeSymUsedInInterface(gd, TypeLinksym(gd, t), from)
}
func MarkTypeSymUsedInInterface(gd *base.Invocation, tsym *obj.LSym, from *obj.LSym) {
	// Emit a marker relocation. The linker will know the type is converted
	// to an interface if "from" is reachable.
	from.AddRel(gd.Ctxt, obj.Reloc{Type: objabi.R_USEIFACE, Sym: tsym})
}

// MarkUsedIfaceMethod marks that an interface method is used in the current
// function. n is OCALLINTER node.
func MarkUsedIfaceMethod(gd *base.Invocation, n *ir.CallExpr) {
	// skip unnamed functions (func _())
	if ir.CurFunc(gd).LSym == nil {
		return
	}
	dot := n.Fun.(*ir.SelectorExpr)
	ityp := dot.X.Type()
	if ityp.HasShape() {
		// Here we're calling a method on a generic interface. Something like:
		//
		// type I[T any] interface { foo() T }
		// func f[T any](x I[T]) {
		//     ... = x.foo()
		// }
		// f[int](...)
		// f[string](...)
		//
		// In this case, in f we're calling foo on a generic interface.
		// Which method could that be? Normally we could match the method
		// both by name and by type. But in this case we don't really know
		// the type of the method we're calling. It could be func()int
		// or func()string. So we match on just the function name, instead
		// of both the name and the type used for the non-generic case below.
		// TODO: instantiations at least know the shape of the instantiated
		// type, and the linker could do more complicated matching using
		// some sort of fuzzy shape matching. For now, only use the name
		// of the method for matching.
		ir.CurFunc(gd).LSym.AddRel(gd.Ctxt, obj.Reloc{
			Type: objabi.R_USENAMEDMETHOD,
			Sym:  staticdata.StringSymNoCommon(gd, dot.Sel.Name),
		})
		return
	}

	// dot.Offset() is the method index * PtrSize (the offset of code pointer in itab).
	midx := dot.Offset() / int64(types.PtrSize)
	ir.CurFunc(gd).LSym.AddRel(gd.Ctxt, obj.Reloc{
		Type: objabi.R_USEIFACEMETHOD,
		Sym:  TypeLinksym(gd, ityp),
		Add:  InterfaceMethodOffset(gd, ityp, midx),
	})
}
