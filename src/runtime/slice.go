// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/goexperiment"
	"internal/runtime/math"
	"internal/runtime/sys"
	"unsafe"
)

type slice struct {
	array unsafe.Pointer
	len   int
	cap   int
}

// A notInHeapSlice is a slice backed by internal/runtime/sys.NotInHeap
// memory: a store of one has no write barrier.
type notInHeapSlice []notInHeap

// mkNotInHeapSlice returns the notInHeapSlice of array p, length len and
// capacity cap.
//
//go:nosplit
func mkNotInHeapSlice(p *notInHeap, len, cap int) notInHeapSlice {
	// (Not through a pointer to an array, which would be checked for
	// nil: p may be memory that is only reserved.)
	s := unsafe.Slice((*byte)(unsafe.Pointer(p)), cap)[:len]
	return *(*notInHeapSlice)(unsafe.Pointer(&s))
}

// noWBSlice returns the slice at p as a notInHeapSlice, whose stores have
// no write barrier, for code that may have none: where a slice's array
// stays (s = s[:n]: with 8-byte words its length is in a word with its
// capacity, and the slice is stored whole) or is not in the heap.
//
//go:nosplit
func noWBSlice(p unsafe.Pointer) *notInHeapSlice {
	return (*notInHeapSlice)(p)
}

// slicehdr is called by compiled code to store a huge slice, or to pass
// it in registers (doc/gd/slice16.md): with 8-byte words a slice whose
// capacity does not fit in its 32-bit field is the address of a header,
// a new one for each store, which no one writes after, and the marker.
// It returns the address of a new header of the slice p, len, cap.
func slicehdr(p unsafe.Pointer, len, cap int) unsafe.Pointer {
	if gp := getg(); gp.m.mallocing != 0 || gp == gp.m.g0 || gp == gp.m.gsignal {
		// (The runtime's own slices are never huge: the compiler's
		// check of the functions with no write barrier does not
		// follow the calls here.)
		throw("huge slice where the runtime may not allocate")
	}
	h := new(slice)
	*h = slice{p, len, cap}
	// The header is written before its address is stored.
	publicationBarrier()
	return unsafe.Pointer(h)
}

// slicehdrnotinheap is slicehdr for a slice of a type not in the heap,
// whose word 0 is stored without a write barrier: there is no huge one.
func slicehdrnotinheap(p unsafe.Pointer, len, cap int) unsafe.Pointer {
	throw("slice of a type not in the heap too large")
	return nil
}

func panicmakeslicelen() {
	panic(errorString("makeslice: len out of range"))
}

func panicmakeslicecap() {
	panic(errorString("makeslice: cap out of range"))
}

// makeslicecopy allocates a slice of "tolen" elements of type "et",
// then copies "fromlen" elements of type "et" into that new allocation from "from".
func makeslicecopy(et *_type, tolen int, fromlen int, from unsafe.Pointer) unsafe.Pointer {
	et, local := allocType(et)
	var tomem, copymem uintptr
	if uintptr(tolen) > uintptr(fromlen) {
		var overflow bool
		tomem, overflow = math.MulUintptr(et.Size_, uintptr(tolen))
		if overflow || tomem > maxAlloc || tolen < 0 {
			panicmakeslicelen()
		}
		copymem = et.Size_ * uintptr(fromlen)
	} else {
		// fromlen is a known good length providing and equal or greater than tolen,
		// thereby making tolen a good slice length too as from and to slices have the
		// same element width.
		tomem = et.Size_ * uintptr(tolen)
		copymem = tomem
	}

	var to unsafe.Pointer
	if !et.Pointers() {
		to = mallocgcl(tomem, nil, false, local)
		if copymem < tomem {
			memclrNoHeapPointers(add(to, copymem), tomem-copymem)
		}
	} else {
		// Note: can't use rawmem (which avoids zeroing of memory), because then GC can scan uninitialized memory.
		to = mallocgcl(tomem, et, true, local)
		if copymem > 0 && writeBarrier.enabled {
			// Only shade the pointers in old.array since we know the destination slice to
			// only contains nil pointers because it has been cleared during alloc.
			//
			// It's safe to pass a type to this function as an optimization because
			// from and to only ever refer to memory representing whole values of
			// type et. See the comment on bulkBarrierPreWrite.
			bulkBarrierPreWriteSrcOnly(uintptr(to), uintptr(from), copymem, et)
		}
	}

	if raceenabled {
		callerpc := sys.GetCallerPC()
		pc := abi.FuncPCABIInternal(makeslicecopy)
		racereadrangepc(from, copymem, callerpc, pc)
	}
	if msanenabled {
		msanread(from, copymem)
	}
	if asanenabled {
		asanread(from, copymem)
	}

	if tearEnabled && copymem > 0 && et.Pointers() && (holdsIface(et) && !onCurStack(from) || isShared(from) && tearable(et)) {
		// Another goroutine may be storing an element.
		shareCopyFrom(et, to, from, copymem/et.Size_)
		if !storesOrdered {
			// The copy is seen before the slice is.
			publicationBarrier()
		}
		return to
	}
	memmove(to, from, copymem)
	if !storesOrdered && (copymem > 0 || !et.Pointers()) {
		// (Without pointers the array was not cleared by mallocgc,
		// whose barrier would have covered that.)
		publicationBarrier()
	}

	return to
}

// makeslice should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/bytedance/sonic
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname makeslice
func makeslice(et *_type, len, cap int) unsafe.Pointer {
	et, local := allocType(et)
	mem, overflow := math.MulUintptr(et.Size_, uintptr(cap))
	if overflow || mem > maxAlloc || len < 0 || len > cap {
		// NOTE: Produce a 'len out of range' error instead of a
		// 'cap out of range' error when someone does make([]T, bignumber).
		// 'cap out of range' is true too, but since the cap is only being
		// supplied implicitly, saying len is clearer.
		// See golang.org/issue/4085.
		mem, overflow := math.MulUintptr(et.Size_, uintptr(len))
		if overflow || mem > maxAlloc || len < 0 {
			panicmakeslicelen()
		}
		panicmakeslicecap()
	}

	return mallocgcl(mem, et, true, local)
}

func makeslice64(et *_type, len64, cap64 int64) unsafe.Pointer {
	len := int(len64)
	if int64(len) != len64 {
		panicmakeslicelen()
	}

	cap := int(cap64)
	if int64(cap) != cap64 {
		panicmakeslicecap()
	}

	return makeslice(et, len, cap)
}

// growslice allocates new backing store for a slice.
//
// arguments:
//
//	oldPtr = pointer to the slice's backing array
//	newLen = new length (= oldLen + num)
//	oldCap = original slice's capacity.
//	   num = number of elements being added
//	    et = element type
//
// return values:
//
//	newPtr = pointer to the new backing store
//	newLen = same value as the argument
//	newCap = capacity of the new backing store
//
// Requires that uint(newLen) > uint(oldCap).
// Assumes the original slice length is newLen - num
//
// A new backing store is allocated with space for at least newLen elements.
// Existing entries [0, oldLen) are copied over to the new backing store.
// Added entries [oldLen, newLen) are not initialized by growslice
// (although for pointer-containing element types, and where the new slice
// could be seen before the caller stores them, they are zeroed). They
// must be initialized by the caller.
// Trailing entries [newLen, newCap) are zeroed.
//
// growslice's odd calling convention makes the generated code that calls
// this function simpler. In particular, it accepts and returns the
// new length so that the old length is not live (does not need to be
// spilled/restored) and the new length is returned (also does not need
// to be spilled/restored).
//
// growslice should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/bytedance/sonic
//   - github.com/chenzhuoyu/iasm
//   - github.com/cloudwego/dynamicgo
//   - github.com/ugorji/go/codec
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname growslice
func growslice(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type) slice {
	et, local := allocType(et)
	return growslicel(oldPtr, newLen, oldCap, num, et, local)
}

// growslicel is growslice for a type that is not tagged. The new array
// is allocated as local if local is set (allocType).
func growslicel(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type, local bool) slice {
	oldLen := newLen - num
	if raceenabled {
		callerpc := sys.GetCallerPC()
		racereadrangepc(oldPtr, uintptr(oldLen*int(et.Size_)), callerpc, abi.FuncPCABIInternal(growslice))
	}
	if msanenabled {
		msanread(oldPtr, uintptr(oldLen*int(et.Size_)))
	}
	if asanenabled {
		asanread(oldPtr, uintptr(oldLen*int(et.Size_)))
	}

	if newLen < 0 {
		panic(errorString("growslice: len out of range"))
	}

	if et.Size_ == 0 {
		// append should not create a slice with nil pointer but non-zero len.
		// We assume that append doesn't need to preserve oldPtr in this case.
		return slice{unsafe.Pointer(&zerobase), newLen, newLen}
	}

	newcap := nextslicecap(newLen, oldCap)

	var overflow bool
	var lenmem, newlenmem, capmem uintptr
	// Specialize for common values of et.Size.
	// For 1 we don't need any division/multiplication.
	// For goarch.PtrSize, compiler will optimize division/multiplication into a shift by a constant.
	// For powers of 2, use a variable shift.
	noscan := !et.Pointers()
	switch {
	case et.Size_ == 1:
		lenmem = uintptr(oldLen)
		newlenmem = uintptr(newLen)
		capmem = roundupsize(uintptr(newcap), noscan)
		overflow = uintptr(newcap) > maxAlloc
		newcap = int(capmem)
	case et.Size_ == goarch.PtrSize:
		lenmem = uintptr(oldLen) * goarch.PtrSize
		newlenmem = uintptr(newLen) * goarch.PtrSize
		capmem = roundupsize(uintptr(newcap)*goarch.PtrSize, noscan)
		overflow = uintptr(newcap) > maxAlloc/goarch.PtrSize
		newcap = int(capmem / goarch.PtrSize)
	case isPowerOfTwo(et.Size_):
		var shift uintptr
		if goarch.PtrSize == 8 {
			// Mask shift for better code generation.
			shift = uintptr(sys.TrailingZeros64(uint64(et.Size_))) & 63
		} else {
			shift = uintptr(sys.TrailingZeros32(uint32(et.Size_))) & 31
		}
		lenmem = uintptr(oldLen) << shift
		newlenmem = uintptr(newLen) << shift
		capmem = roundupsize(uintptr(newcap)<<shift, noscan)
		overflow = uintptr(newcap) > (maxAlloc >> shift)
		newcap = int(capmem >> shift)
		capmem = uintptr(newcap) << shift
	default:
		lenmem = uintptr(oldLen) * et.Size_
		newlenmem = uintptr(newLen) * et.Size_
		capmem, overflow = math.MulUintptr(et.Size_, uintptr(newcap))
		capmem = roundupsize(capmem, noscan)
		newcap = int(capmem / et.Size_)
		capmem = uintptr(newcap) * et.Size_
	}

	// The check of overflow in addition to capmem > maxAlloc is needed
	// to prevent an overflow which can be used to trigger a segfault
	// on 32bit architectures with this example program:
	//
	// type T [1<<27 + 1]int64
	//
	// var d T
	// var s []T
	//
	// func main() {
	//   s = append(s, d, d, d, d)
	//   print(len(s), "\n")
	// }
	if overflow || capmem > maxAlloc {
		panic(errorString("growslice: len out of range"))
	}

	var p unsafe.Pointer
	if !et.Pointers() {
		p = mallocgcl(capmem, nil, false, local)
		// The append() that calls growslice is going to overwrite from oldLen to newLen.
		// Only clear the part that will not be overwritten.
		// The reflect_growslice() that calls growslice will manually clear
		// the region not cleared here.
		//
		// Where the slice may be seen before those elements are, clear
		// them too, or a racing reader would get what a freed object
		// left there: where stores are not in order, and where append
		// stores the slice's words before the elements (a slice that
		// is not packed; a packed one is stored after them).
		from := newlenmem
		if !storesOrdered || !abi.SlicePacked {
			from = lenmem
		}
		memclrNoHeapPointers(add(p, from), capmem-from)
	} else {
		// Note: can't use rawmem (which avoids zeroing of memory), because then GC can scan uninitialized memory.
		p = mallocgcl(capmem, et, true, local)
		if lenmem > 0 && writeBarrier.enabled {
			// Only shade the pointers in oldPtr since we know the destination slice p
			// only contains nil pointers because it has been cleared during alloc.
			//
			// It's safe to pass a type to this function as an optimization because
			// from and to only ever refer to memory representing whole values of
			// type et. See the comment on bulkBarrierPreWrite.
			bulkBarrierPreWriteSrcOnly(uintptr(p), uintptr(oldPtr), lenmem-et.Size_+et.PtrBytes, et)
		}
	}
	if tearEnabled && lenmem > 0 && (holdsIface(et) && !onCurStack(oldPtr) || isShared(oldPtr) && tearable(et)) {
		// Another goroutine may be storing an element.
		shareCopyFrom(et, p, oldPtr, uintptr(oldLen))
	} else {
		memmove(p, oldPtr, lenmem)
	}
	if !storesOrdered && (lenmem > 0 || !et.Pointers()) {
		// The copy, from the stack or whole from elsewhere, and what
		// was cleared above, are seen before the new array is.
		publicationBarrier()
	}

	return slice{p, newLen, newcap}
}

// growsliceNoAlias is like growslice but only for the case where
// we know that oldPtr is not aliased.
//
// In other words, the caller must know that there are no other references
// to the backing memory of the slice being grown aside from the slice header
// that will be updated with new backing memory when growsliceNoAlias
// returns, and therefore oldPtr must be the only pointer to its referent
// aside from the slice header updated by the returned slice.
//
// In addition, oldPtr must point to the start of the allocation and match
// the pointer that was returned by mallocgc. In particular, oldPtr must not
// be an interior pointer, such as after a reslice.
//
// See freegc for details.
func growsliceNoAlias(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type) slice {
	et, local := allocType(et)
	s := growslicel(oldPtr, newLen, oldCap, num, et, local)
	if goexperiment.RuntimeFreegc && oldPtr != nil && oldPtr != s.array {
		if gp := getg(); uintptr(oldPtr) < gp.stack.lo || gp.stack.hi <= uintptr(oldPtr) {
			// oldPtr does not point into the current stack, and it is not
			// the data pointer for s after the grow, so attempt to free it.
			// (Note that freegc also verifies that oldPtr does not point into our stack,
			// but checking here first is slightly cheaper for the case when
			// oldPtr is on the stack and freegc would be a no-op.)
			//
			// TODO(thepudds): it may be that oldPtr==s.array only when elemsize==0,
			// so perhaps we could prohibit growsliceNoAlias being called in that case
			// and eliminate that check here, or alternatively, we could lean into
			// freegc being a no-op for zero-sized allocations (that is, no check of
			// oldPtr != s.array here and just let freegc return quickly).
			noscan := !et.Pointers()
			freegc(oldPtr, uintptr(oldCap)*et.Size_, noscan)
		}
	}
	return s
}

// nextslicecap computes the next appropriate slice length.
func nextslicecap(newLen, oldCap int) int {
	newcap := oldCap
	doublecap := newcap + newcap
	if newLen > doublecap {
		return newLen
	}

	const threshold = 256
	if oldCap < threshold {
		return doublecap
	}
	for {
		// Transition from growing 2x for small slices
		// to growing 1.25x for large slices. This formula
		// gives a smooth-ish transition between the two.
		newcap += (newcap + 3*threshold) >> 2

		// We need to check `newcap >= newLen` and whether `newcap` overflowed.
		// newLen is guaranteed to be larger than zero, hence
		// when newcap overflows then `uint(newcap) > uint(newLen)`.
		// This allows to check for both with the same comparison.
		if uint(newcap) >= uint(newLen) {
			break
		}
	}

	// Set newcap to the requested cap when
	// the newcap calculation overflowed.
	if newcap <= 0 {
		return newLen
	}
	return newcap
}

// reflect_growslice should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/cloudwego/dynamicgo
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname reflect_growslice reflect.growslice
func reflect_growslice(et *_type, old slice, num int) slice {
	// Semantically equivalent to slices.Grow, except that the caller
	// is responsible for ensuring that old.len+num > old.cap.
	num -= old.cap - old.len // preserve memory of old[old.len:old.cap]
	new := growslice(old.array, old.cap+num, old.cap, num, et)
	// growslice does not zero out new[old.cap:new.len] since it assumes that
	// the memory will be overwritten by an append() that called growslice.
	// Since the caller of reflect_growslice is not append(),
	// zero out this region before returning the slice to the reflect package.
	if !et.Pointers() {
		oldcapmem := uintptr(old.cap) * et.Size_
		newlenmem := uintptr(new.len) * et.Size_
		memclrNoHeapPointers(add(new.array, oldcapmem), newlenmem-oldcapmem)
		if !storesOrdered {
			publicationBarrier()
		}
	}
	new.len = old.len // preserve the old length
	return new
}

func isPowerOfTwo(x uintptr) bool {
	return x&(x-1) == 0
}

// slicecopy is used to copy from a string or slice of pointerless elements into a slice.
func slicecopy(toPtr unsafe.Pointer, toLen int, fromPtr unsafe.Pointer, fromLen int, width uintptr) int {
	if fromLen == 0 || toLen == 0 {
		return 0
	}

	n := fromLen
	if toLen < n {
		n = toLen
	}

	if width == 0 {
		return n
	}

	size := uintptr(n) * width
	if raceenabled {
		callerpc := sys.GetCallerPC()
		pc := abi.FuncPCABIInternal(slicecopy)
		racereadrangepc(fromPtr, size, callerpc, pc)
		racewriterangepc(toPtr, size, callerpc, pc)
	}
	if msanenabled {
		msanread(fromPtr, size)
		msanwrite(toPtr, size)
	}
	if asanenabled {
		asanread(fromPtr, size)
		asanwrite(toPtr, size)
	}

	if size == 1 { // common case worth about 2x to do here
		// TODO: is this still worth it with new memmove impl?
		*(*byte)(toPtr) = *(*byte)(fromPtr) // known to be a byte pointer
	} else {
		memmove(toPtr, fromPtr, size)
	}
	return n
}

//go:linkname bytealg_MakeNoZero internal/bytealg.MakeNoZero
func bytealg_MakeNoZero(len int) []byte {
	if uintptr(len) > maxAlloc {
		panicmakeslicelen()
	}
	cap := roundupsize(uintptr(len), true)
	// The caller fills it after this returns, so no barrier here can
	// order that fill before the slice is seen. Where stores are not in
	// order the memory is cleared (with mallocgc's barrier after it): a
	// racing reader then sees zeros, not what a freed object left. Where
	// they are, callers store the slice after they fill what it shows,
	// and do not expose its capacity.
	return unsafe.Slice((*byte)(mallocgc(cap, nil, !storesOrdered)), cap)[:len]
}

// moveSlice copies the input slice to the heap and returns it.
// et is the element type of the slice.
func moveSlice(et *_type, old unsafe.Pointer, len, cap int) (unsafe.Pointer, int, int) {
	if cap == 0 {
		if old != nil {
			old = unsafe.Pointer(&zerobase)
		}
		return old, 0, 0
	}
	capmem := uintptr(cap) * et.Size_
	new := mallocgc(capmem, et, true)
	bulkBarrierPreWriteSrcOnly(uintptr(new), uintptr(old), capmem, et)
	memmove(new, old, capmem)
	if !storesOrdered {
		// The copy of the stack's elements is seen before the slice is.
		publicationBarrier()
	}
	return new, len, cap
}

// moveSliceNoScan is like moveSlice except the element type is known to
// not have any pointers. We instead pass in the size of the element.
func moveSliceNoScan(elemSize uintptr, old unsafe.Pointer, len, cap int) (unsafe.Pointer, int, int) {
	if cap == 0 {
		if old != nil {
			old = unsafe.Pointer(&zerobase)
		}
		return old, 0, 0
	}
	capmem := uintptr(cap) * elemSize
	new := mallocgc(capmem, nil, false)
	memmove(new, old, capmem)
	if !storesOrdered {
		publicationBarrier()
	}
	return new, len, cap
}

// moveSliceNoCap is like moveSlice, but can pick any appropriate capacity
// for the returned slice.
// Elements between len and cap in the returned slice will be zeroed.
func moveSliceNoCap(et *_type, old unsafe.Pointer, len int) (unsafe.Pointer, int, int) {
	if len == 0 {
		if old != nil {
			old = unsafe.Pointer(&zerobase)
		}
		return old, 0, 0
	}
	lenmem := uintptr(len) * et.Size_
	capmem := roundupsize(lenmem, false)
	cap := capmem / et.Size_
	new := mallocgc(cap*et.Size_, et, true)
	bulkBarrierPreWriteSrcOnly(uintptr(new), uintptr(old), lenmem, et)
	memmove(new, old, lenmem)
	if !storesOrdered {
		// (As in moveSlice.)
		publicationBarrier()
	}
	return new, len, int(cap)
}

// moveSliceNoCapNoScan is a combination of moveSliceNoScan and moveSliceNoCap.
func moveSliceNoCapNoScan(elemSize uintptr, old unsafe.Pointer, len int) (unsafe.Pointer, int, int) {
	if len == 0 {
		if old != nil {
			old = unsafe.Pointer(&zerobase)
		}
		return old, 0, 0
	}
	lenmem := uintptr(len) * elemSize
	capmem := roundupsize(lenmem, true)
	new := mallocgc(capmem, nil, false)
	memmove(new, old, lenmem)
	if capmem > lenmem {
		memclrNoHeapPointers(add(new, lenmem), capmem-lenmem)
	}
	if !storesOrdered {
		publicationBarrier()
	}
	return new, len, int(capmem / elemSize)
}

// growsliceBuf is like growslice, but we can use the given buffer
// as a backing store if we want. bufPtr must be on the stack.
func growsliceBuf(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type, bufPtr unsafe.Pointer, bufLen int) slice {
	et, local := allocType(et)
	if newLen > bufLen {
		// Doesn't fit, process like a normal growslice.
		return growslicel(oldPtr, newLen, oldCap, num, et, local)
	}
	oldLen := newLen - num
	if oldPtr != bufPtr && oldLen != 0 {
		// Move data to start of buffer.
		// Note: bufPtr is on the stack, so no write barrier needed.
		if holdsIface(et) && !onCurStack(oldPtr) {
			// Another goroutine may be storing an element.
			shareCopyFrom(et, bufPtr, oldPtr, uintptr(oldLen))
		} else {
			memmove(bufPtr, oldPtr, uintptr(oldLen)*et.Size_)
		}
	}
	// Pick a new capacity.
	//
	// Unlike growslice, we don't need to double the size each time.
	// The work done here is not proportional to the length of the slice.
	// (Unless the memmove happens above, but that is rare, and in any
	// case there are not many elements on this path.)
	//
	// Instead, we try to just bump up to the next size class.
	// This will ensure that we don't waste any space when we eventually
	// call moveSlice with the resulting slice.
	newCap := int(roundupsize(uintptr(newLen)*et.Size_, !et.Pointers()) / et.Size_)

	// Zero slice beyond newLen.
	// The buffer is stack memory, so NoHeapPointers is ok.
	// Caller will overwrite [oldLen:newLen], so we don't need to zero that portion.
	// If et.Pointers(), buffer is at least initialized so we don't need to
	// worry about the caller overwriting junk in [oldLen:newLen].
	if newLen < newCap {
		memclrNoHeapPointers(add(bufPtr, uintptr(newLen)*et.Size_), uintptr(newCap-newLen)*et.Size_)
	}

	return slice{bufPtr, newLen, newCap}
}

// growsliceBufNoAlias is a combination of growsliceBuf and growsliceNoAlias.
// bufPtr must be on the stack.
func growsliceBufNoAlias(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type, bufPtr unsafe.Pointer, bufLen int) slice {
	s := growsliceBuf(oldPtr, newLen, oldCap, num, et, bufPtr, bufLen)
	et, _ = allocType(et)
	if goexperiment.RuntimeFreegc && oldPtr != bufPtr && oldPtr != nil && oldPtr != s.array {
		// oldPtr is not bufPtr (the stack buffer) and it is not
		// the data pointer for s after the grow, so attempt to free it.
		// (Note that freegc does a broader check that oldPtr does not point into our stack,
		// but checking here first is slightly cheaper for a common case when oldPtr is bufPtr
		// and freegc would be a no-op.)
		//
		// TODO(thepudds): see related TODO in growsliceNoAlias about possibly eliminating
		// the oldPtr != s.array check.
		noscan := !et.Pointers()
		freegc(oldPtr, uintptr(oldCap)*et.Size_, noscan)
	}
	return s
}
