// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// The functions of sync/atomic. One goroutine runs at a time and none
// is preempted, so these are plain loads and stores.

//go:linkname sync_atomic_AddInt32 sync/atomic.AddInt32
func sync_atomic_AddInt32(addr *int32, delta int32) int32 { *addr += delta; return *addr }

//go:linkname sync_atomic_AndInt32 sync/atomic.AndInt32
func sync_atomic_AndInt32(addr *int32, mask int32) int32 {
	old := *addr
	*addr = old & mask
	return old
}

//go:linkname sync_atomic_OrInt32 sync/atomic.OrInt32
func sync_atomic_OrInt32(addr *int32, mask int32) int32 { old := *addr; *addr = old | mask; return old }

//go:linkname sync_atomic_CompareAndSwapInt32 sync/atomic.CompareAndSwapInt32
func sync_atomic_CompareAndSwapInt32(addr *int32, old, new int32) bool {
	if *addr != old {
		return false
	}
	*addr = new
	return true
}

//go:linkname sync_atomic_LoadInt32 sync/atomic.LoadInt32
func sync_atomic_LoadInt32(addr *int32) int32 { return *addr }

//go:linkname sync_atomic_StoreInt32 sync/atomic.StoreInt32
func sync_atomic_StoreInt32(addr *int32, val int32) { *addr = val }

//go:linkname sync_atomic_SwapInt32 sync/atomic.SwapInt32
func sync_atomic_SwapInt32(addr *int32, new int32) int32 { old := *addr; *addr = new; return old }

//go:linkname sync_atomic_AddInt64 sync/atomic.AddInt64
func sync_atomic_AddInt64(addr *int64, delta int64) int64 { *addr += delta; return *addr }

//go:linkname sync_atomic_AndInt64 sync/atomic.AndInt64
func sync_atomic_AndInt64(addr *int64, mask int64) int64 {
	old := *addr
	*addr = old & mask
	return old
}

//go:linkname sync_atomic_OrInt64 sync/atomic.OrInt64
func sync_atomic_OrInt64(addr *int64, mask int64) int64 { old := *addr; *addr = old | mask; return old }

//go:linkname sync_atomic_CompareAndSwapInt64 sync/atomic.CompareAndSwapInt64
func sync_atomic_CompareAndSwapInt64(addr *int64, old, new int64) bool {
	if *addr != old {
		return false
	}
	*addr = new
	return true
}

//go:linkname sync_atomic_LoadInt64 sync/atomic.LoadInt64
func sync_atomic_LoadInt64(addr *int64) int64 { return *addr }

//go:linkname sync_atomic_StoreInt64 sync/atomic.StoreInt64
func sync_atomic_StoreInt64(addr *int64, val int64) { *addr = val }

//go:linkname sync_atomic_SwapInt64 sync/atomic.SwapInt64
func sync_atomic_SwapInt64(addr *int64, new int64) int64 { old := *addr; *addr = new; return old }

//go:linkname sync_atomic_AddUint32 sync/atomic.AddUint32
func sync_atomic_AddUint32(addr *uint32, delta uint32) uint32 { *addr += delta; return *addr }

//go:linkname sync_atomic_AndUint32 sync/atomic.AndUint32
func sync_atomic_AndUint32(addr *uint32, mask uint32) uint32 {
	old := *addr
	*addr = old & mask
	return old
}

//go:linkname sync_atomic_OrUint32 sync/atomic.OrUint32
func sync_atomic_OrUint32(addr *uint32, mask uint32) uint32 {
	old := *addr
	*addr = old | mask
	return old
}

//go:linkname sync_atomic_CompareAndSwapUint32 sync/atomic.CompareAndSwapUint32
func sync_atomic_CompareAndSwapUint32(addr *uint32, old, new uint32) bool {
	if *addr != old {
		return false
	}
	*addr = new
	return true
}

//go:linkname sync_atomic_LoadUint32 sync/atomic.LoadUint32
func sync_atomic_LoadUint32(addr *uint32) uint32 { return *addr }

//go:linkname sync_atomic_StoreUint32 sync/atomic.StoreUint32
func sync_atomic_StoreUint32(addr *uint32, val uint32) { *addr = val }

//go:linkname sync_atomic_SwapUint32 sync/atomic.SwapUint32
func sync_atomic_SwapUint32(addr *uint32, new uint32) uint32 { old := *addr; *addr = new; return old }

//go:linkname sync_atomic_AddUint64 sync/atomic.AddUint64
func sync_atomic_AddUint64(addr *uint64, delta uint64) uint64 { *addr += delta; return *addr }

//go:linkname sync_atomic_AndUint64 sync/atomic.AndUint64
func sync_atomic_AndUint64(addr *uint64, mask uint64) uint64 {
	old := *addr
	*addr = old & mask
	return old
}

//go:linkname sync_atomic_OrUint64 sync/atomic.OrUint64
func sync_atomic_OrUint64(addr *uint64, mask uint64) uint64 {
	old := *addr
	*addr = old | mask
	return old
}

//go:linkname sync_atomic_CompareAndSwapUint64 sync/atomic.CompareAndSwapUint64
func sync_atomic_CompareAndSwapUint64(addr *uint64, old, new uint64) bool {
	if *addr != old {
		return false
	}
	*addr = new
	return true
}

//go:linkname sync_atomic_LoadUint64 sync/atomic.LoadUint64
func sync_atomic_LoadUint64(addr *uint64) uint64 { return *addr }

//go:linkname sync_atomic_StoreUint64 sync/atomic.StoreUint64
func sync_atomic_StoreUint64(addr *uint64, val uint64) { *addr = val }

//go:linkname sync_atomic_SwapUint64 sync/atomic.SwapUint64
func sync_atomic_SwapUint64(addr *uint64, new uint64) uint64 { old := *addr; *addr = new; return old }

//go:linkname sync_atomic_AddUintptr sync/atomic.AddUintptr
func sync_atomic_AddUintptr(addr *uintptr, delta uintptr) uintptr { *addr += delta; return *addr }

//go:linkname sync_atomic_AndUintptr sync/atomic.AndUintptr
func sync_atomic_AndUintptr(addr *uintptr, mask uintptr) uintptr {
	old := *addr
	*addr = old & mask
	return old
}

//go:linkname sync_atomic_OrUintptr sync/atomic.OrUintptr
func sync_atomic_OrUintptr(addr *uintptr, mask uintptr) uintptr {
	old := *addr
	*addr = old | mask
	return old
}

//go:linkname sync_atomic_CompareAndSwapUintptr sync/atomic.CompareAndSwapUintptr
func sync_atomic_CompareAndSwapUintptr(addr *uintptr, old, new uintptr) bool {
	if *addr != old {
		return false
	}
	*addr = new
	return true
}

//go:linkname sync_atomic_LoadUintptr sync/atomic.LoadUintptr
func sync_atomic_LoadUintptr(addr *uintptr) uintptr { return *addr }

//go:linkname sync_atomic_StoreUintptr sync/atomic.StoreUintptr
func sync_atomic_StoreUintptr(addr *uintptr, val uintptr) { *addr = val }

//go:linkname sync_atomic_SwapUintptr sync/atomic.SwapUintptr
func sync_atomic_SwapUintptr(addr *uintptr, new uintptr) uintptr {
	old := *addr
	*addr = new
	return old
}

//go:linkname sync_atomic_CompareAndSwapPointer sync/atomic.CompareAndSwapPointer
func sync_atomic_CompareAndSwapPointer(addr *unsafe.Pointer, old, new unsafe.Pointer) bool {
	if *addr != old {
		return false
	}
	*addr = new
	return true
}

//go:linkname sync_atomic_LoadPointer sync/atomic.LoadPointer
func sync_atomic_LoadPointer(addr *unsafe.Pointer) unsafe.Pointer { return *addr }

//go:linkname sync_atomic_StorePointer sync/atomic.StorePointer
func sync_atomic_StorePointer(addr *unsafe.Pointer, val unsafe.Pointer) { *addr = val }

//go:linkname sync_atomic_SwapPointer sync/atomic.SwapPointer
func sync_atomic_SwapPointer(addr *unsafe.Pointer, new unsafe.Pointer) unsafe.Pointer {
	old := *addr
	*addr = new
	return old
}

//go:linkname sync_atomic_runtime_procPin sync/atomic.runtime_procPin
func sync_atomic_runtime_procPin() int { return 0 }

//go:linkname sync_atomic_runtime_procUnpin sync/atomic.runtime_procUnpin
func sync_atomic_runtime_procUnpin() {}
