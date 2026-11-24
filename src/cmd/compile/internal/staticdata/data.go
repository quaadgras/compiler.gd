// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package staticdata

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"go/constant"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/fatal"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/types"
	"cmd/internal/hash"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/src"
)

// InitAddrOffset writes the static name symbol lsym to n, it does not modify n.
// It's the caller responsibility to make sure lsym is from ONAME/PEXTERN node.
func InitAddrOffset(gd *base.Invocation, n *ir.Name, noff int64, lsym *obj.LSym, off int64) {
	if n.Op() != ir.ONAME {
		gd.Fatalf("InitAddr n op %v", n.Op())
	}
	if n.Sym() == nil {
		gd.Fatalf("InitAddr nil n sym")
	}
	s := n.Linksym()
	s.WriteAddr(gd.Ctxt, noff, types.PtrSize, lsym, off)
}

// InitAddr is InitAddrOffset, with offset fixed to 0.
func InitAddr(gd *base.Invocation, n *ir.Name, noff int64, lsym *obj.LSym) {
	InitAddrOffset(gd, n, noff, lsym, 0)
}

// InitSlice writes a static slice symbol {lsym, lencap, lencap} to n+noff, it does not modify n.
// It's the caller responsibility to make sure lsym is from ONAME node.
func InitSlice(gd *base.Invocation, n *ir.Name, noff int64, lsym *obj.LSym, lencap int64) {
	s := n.Linksym()
	s.WriteAddr(gd.Ctxt, noff, types.PtrSize, lsym, 0)
	if types.SlicePacked() {
		// uint32s, which a static slice fits (types.SlicePacked).
		if lencap >= types.SliceHugeCap {
			gd.Fatalf("static slice of %d elements", lencap)
		}
		s.WriteInt(gd.Ctxt, noff+types.SliceLenOffset, 4, lencap)
		s.WriteInt(gd.Ctxt, noff+types.SliceCapOffset, 4, lencap)
		return
	}
	s.WriteInt(gd.Ctxt, noff+types.SliceLenOffset, types.PtrSize, lencap)
	s.WriteInt(gd.Ctxt, noff+types.SliceCapOffset, types.PtrSize, lencap)
}

func InitSliceBytes(gd *base.Invocation, nam *ir.Name, off int64, s string) {
	if nam.Op() != ir.ONAME {
		gd.Fatalf("InitSliceBytes %v", nam)
	}
	InitSlice(gd, nam, off, slicedata(gd, nam.Pos(), s), int64(len(s)))
}

const (
	stringSymPrefix  = "go:string."
	stringSymPattern = ".gostring.%d.%s"
)

// shortHashString converts the hash to a string for use with stringSymPattern.
// We cut it to 16 bytes and then base64-encode to make it even smaller.
func shortHashString(hash []byte) string {
	return base64.StdEncoding.EncodeToString(hash[:16])
}

// StringSym returns a symbol containing the string s.
// The symbol contains the string data, not a string header.
func StringSym(gd *base.Invocation, pos src.XPos, s string) (data *obj.LSym) {
	var symname string
	if len(s) > 100 {
		// Huge strings are hashed to avoid long names in object files.
		// Indulge in some paranoia by writing the length of s, too,
		// as protection against length extension attacks.
		// Same pattern is known to fileStringSym below.
		h := hash.New32()
		io.WriteString(h, s)
		symname = fmt.Sprintf(stringSymPattern, len(s), shortHashString(h.Sum(nil)))
	} else {
		// Small strings get named directly by their contents.
		symname = strconv.Quote(s)
	}

	symdata := gd.Ctxt.Lookup(stringSymPrefix + symname)
	if !symdata.OnList() {
		off := dstringdata(gd, symdata, 0, s, pos, "string")
		// A 0 byte follows the string (not counted in its length), so
		// the data of a string constant is also a C string. Nothing
		// points into the middle of another string's symbol, so this
		// costs one byte per distinct string.
		symdata.WriteInt(gd.Ctxt, int64(off), 1, 0)
		off++
		objw.Global(gd, symdata, int32(off), obj.DUPOK|obj.RODATA|obj.LOCAL)
		symdata.Set(obj.AttrContentAddressable, true)
		symdata.Align = 1
	}

	return symdata
}

// StringSymNoCommon is like StringSym, but produces a symbol that is not content-
// addressable. This symbol is not supposed to appear in the final binary, it is
// only used to pass string arguments to the linker like R_USENAMEDMETHOD does.
func StringSymNoCommon(gd *base.Invocation, s string) (data *obj.LSym) {
	var nameSym obj.LSym
	nameSym.WriteString(gd.Ctxt, 0, len(s), s)
	objw.Global(gd, &nameSym, int32(len(s)), obj.RODATA)
	return &nameSym
}

// maxFileSize is the maximum file size permitted by the linker
// (see issue #9862).
const maxFileSize = obj.MaxSymSize

// fileStringSym returns a symbol for the contents and the size of file.
// If readonly is true, the symbol shares storage with any literal string
// or other file with the same content and is placed in a read-only section.
// If readonly is false, the symbol is a read-write copy separate from any other,
// for use as the backing store of a []byte.
// The content hash of file is copied into hashBytes. (If hash is nil, nothing is copied.)
// The returned symbol contains the data itself, not a string header.
func fileStringSym(gd *base.Invocation, pos src.XPos, file string, readonly bool, hashBytes []byte) (*obj.LSym, int64, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("not a regular file")
	}
	size := info.Size()
	if size <= 1*1024 {
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, 0, err
		}
		if int64(len(data)) != size {
			return nil, 0, fmt.Errorf("file changed between reads")
		}
		var sym *obj.LSym
		if readonly {
			sym = StringSym(gd, pos, string(data))
		} else {
			sym = slicedata(gd, pos, string(data))
		}
		if len(hashBytes) > 0 {
			sum := hash.Sum32(data)
			copy(hashBytes, sum[:])
		}
		return sym, size, nil
	}
	if size+1 > maxFileSize { // +1 for the 0 byte after it
		// ggloblsym takes an int32,
		// and probably the rest of the toolchain
		// can't handle such big symbols either.
		// See golang.org/issue/9862.
		return nil, 0, fmt.Errorf("file too large (%d bytes > %d bytes)", size, maxFileSize)
	}

	// File is too big to read and keep in memory.
	// Compute hashBytes if needed for read-only content hashing or if the caller wants it.
	var sum []byte
	if readonly || len(hashBytes) > 0 {
		h := hash.New32()
		n, err := io.Copy(h, f)
		if err != nil {
			return nil, 0, err
		}
		if n != size {
			return nil, 0, fmt.Errorf("file changed between reads")
		}
		sum = h.Sum(nil)
		copy(hashBytes, sum)
	}

	var symdata *obj.LSym
	if readonly {
		symname := fmt.Sprintf(stringSymPattern, size, shortHashString(sum))
		symdata = gd.Ctxt.Lookup(stringSymPrefix + symname)
		if !symdata.OnList() {
			info := symdata.NewFileInfo()
			info.Name = file
			info.Size = size
			// One more than the file: the 0 byte StringSym writes
			// after a string (the linker zero-fills the rest of a
			// symbol), as this symbol may be StringSym's for the
			// same content.
			objw.Global(gd, symdata, int32(size+1), obj.DUPOK|obj.RODATA|obj.LOCAL)
			symdata.Align = 1
			// Note: AttrContentAddressable cannot be set here,
			// because the content-addressable-handling code
			// does not know about file symbols.
		}
	} else {
		// Emit a zero-length data symbol
		// and then fix up length and content to use file.
		symdata = slicedata(gd, pos, "")
		symdata.Size = size
		symdata.Type = objabi.SNOPTRDATA
		info := symdata.NewFileInfo()
		info.Name = file
		info.Size = size
	}

	return symdata, size, nil
}

// slicedata's counter lives on Invocation (gd.Slicedatagen) so multiple
// compile invocations don't share the .gobytes name space.
func slicedata(gd *base.Invocation, pos src.XPos, s string) *obj.LSym {
	gd.Slicedatagen++
	symname := fmt.Sprintf(".gobytes.%d", gd.Slicedatagen)
	lsym := types.LocalPkg(gd).Lookup(symname).LinksymABI(gd, obj.ABI0)
	off := dstringdata(gd, lsym, 0, s, pos, "slice")
	objw.Global(gd, lsym, int32(off), obj.NOPTR|obj.LOCAL)
	lsym.Align = 1

	return lsym
}

func dstringdata(gd *base.Invocation, s *obj.LSym, off int, t string, pos src.XPos, what string) int {
	// Objects that are too large will cause the data section to overflow right away,
	// causing a cryptic error message by the linker. Check for oversize objects here
	// and provide a useful error message instead.
	if int64(len(t)) > 2e9 {
		gd.ErrorfAt(pos, 0, "%v with length %v is too big", what, len(t))
		return 0
	}

	s.WriteString(gd.Ctxt, int64(off), len(t), t)
	return off + len(t)
}

// funcsyms / funcsymsmu live on Invocation (gd.StaticdataFuncsyms,
// gd.StaticdataFuncsymsMu). Per-compile so concurrent compile
// invocations in one process don't share or contend on the list.

// FuncLinksym returns n·f, the function value symbol for n.
func FuncLinksym(gd *base.Invocation, n *ir.Name) *obj.LSym {
	if n.Op() != ir.ONAME || n.Class != ir.PFUNC {
		gd.Fatalf("expected func name: %v", n)
	}
	s := n.Sym()

	// funcsymsmu here serves to protect not just mutations of funcsyms (below),
	// but also the package lookup of the func sym name,
	// since this function gets called concurrently from the backend.
	// There are no other concurrent package lookups in the backend,
	// except for the types package, which is protected separately.
	// Reusing funcsymsmu to also cover this package lookup
	// avoids a general, broader, expensive package lookup mutex.
	gd.StaticdataFuncsymsMu.Lock()
	sf, existed := s.Pkg.LookupOK(ir.FuncSymName(s))
	if !existed {
		fs, _ := gd.StaticdataFuncsyms.([]*ir.Name)
		gd.StaticdataFuncsyms = append(fs, n)
	}
	gd.StaticdataFuncsymsMu.Unlock()

	return sf.Linksym(gd)
}

func GlobalLinksym(n *ir.Name) *obj.LSym {
	if n.Op() != ir.ONAME || n.Class != ir.PEXTERN {
		fatal.Error("expected global variable: %v", n)
	}
	return n.Linksym()
}

func WriteFuncSyms(gd *base.Invocation) {
	funcsyms, _ := gd.StaticdataFuncsyms.([]*ir.Name)
	slices.SortFunc(funcsyms, func(a, b *ir.Name) int {
		return strings.Compare(a.Linksym().Name, b.Linksym().Name)
	})
	for _, nam := range funcsyms {
		s := nam.Sym()
		sf := s.Pkg.Lookup(ir.FuncSymName(s)).Linksym(gd)

		// While compiling package runtime, we might try to create
		// funcsyms for functions from both types.LocalPkg and
		// ir.Pkgs(gd).Runtime.
		if gd.Flag.CompilingRuntime && sf.OnList() {
			continue
		}

		// Function values must always reference ABIInternal
		// entry points.
		target := s.Linksym(gd)
		if target.ABI() != obj.ABIInternal {
			gd.Fatalf("expected ABIInternal: %v has %v", target, target.ABI())
		}
		// gd escape-bits: function-value rodata entry is {F, M}
		// (PtrSize + 8 bytes) instead of the stock {F} alone.
		// M is the function's per-argument heap-escape mask; every
		// indirect call dispatched through this value reads M to
		// decide whether to materialize pointer args to the heap.
		// See doc/gd/escape-bits.md. Capturing closure literals and
		// method values already carry M at offset PtrSize via their
		// own struct layouts; this path uniform-ifies bare function
		// references so the wrap at OCALLFUNC always hits a valid
		// mask word.
		objw.SymPtr(gd, sf, 0, target, 0)
		// The mask is the static one, from the function's escape
		// notes, also for a forwarder method (escape.DetectForwarders),
		// whose itab entries have a dynamic mask instead: the function
		// that computes that one reads the inner interface through
		// the receiver, which an interface call site passes it, and
		// answers for the arguments after the receiver. A call of
		// this function value ((*W).M)(w, &x) has neither: its
		// carrier is this symbol and the receiver is argument 0.
		objw.EscMask(gd, sf, int(types.FuncMaskOffset), ir.EscMaskOf(nam.Type().RecvParams()))
		// What the linker is to know of a call of this value.
		if key, _ := ir.ShareKeyFunc(nam.Type()); key != "" {
			var shares []ir.ParamShare
			if nam.Func != nil {
				shares = nam.Func.ParamShares
			}
			stores := uint16(ir.StoresAll)
			if nam.Func != nil && len(shares) != 0 {
				stores = nam.Func.DirtyInto
			}
			addShareRecord(gd, sf, "F."+target.Name, key, nam.Type().RecvParams(), shares, stores)
		}
		objw.Global(gd, sf, int32(types.ClosureVarsOffset), obj.DUPOK|obj.RODATA)
	}
}

// InitConst writes the static literal c to n.
// Neither n nor c is modified.
func InitConst(gd *base.Invocation, n *ir.Name, noff int64, c ir.Node, wid int) {
	if n.Op() != ir.ONAME {
		gd.Fatalf("InitConst n op %v", n.Op())
	}
	if n.Sym() == nil {
		gd.Fatalf("InitConst nil n sym")
	}
	if c.Op() == ir.ONIL {
		return
	}
	if c.Op() != ir.OLITERAL {
		gd.Fatalf("InitConst c op %v", c.Op())
	}
	s := n.Linksym()
	switch u := c.Val(); u.Kind() {
	case constant.Bool:
		i := int64(obj.Bool2int(constant.BoolVal(u)))
		s.WriteInt(gd.Ctxt, noff, wid, i)

	case constant.Int:
		s.WriteInt(gd.Ctxt, noff, wid, ir.IntVal(gd, c.Type(), u))

	case constant.Float:
		f, _ := constant.Float64Val(u)
		switch c.Type().Kind() {
		case types.TFLOAT32:
			s.WriteFloat32(gd.Ctxt, noff, float32(f))
		case types.TFLOAT64:
			s.WriteFloat64(gd.Ctxt, noff, f)
		}

	case constant.Complex:
		re, _ := constant.Float64Val(constant.Real(u))
		im, _ := constant.Float64Val(constant.Imag(u))
		switch c.Type().Kind() {
		case types.TCOMPLEX64:
			s.WriteFloat32(gd.Ctxt, noff, float32(re))
			s.WriteFloat32(gd.Ctxt, noff+4, float32(im))
		case types.TCOMPLEX128:
			s.WriteFloat64(gd.Ctxt, noff, re)
			s.WriteFloat64(gd.Ctxt, noff+8, im)
		}

	case constant.String:
		// See doc/gd/sso-string.md and types.StringTwoWords.
		i := constant.StringVal(u)
		slen := int64(len(i))
		if types.StringWasm32() {
			// wasm32 (types.StringWasm32): a string of 1 to 15
			// bytes is the two uint64s of the words 1 and 2 of
			// 8-byte words (word 0 is no address: nothing is set in
			// the array of the data's pointers); a longer one is
			// the address of its bytes, its length, and zero.
			if slen >= 1 && slen <= types.StringInlineCap {
				var b [16]byte
				copy(b[:], i)
				b[15] |= byte(slen) << 4
				s.WriteInt(gd.Ctxt, noff, 8, int64(binary.LittleEndian.Uint64(b[:8])))
				s.WriteInt(gd.Ctxt, noff+8, 8, int64(binary.LittleEndian.Uint64(b[8:])))
				break
			}
			s.WriteAddr(gd.Ctxt, noff+types.StringPtrOffset, types.PtrSize, StringSym(gd, n.Pos(), i), 0)
			s.WriteInt(gd.Ctxt, noff+types.StringLenOffset, types.PtrSize, slen)
			s.WriteInt(gd.Ctxt, noff+types.StringHashOffset, 8, 0)
			break
		}
		if types.StringTwoWords() {
			// Two words: the address of the bytes (nil for "") and
			// the length. A constant is never inline, however short
			// (types.StringTagged): its bytes are in rodata.
			if slen == 0 {
				s.WriteInt(gd.Ctxt, noff+types.StringPtrOffset, types.PtrSize, 0)
			} else {
				s.WriteAddr(gd.Ctxt, noff+types.StringPtrOffset, types.PtrSize, StringSym(gd, n.Pos(), i), 0)
			}
			s.WriteInt(gd.Ctxt, noff+types.StringLenOffset, types.PtrSize, slen)
			break
		}
		// GOWASM=gc with 8-byte words: three words.
		if types.PtrSize == 8 && slen >= 1 && slen <= 15 {
			var w1buf [8]byte
			copy(w1buf[:], i)
			word1 := int64(binary.LittleEndian.Uint64(w1buf[:]))

			var w2buf [8]byte
			if slen > 8 {
				copy(w2buf[:], i[8:])
			}
			const lenMask = 1<<60 - 1
			word2 := int64(binary.LittleEndian.Uint64(w2buf[:]))&lenMask | slen<<60
			s.WriteInt(gd.Ctxt, noff+types.StringPtrOffset, types.PtrSize, 0)
			s.WriteInt(gd.Ctxt, noff+types.StringHashOffset, types.PtrSize, word1)
			s.WriteInt(gd.Ctxt, noff+types.StringLenOffset, types.PtrSize, word2)
			break
		}
		symdata := StringSym(gd, n.Pos(), i)
		s.WriteAddr(gd.Ctxt, noff+types.StringPtrOffset, types.PtrSize, symdata, 0)
		s.WriteInt(gd.Ctxt, noff+types.StringHashOffset, types.PtrSize, 0)
		s.WriteInt(gd.Ctxt, noff+types.StringLenOffset, types.PtrSize, slen)

	default:
		gd.Fatalf("InitConst unhandled OLITERAL %v", c)
	}
}

// addShareRecord gives the symbol from, which the program has if the
// function may be called as one of the class key, the record of the
// function for the linker (ir/sharelink.go): name is the kind of class
// and the function's name.
func addShareRecord(gd *base.Invocation, from *obj.LSym, name, key string, params []*types.Field, shares []ir.ParamShare, stores uint16) {
	addShareRecordData(gd, from, name, func() []byte { return ir.ShareRecord(key, params, shares, stores) })
}

func addShareRecordData(gd *base.Invocation, from *obj.LSym, name string, record func() []byte) {
	if !ir.LocalSpans(gd) {
		return
	}
	rec := gd.Ctxt.LookupInit(ir.ShareRecPrefix+name, func(s *obj.LSym) {
		data := record()
		s.WriteBytes(gd.Ctxt, 0, data)
		gd.Ctxt.Globl(s, int64(len(data)), obj.DUPOK|obj.RODATA|obj.NOPTR)
	})
	from.AddRel(gd.Ctxt, obj.Reloc{Type: objabi.R_KEEP, Sym: rec})
}

// ShareRecords gives the function fn, which is compiled, its records
// for the linker: as a method, which an interface may call, and as a
// function that only exists as a value (a closure, a method value).
// A function with a name has the record of its value where that is
// written (WriteFuncSyms).
func ShareRecords(gd *base.Invocation, fn *ir.Func) {
	if fn.LSym == nil || fn.Sym() == nil {
		return
	}
	t := fn.Type()
	switch {
	case t.Recv() != nil:
		name := fn.Sym().Name
		name = name[strings.LastIndex(name, ".")+1:]
		if key, _ := ir.ShareKeyMethod(name, t); key != "" {
			shares := fn.ParamShares
			if len(shares) != 0 {
				shares = shares[1:]
			}
			stores := uint16(ir.StoresAll)
			if len(fn.ParamShares) != 0 {
				stores = fn.DirtyInto
			}
			all := fn.ParamShares
			addShareRecordData(gd, fn.LSym, "M."+fn.LSym.Name, func() []byte {
				return ir.ShareRecordOf(key, t.Params(), shares, stores>>1, all, 1, len(all) != 0)
			})
			// And of what it does with its receiver.
			rkey, _ := ir.ShareKeyRecv(name, t)
			var ps *ir.ParamShare
			if len(fn.ParamShares) != 0 {
				ps = &fn.ParamShares[0]
			}
			addShareRecordData(gd, fn.LSym, "R."+fn.LSym.Name, func() []byte { return ir.ShareRecvRecord(rkey, t.Recv(), ps, stores&1 != 0) })
		} else {
			gd.FatalfAt(fn.Pos(), "no class of methods for %v", fn)
		}
	case fn.OClosure != nil || fn.Needctxt():
		if key, _ := ir.ShareKeyFunc(t); key != "" {
			stores := uint16(ir.StoresAll)
			if len(fn.ParamShares) != 0 {
				stores = fn.DirtyInto
			}
			addShareRecord(gd, fn.LSym, "F."+fn.LSym.Name, key, t.Params(), fn.ParamShares, stores)
		}
	}
}

// ShareRecordUnknown gives the symbol from the record of the method fn,
// which is not compiled here (assembly): it may do anything.
func ShareRecordUnknown(gd *base.Invocation, from *obj.LSym, fn *ir.Func) {
	t := fn.Type()
	if t.Recv() == nil || fn.Sym() == nil {
		return
	}
	name := fn.Sym().Name
	name = name[strings.LastIndex(name, ".")+1:]
	key, _ := ir.ShareKeyMethod(name, t)
	if key == "" {
		gd.FatalfAt(fn.Pos(), "no class of methods for %v", fn)
	}
	// (With no notes, the parameters are kept and shared.)
	params := make([]*types.Field, len(t.Params()))
	for i, f := range t.Params() {
		params[i] = types.NewField(f.Pos, f.Sym, f.Type)
	}
	addShareRecord(gd, from, "M."+ir.LinkFuncName(fn), key, params, nil, ir.StoresAll)
	rkey, _ := ir.ShareKeyRecv(name, t)
	addShareRecordData(gd, from, "R."+ir.LinkFuncName(fn), func() []byte { return ir.ShareRecvRecord(rkey, t.Recv(), nil, true) })
}
