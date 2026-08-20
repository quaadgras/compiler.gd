// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/byteorder"
	"internal/goarch"
	"internal/runtime/maps"
	"internal/runtime/sys"
	"unsafe"
)

const (
	// We use 32-bit hash on Wasm, see hash32.go.
	hashSize = (1-goarch.IsWasm)*goarch.PtrSize + goarch.IsWasm*4
	c0       = uintptr((8-hashSize)/4*2860486313 + (hashSize-4)/4*33054211828000289)
	c1       = uintptr((8-hashSize)/4*3267000013 + (hashSize-4)/4*23344194077549503)
)

func trimHash(h uintptr) uintptr {
	if goarch.IsWasm != 0 {
		// On Wasm, we use 32-bit hash, despite that uintptr is 64-bit.
		// memhash* always returns a uintptr with high 32-bit being 0
		// (see hash32.go). We trim the hash in other places where we
		// compute the hash manually, e.g. in interhash.
		return uintptr(uint32(h))
	}
	return h
}

func memhash0(p unsafe.Pointer, h uintptr) uintptr {
	return h
}

func memhash8(p unsafe.Pointer, h uintptr) uintptr {
	return memhash(p, h, 1)
}

func memhash16(p unsafe.Pointer, h uintptr) uintptr {
	return memhash(p, h, 2)
}

func memhash128(p unsafe.Pointer, h uintptr) uintptr {
	return memhash(p, h, 16)
}

//go:nosplit
func memhash_varlen(p unsafe.Pointer, h uintptr) uintptr {
	ptr := sys.GetClosurePtr()
	size := *(*uintptr)(unsafe.Pointer(ptr + unsafe.Sizeof(h)))
	return memhash(p, h, size)
}

// This is simple wrappers.
// It's better to use maps.MemHash functions directly,
// but we have reflection code that still calls hashing from runtime via LookupRuntime,
// so we have to try to minimize overhead of an extra call.
// For this add nosplit for performance

// memhash should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/aacfactory/fns
//   - github.com/dgraph-io/ristretto
//   - github.com/minio/simdjson-go
//   - github.com/nbd-wtf/go-nostr
//   - github.com/outcaste-io/ristretto
//   - github.com/puzpuzpuz/xsync/v2
//   - github.com/puzpuzpuz/xsync/v3
//   - github.com/authzed/spicedb
//   - github.com/pingcap/badger
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:nosplit
//go:linkname memhash
func memhash(p unsafe.Pointer, h, s uintptr) uintptr {
	return maps.MemHash(p, h, s)
}

//go:nosplit
func memhash64(p unsafe.Pointer, seed uintptr) uintptr {
	return maps.MemHash64(readUnaligned64(p), seed)
}

//go:nosplit
func memhash32(p unsafe.Pointer, seed uintptr) uintptr {
	return maps.MemHash32(readUnaligned32(p), seed)
}

// strhash is declared in strhash_asm.go (amd64, arm64: assembly
// implementation in asm_$GOARCH.s) and strhash_noasm.go (everything
// else: strhashFallback). Both keep the go:linkname hall-of-shame
// contract from upstream.
//
// strhashFallback is the gd fork's portable cache-aware string hash.
func strhashFallback(a unsafe.Pointer, h uintptr) uintptr {
	x := (*stringStruct)(a)
	// gd string hash cache: heap-rep strings carry their hash in
	// word 1, populated eagerly by runtime producers (see
	// sealStringHash) or by the compiler's static-init emitter.
	// Inline rep keeps bytes in word 1, so gate the cache check on a
	// non-nil data pointer. h (the caller's seed) is ignored — the
	// fork uses a process-wide fixed seed (aeskeysched loaded from
	// abi.AeskeyschedSeed) so runtime and static-init emit identical
	// values.
	if x.str != nil && x.hash != 0 {
		return uintptr(x.hash)
	}
	// Cache miss. Inline-rep (x.str==nil) reads bytes from the header
	// via bytes() — valid while x is live. Heap-rep with hash==0 means
	// the seal never ran; compute now. On AES-capable CPUs route
	// through memhash so the fast asm aeshashbody path runs; otherwise
	// fall back to the pure-Go aeshash port so the output still
	// matches what the compiler emitted for literals.
	n := x.length()
	if maps.UseAeshash {
		if n == 0 {
			return memhash(nil, 0, 0)
		}
		return memhash(x.bytes(), 0, uintptr(n))
	}
	if n == 0 {
		return uintptr(strhashPort(""))
	}
	return uintptr(strhashPort(unsafe.String((*byte)(x.bytes()), n)))
}

// NOTE: Because NaN != NaN, a map can contain any
// number of (mostly useless) entries keyed with NaNs.
// To avoid long hash chains, we assign a random number
// as the hash value for a NaN.

func f32hash(p unsafe.Pointer, h uintptr) uintptr {
	f := *(*float32)(p)
	switch {
	case f == 0:
		return trimHash(c1 * (c0 ^ h)) // +0, -0
	case f != f:
		return trimHash(c1 * (c0 ^ h ^ uintptr(rand()))) // any kind of NaN
	default:
		return memhash(p, h, 4)
	}
}

func f64hash(p unsafe.Pointer, h uintptr) uintptr {
	f := *(*float64)(p)
	switch {
	case f == 0:
		return trimHash(c1 * (c0 ^ h)) // +0, -0
	case f != f:
		return trimHash(c1 * (c0 ^ h ^ uintptr(rand()))) // any kind of NaN
	default:
		return memhash(p, h, 8)
	}
}

func c64hash(p unsafe.Pointer, h uintptr) uintptr {
	x := (*[2]float32)(p)
	return f32hash(unsafe.Pointer(&x[1]), f32hash(unsafe.Pointer(&x[0]), h))
}

func c128hash(p unsafe.Pointer, h uintptr) uintptr {
	x := (*[2]float64)(p)
	return f64hash(unsafe.Pointer(&x[1]), f64hash(unsafe.Pointer(&x[0]), h))
}

func interhash(p unsafe.Pointer, h uintptr) uintptr {
	a := (*iface)(p)
	tab := a.tab
	if tab == nil {
		return h
	}
	t := tab.Type
	if t.Equal == nil {
		// Check hashability here. We could do this check inside
		// typehash, but we want to report the topmost type in
		// the error text (e.g. in a struct with a field of slice type
		// we want to report the struct, not the slice).
		panic(errorString("hash of unhashable type " + toRType(t).string()))
	}
	if tab.Inline != 0 {
		// gd fat-iface: payload lives in a.inline.
		return trimHash(c1 * typehash(t, unsafe.Pointer(&a.inline), h^c0))
	}
	if t.IsSpreadIface() {
		// gd Phase D: value is split across a.data (word 0) + a.inline
		// (words 1+2). Materialise a contiguous 24 B buffer so typehash
		// sees a normal header. Buffer is stack-local — noescape keeps
		// it off the heap even though typehash takes its address.
		var buf [3]uintptr
		buf[0] = uintptr(a.data)
		*(*[16]byte)(unsafe.Pointer(&buf[1])) = *(*[16]byte)(unsafe.Pointer(&a.inline))
		return trimHash(c1 * typehash(t, noescape(unsafe.Pointer(&buf)), h^c0))
	}
	if t.IsDirectIface() {
		return trimHash(c1 * typehash(t, unsafe.Pointer(&a.data), h^c0))
	} else {
		return trimHash(c1 * typehash(t, a.data, h^c0))
	}
}

// nilinterhash should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/anacrolix/stm
//   - github.com/aristanetworks/goarista
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname nilinterhash
func nilinterhash(p unsafe.Pointer, h uintptr) uintptr {
	a := (*eface)(p)
	t := a._type
	if t == nil {
		return h
	}
	if t.Equal == nil {
		// See comment in interhash above.
		panic(errorString("hash of unhashable type " + toRType(t).string()))
	}
	if t.IsInlineIface() {
		// gd fat-iface: payload lives in a.inline.
		return trimHash(c1 * typehash(t, unsafe.Pointer(&a.inline), h^c0))
	}
	if t.IsSpreadIface() {
		// gd Phase D: reassemble the 24 B header from a.data + a.inline
		// so typehash (for string and slice) sees a stock layout.
		var buf [3]uintptr
		buf[0] = uintptr(a.data)
		*(*[16]byte)(unsafe.Pointer(&buf[1])) = *(*[16]byte)(unsafe.Pointer(&a.inline))
		return trimHash(c1 * typehash(t, noescape(unsafe.Pointer(&buf)), h^c0))
	}
	if t.IsDirectIface() {
		return trimHash(c1 * typehash(t, unsafe.Pointer(&a.data), h^c0))
	} else {
		return trimHash(c1 * typehash(t, a.data, h^c0))
	}
}

// typehash computes the hash of the object of type t at address p.
// h is the seed.
// This function is seldom used. Most maps use for hashing either
// fixed functions (e.g. f32hash) or compiler-generated functions
// (e.g. for a type like struct { x, y string }). This implementation
// is slower but more general and is used for hashing interface types
// (called from interhash or nilinterhash, above) or for hashing in
// maps generated by reflect.MapOf (reflect_typehash, below).
//
// typehash should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/puzpuzpuz/xsync/v2
//   - github.com/puzpuzpuz/xsync/v3
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname typehash
func typehash(t *_type, p unsafe.Pointer, h uintptr) uintptr {
	if t.TFlag&abi.TFlagRegularMemory != 0 {
		// Handle ptr sizes specially, see issue 37086.
		switch t.Size_ {
		case 4:
			return memhash32(p, h)
		case 8:
			return memhash64(p, h)
		default:
			return memhash(p, h, t.Size_)
		}
	}
	switch t.Kind() {
	case abi.Float32:
		return f32hash(p, h)
	case abi.Float64:
		return f64hash(p, h)
	case abi.Complex64:
		return c64hash(p, h)
	case abi.Complex128:
		return c128hash(p, h)
	case abi.String:
		return strhash(p, h)
	case abi.Interface:
		i := (*interfacetype)(unsafe.Pointer(t))
		if len(i.Methods) == 0 {
			return nilinterhash(p, h)
		}
		return interhash(p, h)
	case abi.Array:
		a := (*arraytype)(unsafe.Pointer(t))
		for i := uintptr(0); i < a.Len; i++ {
			h = typehash(a.Elem, add(p, i*a.Elem.Size_), h)
		}
		return h
	case abi.Struct:
		s := (*structtype)(unsafe.Pointer(t))
		for _, f := range s.Fields {
			if f.Name.IsBlank() {
				continue
			}
			h = typehash(f.Typ, add(p, f.Offset), h)
		}
		return h
	default:
		// Should never happen, as typehash should only be called
		// with comparable types.
		panic(errorString("hash of unhashable type " + toRType(t).string()))
	}
}

//go:linkname reflect_typehash reflect.typehash
func reflect_typehash(t *_type, p unsafe.Pointer, h uintptr) uintptr {
	return typehash(t, p, h)
}

func memequal0(p, q unsafe.Pointer) bool {
	return true
}
func memequal8(p, q unsafe.Pointer) bool {
	return *(*int8)(p) == *(*int8)(q)
}
func memequal16(p, q unsafe.Pointer) bool {
	return *(*int16)(p) == *(*int16)(q)
}
func memequal32(p, q unsafe.Pointer) bool {
	return *(*int32)(p) == *(*int32)(q)
}
func memequal64(p, q unsafe.Pointer) bool {
	return *(*int64)(p) == *(*int64)(q)
}
func memequal128(p, q unsafe.Pointer) bool {
	return *(*[2]int64)(p) == *(*[2]int64)(q)
}
func f32equal(p, q unsafe.Pointer) bool {
	return *(*float32)(p) == *(*float32)(q)
}
func f64equal(p, q unsafe.Pointer) bool {
	return *(*float64)(p) == *(*float64)(q)
}
func c64equal(p, q unsafe.Pointer) bool {
	return *(*complex64)(p) == *(*complex64)(q)
}
func c128equal(p, q unsafe.Pointer) bool {
	return *(*complex128)(p) == *(*complex128)(q)
}
func strequal(p, q unsafe.Pointer) bool {
	return *(*string)(p) == *(*string)(q)
}

// streqfast is the fallback implementation of the string-equality
// intrinsic. The compiler lowers calls to this function directly into
// optimised SSA (3-word fast path + decoded len / memequal fallback),
// so this body only runs if the intrinsic is disabled. Kept as a
// non-recursive byte-level compare to avoid infinite recursion if the
// compiler routes `==` back through streqfast.
//
//go:nosplit
func streqfast(s, t string) bool {
	sh := (*stringStruct)(unsafe.Pointer(&s))
	th := (*stringStruct)(unsafe.Pointer(&t))
	sl := sh.length()
	tl := th.length()
	if sl != tl {
		return false
	}
	if sl == 0 {
		return true
	}
	return memequal(sh.bytes(), th.bytes(), uintptr(sl))
}

func interequal(p, q unsafe.Pointer) bool {
	x := (*iface)(p)
	y := (*iface)(q)
	if x.tab != y.tab {
		return false
	}
	// gd fat-iface / Phase D: ifaceeq now takes pointers to the full
	// iface headers so it can handle direct / inline / spread storage
	// internally. Pass p and q through verbatim.
	return ifaceeq(x.tab, p, q)
}
func nilinterequal(p, q unsafe.Pointer) bool {
	x := (*eface)(p)
	y := (*eface)(q)
	if x._type != y._type {
		return false
	}
	// gd fat-iface / Phase D: efaceeq now takes pointers to the full
	// eface headers so it can handle direct / inline / spread storage
	// internally.
	return efaceeq(x._type, p, q)
}

// efaceeq is the callee for compiler-generated empty-interface equality.
// Under gd's fat-iface, x and y are pointers to the full eface headers
// (not just the data slots) so the function can read data + inline and
// dispatch per the concrete type's storage mode:
//   - DirectIface : value == data slot        (pointer compare)
//   - InlineIface : value lives in inline     (feed &inline to t.Equal)
//   - SpreadIface : 3-word value split across data + inline; reassemble
//     into a stack buffer, then feed to t.Equal
//   - Boxed       : data = pointer to heap value (legacy path)
//
// Callers from the runtime (interequal/nilinterequal) must also pass
// pointers to ifaces.
func efaceeq(t *_type, x, y unsafe.Pointer) bool {
	if t == nil {
		return true
	}
	eq := t.Equal
	if eq == nil {
		panic(errorString("comparing uncomparable type " + toRType(t).string()))
	}
	xe := (*eface)(x)
	ye := (*eface)(y)
	if t.IsDirectIface() {
		return xe.data == ye.data
	}
	if t.IsInlineIface() {
		return eq(unsafe.Pointer(&xe.inline), unsafe.Pointer(&ye.inline))
	}
	if t.IsSpreadIface() {
		var xbuf, ybuf [3]uintptr
		xbuf[0] = uintptr(xe.data)
		*(*[16]byte)(unsafe.Pointer(&xbuf[1])) = *(*[16]byte)(unsafe.Pointer(&xe.inline))
		ybuf[0] = uintptr(ye.data)
		*(*[16]byte)(unsafe.Pointer(&ybuf[1])) = *(*[16]byte)(unsafe.Pointer(&ye.inline))
		return eq(noescape(unsafe.Pointer(&xbuf)), noescape(unsafe.Pointer(&ybuf)))
	}
	return eq(xe.data, ye.data)
}

// ifaceeq mirrors efaceeq for non-empty interfaces: x, y point to full
// iface headers (tab + data + inline).
func ifaceeq(tab *itab, x, y unsafe.Pointer) bool {
	if tab == nil {
		return true
	}
	t := tab.Type
	eq := t.Equal
	if eq == nil {
		panic(errorString("comparing uncomparable type " + toRType(t).string()))
	}
	xi := (*iface)(x)
	yi := (*iface)(y)
	if t.IsDirectIface() {
		return xi.data == yi.data
	}
	if t.IsInlineIface() {
		return eq(unsafe.Pointer(&xi.inline), unsafe.Pointer(&yi.inline))
	}
	if t.IsSpreadIface() {
		var xbuf, ybuf [3]uintptr
		xbuf[0] = uintptr(xi.data)
		*(*[16]byte)(unsafe.Pointer(&xbuf[1])) = *(*[16]byte)(unsafe.Pointer(&xi.inline))
		ybuf[0] = uintptr(yi.data)
		*(*[16]byte)(unsafe.Pointer(&ybuf[1])) = *(*[16]byte)(unsafe.Pointer(&yi.inline))
		return eq(noescape(unsafe.Pointer(&xbuf)), noescape(unsafe.Pointer(&ybuf)))
	}
	return eq(xi.data, yi.data)
}

// Testing adapters for hash quality tests (see hash_test.go)
//
// stringHash should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/k14s/starlark-go
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname stringHash
func stringHash(s string, seed uintptr) uintptr {
	return strhash(noescape(unsafe.Pointer(&s)), seed)
}

func bytesHash(b []byte, seed uintptr) uintptr {
	s := (*slice)(unsafe.Pointer(&b))
	return memhash(s.array, seed, uintptr(s.len))
}

func int32Hash(i uint32, seed uintptr) uintptr {
	return memhash32(noescape(unsafe.Pointer(&i)), seed)
}

func int64Hash(i uint64, seed uintptr) uintptr {
	return memhash64(noescape(unsafe.Pointer(&i)), seed)
}

func efaceHash(i any, seed uintptr) uintptr {
	return nilinterhash(noescape(unsafe.Pointer(&i)), seed)
}

func ifaceHash(i interface {
	F()
}, seed uintptr) uintptr {
	return interhash(noescape(unsafe.Pointer(&i)), seed)
}

func readUnaligned32(p unsafe.Pointer) uint32 {
	q := (*[4]byte)(p)
	if goarch.BigEndian {
		return byteorder.BEUint32(q[:])
	}
	return byteorder.LEUint32(q[:])
}

func readUnaligned64(p unsafe.Pointer) uint64 {
	q := (*[8]byte)(p)
	if goarch.BigEndian {
		return byteorder.BEUint64(q[:])
	}
	return byteorder.LEUint64(q[:])
}
