// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"unsafe"
)

// makeslicecopy makes a slice of tolen elements of type et and copies
// fromlen of them from from.
func makeslicecopy(et *_type, tolen int, fromlen int, from unsafe.Pointer) unsafe.Pointer {
	p := newarrayT(et, tolen)
	n := fromlen
	if tolen < n {
		n = tolen
	}
	typedslicecopy(et, p, n, from, n)
	return p
}

// memclrHasPointers zeroes n bytes at ptr, pointers included: a byte
// stored over a pointer clears it.
func memclrHasPointers(ptr unsafe.Pointer, n uintptr) {
	memclrNoHeapPointers(ptr, n)
}

// memclrHasPointersT is memclrHasPointers for the n bytes of elements
// of type t (or of unknown type, if t is nil) at ptr, which the
// compiler calls instead.
func memclrHasPointersT(t *_type, ptr unsafe.Pointer, n uintptr) {
	if t == nil || trylinaddr(ptr) != 0 {
		memclrNoHeapPointers(ptr, n)
		return
	}
	for off := uintptr(0); off < n; off += t.Size_ {
		typedmemclr(t, add(ptr, off))
	}
}

// moveSlice and moveSliceNoCap move a slice from the stack to an
// array of its own.
func moveSlice(et *_type, old unsafe.Pointer, len, cap int) (unsafe.Pointer, int, int) {
	p := newarrayT(et, cap)
	typedslicecopy(et, p, len, old, len)
	return p, len, cap
}

func moveSliceNoCap(et *_type, old unsafe.Pointer, len int) (unsafe.Pointer, int, int) {
	return moveSlice(et, old, len, len)
}

//go:linkname bytealg_MakeNoZero internal/bytealg.MakeNoZero
func bytealg_MakeNoZero(len int) []byte {
	// (The capacity Go's allocator would give.)
	return make([]byte, len, roundupcap(len, 1))
}

// Package runtime/debug.

//go:linkname setGCPercent runtime/debug.setGCPercent
func setGCPercent(in int32) int32 { return 100 }

//go:linkname setMemoryLimit runtime/debug.setMemoryLimit
func setMemoryLimit(in int64) int64 { return 1<<63 - 1 }

//go:linkname setMaxStack runtime/debug.setMaxStack
func setMaxStack(in int) int { return 1 << 20 }

//go:linkname setMaxThreads runtime/debug.setMaxThreads
func setMaxThreads(in int) int { return 1 }

//go:linkname setPanicOnFault runtime/debug.setPanicOnFault
func setPanicOnFault(new bool) bool { return false }

//go:linkname readGCStats runtime/debug.readGCStats
func readGCStats(pauses *[]int64) {}

//go:linkname freeOSMemory runtime/debug.freeOSMemory
func freeOSMemory() {}

//go:linkname debug_modinfo runtime/debug.modinfo
func debug_modinfo() string { return "" }

//go:linkname setTraceback runtime/debug.SetTraceback
func setTraceback(level string) {}

//go:linkname runtime_debug_WriteHeapDump runtime/debug.WriteHeapDump
func runtime_debug_WriteHeapDump(fd uintptr) {}

//go:linkname setCrashFD
func setCrashFD(fd uintptr) uintptr { return ^uintptr(0) }

// Package internal/godebug.

//go:linkname godebug_setUpdate internal/godebug.setUpdate
func godebug_setUpdate(update func(string, string)) {
	godebugUpdate = update
	update(godebugDefault, godebugEnv())
}

// godebugUpdate is internal/godebug's function to call when GODEBUG
// changes; godebugDefault is the program's default, which the linker
// sets.
var (
	godebugUpdate  func(string, string)
	godebugDefault string
)

func godebugEnv() string {
	hostArgs()
	for _, kv := range envs {
		if len(kv) > 8 && kv[:8] == "GODEBUG=" {
			return kv[8:]
		}
	}
	return ""
}

//go:linkname godebug_setNewIncNonDefault internal/godebug.setNewIncNonDefault
func godebug_setNewIncNonDefault(newIncNonDefault func(string) func()) {}

//go:linkname godebug_registerMetric internal/godebug.registerMetric
func godebug_registerMetric(name string, read func() uint64) {}

// A MemProfileRecord describes the live objects allocated by a
// particular call sequence: none are recorded.
type MemProfileRecord struct {
	AllocBytes, FreeBytes     int64
	AllocObjects, FreeObjects int64
	Stack0                    [32]uintptr
}

func (r *MemProfileRecord) InUseBytes() int64   { return r.AllocBytes - r.FreeBytes }
func (r *MemProfileRecord) InUseObjects() int64 { return r.AllocObjects - r.FreeObjects }
func (r *MemProfileRecord) Stack() []uintptr    { return nil }

func MemProfile(p []MemProfileRecord, inuseZero bool) (n int, ok bool) { return 0, true }

// makesliceT and growsliceT are makeslice and growslice for an element
// type known at run time (from a dictionary).
func makesliceT(et *_type, len, cap int) unsafe.Pointer {
	checkmakeslice(int(et.Size_), len, cap)
	return newarrayT(et, cap)
}

func growsliceT(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type) (unsafe.Pointer, int, int) {
	oldLen := newLen - num
	if newLen < 0 {
		panicmakeslicelen()
	}
	newCap := nextslicecap(newLen, oldCap)
	p := newarrayT(et, newCap)
	typedslicecopy(et, p, oldLen, oldPtr, oldLen)
	return p, newLen, newCap
}

// write writes n bytes at p to the file descriptor fd: standard output
// and standard error are the host's.
//
//go:linkname write
func write(fd uintptr, p unsafe.Pointer, n int32) int32 {
	for i := int32(0); i < n; i++ {
		putc(uint32(*(*byte)(add(p, uintptr(i)))))
	}
	return n
}

// concatbytes and concatbyteN return the concatenation of strings as a
// new slice of bytes. The buffer on the caller's stack is not used.
func concatbytes(buf *tmpBuf, a []string) []byte {
	l := 0
	for _, x := range a {
		l += len(x)
	}
	b := make([]byte, 0, l)
	for _, x := range a {
		b = append(b, x...)
	}
	return b
}

func concatbyte2(buf *tmpBuf, a0, a1 string) []byte {
	return concatbytes(buf, []string{a0, a1})
}

func concatbyte3(buf *tmpBuf, a0, a1, a2 string) []byte {
	return concatbytes(buf, []string{a0, a1, a2})
}

func concatbyte4(buf *tmpBuf, a0, a1, a2, a3 string) []byte {
	return concatbytes(buf, []string{a0, a1, a2, a3})
}

func concatbyte5(buf *tmpBuf, a0, a1, a2, a3, a4 string) []byte {
	return concatbytes(buf, []string{a0, a1, a2, a3, a4})
}

// maps_clone returns a copy of the map m, of the same type.
//
//go:linkname maps_clone maps.clone
func maps_clone(m any) any {
	e := efaceOf(&m)
	t := (*abi.MapType)(unsafe.Pointer(e._type))
	src := (*gcmap)(e.data)
	if src == nil {
		return m
	}
	dst := makemap(t, int(src.used), nil)
	var it mapIter
	for mapIterStart(t, src, &it); it.key != nil; mapIterNext(&it) {
		var zero unsafe.Pointer
		if t.Elem.Pointers() {
			zero = newobjectT(t.Elem)
		}
		p := mapassign(t, dst, it.key, zero)
		typedmemmove(t.Elem, p, it.elem)
	}
	var r any
	er := efaceOf(&r)
	er._type = e._type
	er.data = unsafe.Pointer(dst)
	return r
}

//go:linkname fips_fatal crypto/internal/fips140.fatal
func fips_fatal(s string) { throw(s) }

var fipsIndicator uint8

//go:linkname fips_getIndicator crypto/internal/fips140.getIndicator
func fips_getIndicator() uint8 { return fipsIndicator }

//go:linkname fips_setIndicator crypto/internal/fips140.setIndicator
func fips_setIndicator(i uint8) { fipsIndicator = i }

// There are no signals: os/signal's loop blocks forever.

//go:linkname signal_recv os/signal.signal_recv
func signal_recv() uint32 {
	var c chan uint32
	return <-c
}

//go:linkname signal_enable os/signal.signal_enable
func signal_enable(uint32) {}

//go:linkname signal_disable os/signal.signal_disable
func signal_disable(uint32) {}

//go:linkname signal_ignore os/signal.signal_ignore
func signal_ignore(uint32) {}

//go:linkname signal_ignored os/signal.signal_ignored
func signal_ignored(uint32) bool { return false }

//go:linkname signalWaitUntilIdle os/signal.signalWaitUntilIdle
func signalWaitUntilIdle() {}

//go:linkname fips140_isBypassed crypto/fips140.isBypassed
func fips140_isBypassed() bool { return false }

//go:linkname rand_fatal crypto/rand.fatal
func rand_fatal(s string) { fatal(s) }

//go:linkname sysrand_fatal crypto/internal/sysrand.fatal
func sysrand_fatal(s string) { fatal(s) }

//go:linkname fips140_setBypass crypto/fips140.setBypass
func fips140_setBypass() {}

//go:linkname fips140_unsetBypass crypto/fips140.unsetBypass
func fips140_unsetBypass() {}
