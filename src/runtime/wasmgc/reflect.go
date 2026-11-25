// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"unsafe"
)

// What packages reflect and internal/reflectlite ask of the runtime:
// values of types known only by their descriptors.
//
// A value is read and written a scalar at a time, through pointers of
// any kind (the accessors the linker generates find the field), so
// what matters of a type is the list of its scalars, which typeScalars
// walks. An object is made by the function the linker generates for
// each type of the program, whose number it leaves in the descriptor
// in place of the GC bitmap, which nothing needs here.

// gcnew returns a new zeroed object of the type the linker numbered n.
// The compiler replaces a call to it by a call of that function.
func gcnew(n uintptr) unsafe.Pointer

// newobjectT returns a new zeroed value of type t.
func newobjectT(t *_type) unsafe.Pointer {
	memstatsMallocs++
	if splitMode {
		return newarray(t, 1)
	}
	n := uintptr(unsafe.Pointer(t.GCData))
	if n == 0 && t.Kind() == abi.Array {
		// A type made by reflect.ArrayOf: element by element.
		at := (*abi.ArrayType)(unsafe.Pointer(t))
		if t.Size_ == 0 {
			return unsafe.Pointer(&zerobase)
		}
		return newarrayT(at.Elem, int(at.Len))
	}
	if n == 0 && t.Kind() == abi.Struct {
		// A type made by reflect.StructOf: in linear memory, or, with
		// pointers, a wasm.generic (by its size, as a negative
		// constructor number), which the accessors know.
		if t.Size_ == 0 {
			return unsafe.Pointer(&zerobase)
		}
		if !t.Pointers() {
			return makeslicelin(int(t.Size_), 1, 1)
		}
		return gcnew(1<<31 | t.Size_)
	}
	if n == 0 {
		fatal("GOWASM=gc: cannot allocate a value of type " + toRType(t).string() + ", which the program did not have when it was linked")
	}
	return gcnew(n)
}

// The kinds of scalar, as in the names of WebAssembly types.
const (
	scalarB = 'b' // 8 bits
	scalarH = 'h' // 16
	scalarW = 'w' // 32
	scalarL = 'l' // 64
	scalarF = 'f' // float32
	scalarD = 'd' // float64
	scalarR = 'r' // pointer

	// scalarWord is an int, a uintptr, or a word of a string, a
	// slice or an interface: 32 or 64 bits, as goarch.PtrSize.
	scalarWord = scalarW*(goarch.PtrSize/8^1) + scalarL*(goarch.PtrSize/8)
)

// typeScalars calls do for each scalar of a value of type t at off.
func typeScalars(t *_type, off uintptr, do func(k byte, off uintptr)) {
	switch t.Kind() {
	case abi.Bool, abi.Int8, abi.Uint8:
		do(scalarB, off)
	case abi.Int16, abi.Uint16:
		do(scalarH, off)
	case abi.Int32, abi.Uint32:
		do(scalarW, off)
	case abi.Int64, abi.Uint64:
		do(scalarL, off)
	case abi.Int, abi.Uint, abi.Uintptr:
		do(scalarWord, off)
	case abi.Float32:
		do(scalarF, off)
	case abi.Float64:
		do(scalarD, off)
	case abi.Complex64:
		do(scalarF, off)
		do(scalarF, off+4)
	case abi.Complex128:
		do(scalarD, off)
		do(scalarD, off+8)
	case abi.Pointer, abi.UnsafePointer, abi.Map, abi.Chan, abi.Func:
		do(scalarR, off)
	case abi.String, abi.Slice:
		do(scalarR, off)
		do(scalarWord, off+goarch.PtrSize)
		do(scalarWord, off+2*goarch.PtrSize)
	case abi.Interface:
		do(scalarWord, off)
		do(scalarR, off+goarch.PtrSize)
		do(scalarD, off+2*goarch.PtrSize)
		do(scalarD, off+2*goarch.PtrSize+8)
	case abi.Array:
		at := (*abi.ArrayType)(unsafe.Pointer(t))
		for i := uintptr(0); i < at.Len; i++ {
			typeScalars(at.Elem, off+i*at.Elem.Size_, do)
		}
	case abi.Struct:
		st := (*abi.StructType)(unsafe.Pointer(t))
		for i := range st.Fields {
			f := &st.Fields[i]
			typeScalars(f.Typ, off+f.Offset, do)
		}
	}
}

func copyScalar(k byte, dst, src unsafe.Pointer) {
	switch k {
	case scalarB:
		*(*uint8)(dst) = *(*uint8)(src)
	case scalarH:
		*(*uint16)(dst) = *(*uint16)(src)
	case scalarW:
		*(*uint32)(dst) = *(*uint32)(src)
	case scalarL:
		*(*uint64)(dst) = *(*uint64)(src)
	case scalarF:
		*(*float32)(dst) = *(*float32)(src)
	case scalarD:
		*(*float64)(dst) = *(*float64)(src)
	case scalarR:
		*(*unsafe.Pointer)(dst) = *(*unsafe.Pointer)(src)
	}
}

func clearScalar(k byte, p unsafe.Pointer) {
	switch k {
	case scalarB:
		*(*uint8)(p) = 0
	case scalarH:
		*(*uint16)(p) = 0
	case scalarW:
		*(*uint32)(p) = 0
	case scalarL:
		*(*uint64)(p) = 0
	case scalarF:
		*(*float32)(p) = 0
	case scalarD:
		*(*float64)(p) = 0
	case scalarR:
		*(*unsafe.Pointer)(p) = nil
	}
}

// typedmemmove copies a value of type t from src to dst.
func typedmemmove(t *_type, dst, src unsafe.Pointer) {
	if dst == src || t.Size_ == 0 {
		return
	}
	if splitMode {
		splitmove(dst, src, t.Size_)
		return
	}
	if d, s := trylinaddr(dst), trylinaddr(src); d != 0 && s != 0 {
		lincopy(d, s, t.Size_)
		return
	}
	if gcmove(dst, src, t.Size_) {
		return
	}
	typeScalars(t, 0, func(k byte, off uintptr) {
		copyScalar(k, add(dst, off), add(src, off))
	})
}

// gcmove copies the size bytes of a Go value at src to dst, if both
// are within objects that are structs, and reports whether they are;
// gczero zeroes those at p likewise. The compiler replaces a call to
// them by a call of the functions the linker generates, which go by
// the layouts of the objects: the fields in those bytes, whatever the
// type of the value.
func gcmove(dst, src unsafe.Pointer, size uintptr) bool
func gczero(p unsafe.Pointer, size uintptr) bool

// typedmemclr zeroes the value of type t at p.
func typedmemclr(t *_type, p unsafe.Pointer) {
	if t.Size_ == 0 {
		return
	}
	if splitMode {
		splitzero(p, t.Size_)
		return
	}
	if a := trylinaddr(p); a != 0 {
		linzero(a, t.Size_)
		return
	}
	if gczero(p, t.Size_) {
		return
	}
	typeScalars(t, 0, func(k byte, off uintptr) {
		clearScalar(k, add(p, off))
	})
}

// typedslicecopy copies the elements of type t of one slice to another.
func typedslicecopy(t *_type, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int {
	n := dstLen
	if srcLen < n {
		n = srcLen
	}
	if n == 0 || dstPtr == srcPtr {
		return n
	}
	size := t.Size_
	if splitMode {
		splitmove(dstPtr, srcPtr, uintptr(n)*size)
		return n
	}
	if d, s := trylinaddr(dstPtr), trylinaddr(srcPtr); d != 0 && s != 0 {
		lincopy(d, s, uintptr(n)*size)
		return n
	}
	if boxobj(dstPtr) == boxobj(srcPtr) && boxoff(dstPtr) > boxoff(srcPtr) {
		// Backward, in case they overlap.
		for i := n - 1; i >= 0; i-- {
			typedmemmove(t, add(dstPtr, uintptr(i)*size), add(srcPtr, uintptr(i)*size))
		}
		return n
	}
	for i := 0; i < n; i++ {
		typedmemmove(t, add(dstPtr, uintptr(i)*size), add(srcPtr, uintptr(i)*size))
	}
	return n
}

// newarrayT returns the array of n zeroed elements of type t that a
// slice of them would have.
func newarrayT(t *_type, n int) unsafe.Pointer {
	if n < 0 {
		panicmakeslicelen()
	}
	if splitMode {
		return newarray(t, n)
	}
	if !t.Pointers() {
		return makeslicelin(int(t.Size_), n, n)
	}
	if isRefType(t) {
		s := make([]unsafe.Pointer, n)
		return unsafe.Pointer(unsafe.SliceData(s))
	}
	return newobjarrayT(t, n)
}

// newobjarrayT returns an array of n new objects of type t, as an
// array of structs is inside an object.
func newobjarrayT(t *_type, n int) unsafe.Pointer {
	a := newobjarray(n)
	for i := 0; i < n; i++ {
		objarrayset(a, i, newobjectT(t))
	}
	return a
}

// isRefType reports whether a value of type t is a single reference,
// as an element of an array of references.
func isRefType(t *_type) bool {
	switch t.Kind() {
	case abi.Pointer, abi.UnsafePointer, abi.Map, abi.Chan, abi.Func:
		return true
	case abi.Array:
		at := (*abi.ArrayType)(unsafe.Pointer(t))
		return at.Len == 1 && isRefType(at.Elem)
	case abi.Struct:
		st := (*abi.StructType)(unsafe.Pointer(t))
		var ref bool
		for i := range st.Fields {
			f := &st.Fields[i]
			if f.Typ.Size_ == 0 {
				continue
			}
			if ref || f.Offset != 0 || !isRefType(f.Typ) {
				return false
			}
			ref = true
		}
		return ref
	}
	return false
}

// convT and convTnoptr return a pointer to a copy of the value of type
// t at v, for an interface.
func convT(t *_type, v unsafe.Pointer) unsafe.Pointer {
	x := newobjectT(t)
	typedmemmove(t, x, v)
	return x
}

func convTnoptr(t *_type, v unsafe.Pointer) unsafe.Pointer {
	return convT(t, v)
}

// moveSliceNoCapNoScan moves the elements of a slice from the stack to
// linear memory of its own.
func moveSliceNoCapNoScan(elemSize uintptr, old unsafe.Pointer, len int) (unsafe.Pointer, int, int) {
	p := makeslicelin(int(elemSize), len, len)
	memmove(p, old, uintptr(len)*elemSize)
	return p, len, len
}

func moveSliceNoScan(elemSize uintptr, old unsafe.Pointer, len, cap int) (unsafe.Pointer, int, int) {
	p := makeslicelin(int(elemSize), len, cap)
	memmove(p, old, uintptr(len)*elemSize)
	return p, len, cap
}

// Package reflect.

//go:linkname reflect_unsafe_New reflect.unsafe_New
func reflect_unsafe_New(t *_type) unsafe.Pointer { return newobjectT(t) }

//go:linkname reflectlite_unsafe_New internal/reflectlite.unsafe_New
func reflectlite_unsafe_New(t *_type) unsafe.Pointer { return newobjectT(t) }

//go:linkname reflect_unsafe_NewArray reflect.unsafe_NewArray
func reflect_unsafe_NewArray(t *_type, n int) unsafe.Pointer { return newarrayT(t, n) }

//go:linkname reflect_typedmemmove reflect.typedmemmove
func reflect_typedmemmove(t *_type, dst, src unsafe.Pointer) { typedmemmove(t, dst, src) }

//go:linkname reflectlite_typedmemmove internal/reflectlite.typedmemmove
func reflectlite_typedmemmove(t *_type, dst, src unsafe.Pointer) { typedmemmove(t, dst, src) }

//go:linkname reflect_typedmemclr reflect.typedmemclr
func reflect_typedmemclr(t *_type, p unsafe.Pointer) { typedmemclr(t, p) }

//go:linkname reflect_typedmemclrpartial reflect.typedmemclrpartial
func reflect_typedmemclrpartial(t *_type, p unsafe.Pointer, off, size uintptr) {
	if splitMode {
		splitzero(add(p, off), size)
		return
	}
	typeScalars(t, 0, func(k byte, o uintptr) {
		if o >= off && o < off+size {
			clearScalar(k, add(p, o))
		}
	})
}

//go:linkname reflect_typedarrayclear reflect.typedarrayclear
func reflect_typedarrayclear(t *_type, p unsafe.Pointer, n int) {
	for i := 0; i < n; i++ {
		typedmemclr(t, add(p, uintptr(i)*t.Size_))
	}
}

type sliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

//go:linkname reflect_typedslicecopy reflect.typedslicecopy
func reflect_typedslicecopy(t *_type, dst, src sliceHeader) int {
	return typedslicecopy(t, dst.Data, dst.Len, src.Data, src.Len)
}

//go:linkname reflect_growslice reflect.growslice
func reflect_growslice(t *_type, old sliceHeader, num int) sliceHeader {
	newLen := old.Len + num
	// (The capacity Go would give: see roundupcap.)
	newCap := nextslicecap(newLen, old.Cap)
	if t.Size_ != 0 {
		newCap = roundupcap(newCap, int(t.Size_))
	}
	p := newarrayT(t, newCap)
	typedslicecopy(t, p, old.Len, old.Data, old.Len)
	return sliceHeader{p, old.Len, newCap}
}

//go:linkname reflect_unsafeslice reflect.unsafeslice
func reflect_unsafeslice(t *_type, ptr unsafe.Pointer, len int) {
	if len < 0 {
		panic(plainError("unsafe.Slice: len out of range"))
	}
	if ptr == nil && len > 0 {
		panic(plainError("unsafe.Slice: ptr is nil and len is not zero"))
	}
	// The size, and the memory it spans, within the address space.
	size := t.Size_
	if size != 0 && uintptr(len) > ^uintptr(0)/size || uintptr(len)*size > -uintptr(ptr) {
		panic(plainError("unsafe.Slice: len out of range"))
	}
	// A pointer to linear memory, where addresses end at linLimit
	// (with split, every pointer is an address of 32 bits).
	if a := trylinaddr(ptr); splitMode && uint64(uintptr(len)*size) > 1<<32-uint64(a) {
		panic(plainError("unsafe.Slice: len out of range"))
	}
	if a := trylinaddr(ptr); a != 0 && uintptr(len)*size > linLimit-a && !splitMode {
		panic(plainError("unsafe.Slice: len out of range"))
	}
}

//go:linkname reflect_memmove reflect.memmove
func reflect_memmove(dst, src unsafe.Pointer, n uintptr) { memmove(dst, src, n) }

//go:linkname reflect_typehash reflect.typehash
func reflect_typehash(t *_type, p unsafe.Pointer, h uintptr) uintptr { return typehash(t, p, h) }

//go:linkname reflect_verifyNotInHeapPtr reflect.verifyNotInHeapPtr
func reflect_verifyNotInHeapPtr(p uintptr) bool { return true }

//go:linkname reflect_ifaceE2I reflect.ifaceE2I
func reflect_ifaceE2I(t *_type, e any, dst unsafe.Pointer) {
	ifaceE2I(t, e, dst)
}

//go:linkname reflectlite_ifaceE2I internal/reflectlite.ifaceE2I
func reflectlite_ifaceE2I(t *_type, e any, dst unsafe.Pointer) {
	ifaceE2I(t, e, dst)
}

func ifaceE2I(t *_type, e any, dst unsafe.Pointer) {
	x := efaceOf(&e)
	d := (*iface)(dst)
	d.tab = typeWordOf(unsafe.Pointer(getitab((*interfacetype)(unsafe.Pointer(t)), (*_type)(unsafe.Pointer(x._type)), false)))
	d.data = x.data
	d.w0 = x.w0
	d.w1 = x.w1
}

//go:linkname reflect_resolveNameOff reflect.resolveNameOff
func reflect_resolveNameOff(ptrInModule unsafe.Pointer, off int32) unsafe.Pointer {
	if off < 0 {
		return reflectOffs[-1-off]
	}
	return unsafe.Pointer(toRType(nil).nameOff(nameOff(off)).Bytes)
}

//go:linkname reflectlite_resolveNameOff internal/reflectlite.resolveNameOff
func reflectlite_resolveNameOff(ptrInModule unsafe.Pointer, off int32) unsafe.Pointer {
	if off < 0 {
		return reflectOffs[-1-off]
	}
	return unsafe.Pointer(toRType(nil).nameOff(nameOff(off)).Bytes)
}

//go:linkname reflect_resolveTypeOff reflect.resolveTypeOff
func reflect_resolveTypeOff(rtype unsafe.Pointer, off int32) unsafe.Pointer {
	if off < -1 {
		return reflectOffs[-1-off]
	}
	return unsafe.Pointer(toRType(nil).typeOff(typeOff(off)))
}

//go:linkname reflectlite_resolveTypeOff internal/reflectlite.resolveTypeOff
func reflectlite_resolveTypeOff(rtype unsafe.Pointer, off int32) unsafe.Pointer {
	if off < -1 {
		return reflectOffs[-1-off]
	}
	return unsafe.Pointer(toRType(nil).typeOff(typeOff(off)))
}

//go:linkname reflect_resolveTextOff reflect.resolveTextOff
func reflect_resolveTextOff(rtype unsafe.Pointer, off int32) unsafe.Pointer {
	return unsafe.Pointer(toRType(nil).textOff(textOff(off)))
}

// reflectOffs are the names and types that package reflect makes and
// refers to by offsets, as if they were in the program: offset -1-i is
// reflectOffs[i].
var reflectOffs []unsafe.Pointer

// reflectOffsInv finds the index of a pointer in reflectOffs.
var reflectOffsInv map[unsafe.Pointer]int32

//go:linkname reflect_addReflectOff reflect.addReflectOff
func reflect_addReflectOff(ptr unsafe.Pointer) int32 {
	if id, ok := reflectOffsInv[ptr]; ok {
		return id
	}
	if reflectOffsInv == nil {
		reflectOffsInv = make(map[unsafe.Pointer]int32)
	}
	reflectOffs = append(reflectOffs, ptr)
	id := int32(-len(reflectOffs))
	reflectOffsInv[ptr] = id
	return id
}

var typelinks []*_type

// reflect_compiledTypelinks returns the type descriptors of the
// program, which the linker put one after the other.
//
//go:linkname reflect_compiledTypelinks reflect.compiledTypelinks
func reflect_compiledTypelinks() ([]*_type, [][]*_type) {
	if typelinks == nil {
		md := &firstmoduledata
		// (Past the word that the linker puts first, as Go's
		// moduleTypelinks does; descriptors are aligned to a word.)
		td := md.types + goarch.PtrSize
		end := md.types + md.typedesclen
		for td < end {
			td = (td + goarch.PtrSize - 1) &^ (goarch.PtrSize - 1)
			t := (*_type)(unsafe.Pointer(td))
			typelinks = append(typelinks, t)
			td += uintptr(t.DescriptorSize())
		}
	}
	return typelinks, nil
}

//go:linkname reflect_adjustAIXGCDataForRuntime reflect.adjustAIXGCDataForRuntime
func reflect_adjustAIXGCDataForRuntime(p *byte) *byte { return p }

//go:linkname reflect_makeFuncStub reflect.makeFuncStub
func reflect_makeFuncStub() { fatal("reflect.MakeFunc is not supported with GOWASM=gc") }

//go:linkname reflect_methodValueCall reflect.methodValueCall
func reflect_methodValueCall() { fatal("reflect method values are not supported with GOWASM=gc") }

// Maps.

// mapKeyArg returns a pointer to the value of type t at p that the map
// functions can keep: an object of its own if it has pointers.
func mapKeyArg(t *_type, p unsafe.Pointer) unsafe.Pointer {
	if !t.Pointers() {
		return p
	}
	x := newobjectT(t)
	memstatsMallocs-- // (a copy that Go does not make)
	typedmemmove(t, x, p)
	return x
}

//go:linkname reflect_makemap reflect.makemap
func reflect_makemap(t *abi.MapType, cap int) unsafe.Pointer {
	return unsafe.Pointer(makemap(t, cap, nil))
}

//go:linkname reflect_mapaccess reflect.mapaccess
func reflect_mapaccess(t *abi.MapType, m *gcmap, key unsafe.Pointer) unsafe.Pointer {
	if m == nil || m.used == 0 {
		// (A key that cannot be hashed panics all the same.)
		mapKeyCheck(t, key)
		return nil
	}
	i := m.find(t, key)
	if i < 0 {
		return nil
	}
	return m.tab.elems.ptr(i)
}

//go:linkname reflect_mapaccess_faststr reflect.mapaccess_faststr
func reflect_mapaccess_faststr(t *abi.MapType, m *gcmap, key string) unsafe.Pointer {
	k := new(string)
	*k = key
	return reflect_mapaccess(t, m, unsafe.Pointer(k))
}

//go:linkname reflect_mapassign0 reflect.mapassign0
func reflect_mapassign0(t *abi.MapType, m *gcmap, key, elem unsafe.Pointer) {
	var zero unsafe.Pointer
	if t.Elem.Pointers() && !isRefType(t.Elem) {
		// (An element that is a single reference is not an object.)
		zero = newobjectT(t.Elem)
	}
	p := mapassign(t, m, mapKeyArg(t.Key, key), zero)
	typedmemmove(t.Elem, p, elem)
}

//go:linkname reflect_mapassign_faststr0 reflect.mapassign_faststr0
func reflect_mapassign_faststr0(t *abi.MapType, m *gcmap, key string, elem unsafe.Pointer) {
	k := new(string)
	*k = key
	reflect_mapassign0(t, m, unsafe.Pointer(k), elem)
}

//go:linkname reflect_mapdelete reflect.mapdelete
func reflect_mapdelete(t *abi.MapType, m *gcmap, key unsafe.Pointer) { mapdelete(t, m, key) }

//go:linkname reflect_mapdelete_faststr reflect.mapdelete_faststr
func reflect_mapdelete_faststr(t *abi.MapType, m *gcmap, key string) {
	k := new(string)
	*k = key
	mapdelete(t, m, unsafe.Pointer(k))
}

//go:linkname reflect_maplen reflect.maplen
func reflect_maplen(m *gcmap) int {
	if m == nil {
		return 0
	}
	return int(m.used)
}

//go:linkname reflectlite_maplen internal/reflectlite.maplen
func reflectlite_maplen(m *gcmap) int { return reflect_maplen(m) }

//go:linkname reflect_mapclear reflect.mapclear
func reflect_mapclear(t *abi.MapType, m *gcmap) { mapclear(t, m) }

//go:linkname reflect_mapiterinit reflect.mapiterinit
func reflect_mapiterinit(t *abi.MapType, m *gcmap) unsafe.Pointer {
	it := new(mapIter)
	mapIterStart(t, m, it)
	return unsafe.Pointer(it)
}

//go:linkname reflect_mapiterkey reflect.mapiterkey
func reflect_mapiterkey(it *mapIter) unsafe.Pointer { return it.key }

//go:linkname reflect_mapiterelem reflect.mapiterelem
func reflect_mapiterelem(it *mapIter) unsafe.Pointer { return it.elem }

//go:linkname reflect_mapiternext reflect.mapiternext
func reflect_mapiternext(it *mapIter) { mapIterNext(it) }

// Channels.

//go:linkname reflect_makechan reflect.makechan
func reflect_makechan(t *abi.ChanType, size int) unsafe.Pointer {
	var zero unsafe.Pointer
	if t.Elem.Pointers() && !isRefType(t.Elem) {
		// (An element that is a single reference is not an object.)
		zero = newobjectT(t.Elem)
	}
	return unsafe.Pointer(makechan(t, size, zero))
}

//go:linkname reflect_chanlen reflect.chanlen
func reflect_chanlen(c *hchan) int { return chanlen(c) }

//go:linkname reflectlite_chanlen internal/reflectlite.chanlen
func reflectlite_chanlen(c *hchan) int { return chanlen(c) }

//go:linkname reflect_chancap reflect.chancap
func reflect_chancap(c *hchan) int { return chancap(c) }

//go:linkname reflect_chanclose reflect.chanclose
func reflect_chanclose(c *hchan) { closechan(c) }

//go:linkname reflect_chansend0 reflect.chansend0
func reflect_chansend0(c *hchan, elem unsafe.Pointer, nb bool) bool {
	return chansend(c, mapKeyArg(c.elemtype, elem), !nb)
}

//go:linkname reflect_chanrecv reflect.chanrecv
func reflect_chanrecv(c *hchan, nb bool, elem unsafe.Pointer) (selected, received bool) {
	return chanrecv(c, elem, !nb)
}

// runtimeSelect is reflect's: a case of reflect.Select.
type runtimeSelect struct {
	dir int // 1 send, 2 receive, 3 default
	typ unsafe.Pointer
	ch  *hchan
	val unsafe.Pointer
}

//go:linkname reflect_rselect reflect.rselect
func reflect_rselect(cases []runtimeSelect) (int, bool) {
	if len(cases) == 0 {
		block()
	}
	sel := make([]scase, len(cases))
	orig := make([]int, len(cases))
	nsends, nrecvs := 0, 0
	dflt := -1
	for i, rc := range cases {
		var j int
		switch rc.dir {
		case 3:
			dflt = i
			continue
		case 1:
			j = nsends
			nsends++
			if rc.ch != nil {
				rc.val = mapKeyArg(rc.ch.elemtype, rc.val)
			}
		case 2:
			nrecvs++
			j = len(cases) - nrecvs
		}
		sel[j] = scase{c: rc.ch, elem: rc.val}
		orig[j] = i
	}
	if nsends+nrecvs == 0 {
		return dflt, false
	}
	if nsends+nrecvs < len(cases) {
		copy(sel[nsends:], sel[len(cases)-nrecvs:])
		copy(orig[nsends:], orig[len(cases)-nrecvs:])
	}
	order := make([]uint16, 2*(nsends+nrecvs))
	// (The array, not &sel[0]: an element that is an object does not
	// know the array it is in.)
	chosen, recvOK := selectgo(unsafe.SliceData(sel), unsafe.SliceData(order), nil, nsends, nrecvs, dflt == -1)
	if chosen < 0 {
		chosen = dflt
	} else {
		chosen = orig[chosen]
	}
	return chosen, recvOK
}

// zeroelem returns the zero value of the elements of the map or
// channel type t if they are objects, for the functions that the
// compiler gives it to (see zeroElemArg there).
func zeroelem(t *_type) unsafe.Pointer {
	var e *_type
	if t.Kind() == abi.Map {
		e = (*abi.MapType)(unsafe.Pointer(t)).Elem
	} else {
		e = (*abi.ChanType)(unsafe.Pointer(t)).Elem
	}
	if !e.Pointers() {
		return nil
	}
	return newobjectT(e)
}

// mallocgc allocates a value of type typ, or n bytes without pointers if
// typ is nil.
func mallocgc(size uintptr, typ *_type, needzero bool) unsafe.Pointer {
	if typ == nil || !typ.Pointers() {
		return linnew(size)
	}
	return newobjectT(typ)
}

// growsliceBuf is growslice for an append that may use bufPtr, an
// array of bufLen elements on the stack, while it is big enough.
func growsliceBuf(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type, bufPtr unsafe.Pointer, bufLen int) (unsafe.Pointer, int, int) {
	oldLen := newLen - num
	if newLen > bufLen {
		newCap := nextslicecap(newLen, oldCap)
		p := newarrayT(et, newCap)
		typedslicecopy(et, p, oldLen, oldPtr, oldLen)
		return p, newLen, newCap
	}
	if oldPtr != bufPtr && oldLen != 0 {
		typedslicecopy(et, bufPtr, oldLen, oldPtr, oldLen)
	}
	for i := newLen; i < bufLen; i++ {
		typedmemclr(et, add(bufPtr, uintptr(i)*et.Size_))
	}
	return bufPtr, newLen, bufLen
}

func growsliceBufNoAlias(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type, bufPtr unsafe.Pointer, bufLen int) (unsafe.Pointer, int, int) {
	return growsliceBuf(oldPtr, newLen, oldCap, num, et, bufPtr, bufLen)
}

// gcreflectcall calls the function of func type t whose code and
// closure are given, with the arguments that args points to, storing
// the results where results points to (both arrays of pointers); with
// a receiver first if flags&1. It reports whether the program has the
// means to call a function of that type. The compiler replaces a call
// to it by a call to the linker's wasm.reflectcall.
func gcreflectcall(t *_type, code uintptr, ctxt, args, results, rcvr unsafe.Pointer, flags int) bool

// gcmakefunc returns the code of the function of func type t that
// reflect.MakeFunc makes, or 0. The compiler replaces a call to it by a
// call to the linker's wasm.makefunc.
func gcmakefunc(t *_type) uintptr

//go:linkname reflect_gcreflectcall reflect.gcreflectcall
func reflect_gcreflectcall(t *_type, code uintptr, ctxt, args, results, rcvr unsafe.Pointer, flags int) bool {
	if p := curg.panicking; p != nil && p.passed {
		// The method of a deferred method value: its caller's frame
		// ends here (see reflect_passDeferredCall).
		p.passed = false
		p.argp = linsp()
	}
	return gcreflectcall(t, code, ctxt, args, results, rcvr, flags)
}

//go:linkname reflect_gcmakefunc reflect.gcmakefunc
func reflect_gcmakefunc(t *_type) uintptr { return gcmakefunc(t) }

// gcbits returns the pointer bitmap of the type of x, for reflect's
// tests: there is none (objects know their pointers).
//
//go:linkname reflect_gcbits reflect.gcbits
func reflect_gcbits(x any) []byte { return nil }
