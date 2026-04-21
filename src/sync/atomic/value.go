// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package atomic

import (
	"internal/abi"
	"unsafe"
)

// A Value provides an atomic load and store of a consistently typed value.
// The zero value for a Value returns nil from [Value.Load].
// Once [Value.Store] has been called, a Value must not be copied.
//
// A Value must not be copied after first use.
type Value struct {
	v any
	// gd fat-iface: lock serializes all operations on v. The stock 16-byte
	// iface let Store publish (Type,Data) with a single atomic pointer
	// write; the 32-byte fat header spans Type+Data+Inline[2] and no single
	// instruction covers it, so readers and writers both take this spinlock
	// to see a consistent snapshot. Unlocked=0, locked=1.
	lock uint32
}

// acquire takes the spinlock. procPin keeps the lock holder from being
// descheduled while other goroutines busy-wait on the lock.
func (v *Value) acquire() {
	runtime_procPin()
	for !CompareAndSwapUint32(&v.lock, 0, 1) {
		// spin
	}
}

func (v *Value) release() {
	StoreUint32(&v.lock, 0)
	runtime_procUnpin()
}

// Load returns the value set by the most recent Store.
// It returns nil if there has been no call to Store for this Value.
func (v *Value) Load() (val any) {
	vp := (*abi.EmptyInterface)(unsafe.Pointer(v))
	v.acquire()
	if vp.Type == nil {
		v.release()
		return nil
	}
	vlp := (*abi.EmptyInterface)(unsafe.Pointer(&val))
	*vlp = *vp
	v.release()
	return
}

// Store sets the value of the [Value] v to val.
// All calls to Store for a given Value must use values of the same concrete type.
// Store of an inconsistent type panics, as does Store(nil).
func (v *Value) Store(val any) {
	if val == nil {
		panic("sync/atomic: store of nil value into Value")
	}
	vp := (*abi.EmptyInterface)(unsafe.Pointer(v))
	vlp := (*abi.EmptyInterface)(unsafe.Pointer(&val))
	v.acquire()
	if vp.Type != nil && vp.Type != vlp.Type {
		v.release()
		panic("sync/atomic: store of inconsistently typed value into Value")
	}
	*vp = *vlp
	v.release()
}

// Swap stores new into Value and returns the previous value. It returns nil if
// the Value is empty.
//
// All calls to Swap for a given Value must use values of the same concrete
// type. Swap of an inconsistent type panics, as does Swap(nil).
func (v *Value) Swap(new any) (old any) {
	if new == nil {
		panic("sync/atomic: swap of nil value into Value")
	}
	vp := (*abi.EmptyInterface)(unsafe.Pointer(v))
	np := (*abi.EmptyInterface)(unsafe.Pointer(&new))
	v.acquire()
	if vp.Type != nil && vp.Type != np.Type {
		v.release()
		panic("sync/atomic: swap of inconsistently typed value into Value")
	}
	if vp.Type != nil {
		op := (*abi.EmptyInterface)(unsafe.Pointer(&old))
		*op = *vp
	}
	*vp = *np
	v.release()
	return old
}

// CompareAndSwap executes the compare-and-swap operation for the [Value].
//
// All calls to CompareAndSwap for a given Value must use values of the same
// concrete type. CompareAndSwap of an inconsistent type panics, as does
// CompareAndSwap(old, nil).
func (v *Value) CompareAndSwap(old, new any) (swapped bool) {
	if new == nil {
		panic("sync/atomic: compare and swap of nil value into Value")
	}
	vp := (*abi.EmptyInterface)(unsafe.Pointer(v))
	np := (*abi.EmptyInterface)(unsafe.Pointer(&new))
	op := (*abi.EmptyInterface)(unsafe.Pointer(&old))
	if op.Type != nil && np.Type != op.Type {
		panic("sync/atomic: compare and swap of inconsistently typed values")
	}
	v.acquire()
	if vp.Type == nil {
		if old != nil {
			v.release()
			return false
		}
		*vp = *np
		v.release()
		return true
	}
	if vp.Type != np.Type {
		v.release()
		panic("sync/atomic: compare and swap of inconsistently typed value into Value")
	}
	// Compare the full fat iface against old via runtime equality.
	var cur any
	cp := (*abi.EmptyInterface)(unsafe.Pointer(&cur))
	*cp = *vp
	if cur != old {
		v.release()
		return false
	}
	*vp = *np
	v.release()
	return true
}

// Disable/enable preemption, implemented in runtime.
func runtime_procPin() int
func runtime_procUnpin()
