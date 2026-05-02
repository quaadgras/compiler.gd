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
//
// gd fat-iface design (lock-free reads, CAS-loop writes, no allocation):
//
// Stock Go publishes a 16-byte iface (Type+Data) using ordered atomic
// pointer writes; readers see a consistent snapshot once Type is non-nil.
// The fork's 32-byte fat-iface header (Type+Data+Inline[16B]) doesn't fit
// any single atomic instruction, so we use a 2-slot seqlock:
//
//   - Two inline slots a, b each hold a full 32-byte iface header.
//   - seq is even when quiescent; bit 0 is the writer-in-flight flag.
//   - Bit 1 of seq selects the active slot (0 = a, 1 = b).
//   - Writers CAS seq from even N to N+1 to claim the write window;
//     losers retry. The winner writes the *inactive* slot and publishes
//     by storing N+2. Multiple concurrent writes naturally serialize as
//     "last writer wins" — each successful CAS publishes the latest
//     observed state.
//   - Readers loop: read seq, check it's even, copy 32 bytes from the
//     active slot, re-read seq, retry on mismatch.
//
// Properties:
//   - Loads issue no atomic CAS, no procPin, no cache-line writes.
//     Just a uint64 load + 32-byte copy + uint64 load in the fast path.
//   - Stores allocate nothing — the slots are part of the struct.
//   - No procPin: the writer's window between CAS-claim and publish is
//     a few instructions; readers tolerate the writer being preempted
//     by spinning on the odd-seq check.
type Value struct {
	// seq is the seqlock counter. Bit 0 is the writer-in-flight flag.
	// Bit 1 selects the active slot. seq increments by 1 to enter the
	// write window, by 1 again to publish (so by 2 per Store overall).
	// seq == 0 means no Store has happened yet.
	seq uint64

	// a, b alternate as the active publication slot. A writer always
	// writes to the *inactive* slot (the one not selected by the
	// pre-bump seq), then bumps seq to publish.
	a abi.EmptyInterface
	b abi.EmptyInterface
}

// readSlot returns a pointer to the slot that the value identified by
// seq lives in. seq must be even (caller checks).
func (v *Value) readSlot(seq uint64) *abi.EmptyInterface {
	if (seq>>1)&1 == 0 {
		return &v.a
	}
	return &v.b
}

// writeSlot returns a pointer to the slot the writer should fill, given
// the pre-bump seq value. The writer fills this slot before bumping seq.
// (The post-publish seq is curSeq+2, whose slot bit selects this one.)
func (v *Value) writeSlot(curSeq uint64) *abi.EmptyInterface {
	if ((curSeq+2)>>1)&1 == 0 {
		return &v.a
	}
	return &v.b
}

// Load returns the value set by the most recent Store.
// It returns nil if there has been no call to Store for this Value.
func (v *Value) Load() (val any) {
	for {
		s1 := LoadUint64(&v.seq)
		if s1 == 0 {
			return nil
		}
		if s1&1 != 0 {
			// Writer in flight; spin briefly and retry.
			continue
		}
		slot := v.readSlot(s1)
		local := *slot // 32-byte copy; may tear if a writer wins the race
		s2 := LoadUint64(&v.seq)
		if s1 == s2 {
			*(*abi.EmptyInterface)(unsafe.Pointer(&val)) = local
			return val
		}
	}
}

// claim spins on CAS to acquire the write window. Returns the pre-claim
// seq value (always even). procPin keeps the goroutine on its current
// P only between CAS-success and publish so the publish step (a single
// uint64 store a few instructions later) can't be preempted — that
// would leave readers/writers spinning on the odd-seq check until the
// scheduler resumed the writer (microseconds, but observably slow
// under heavy concurrent Store load: dropping procPin entirely
// regressed TestValueSwapConcurrent from ~120s to >300s timeout).
//
// The losers in the CAS race are NOT pinned, so the Go scheduler can
// preempt them periodically and let other work make progress. Holding
// procPin across the contended spin live-locked under heavy concurrent
// Store: more goroutines than cores, all CAS-spinning pinned, no
// scheduler timeslice ever firing.
func (v *Value) claim() uint64 {
	for {
		cur := LoadUint64(&v.seq)
		if cur&1 == 0 {
			runtime_procPin()
			if CompareAndSwapUint64(&v.seq, cur, cur+1) {
				return cur
			}
			runtime_procUnpin()
		}
	}
}

// publish writes np to the inactive slot and bumps seq to cur+2 so
// readers see the new value. The caller must hold the write window.
func (v *Value) publish(cur uint64, np abi.EmptyInterface) {
	*v.writeSlot(cur) = np
	StoreUint64(&v.seq, cur+2)
	runtime_procUnpin()
}

// abandon releases the write window without publishing a new value
// (used on type mismatch / CAS-old-mismatch paths).
func (v *Value) abandon(cur uint64) {
	StoreUint64(&v.seq, cur)
	runtime_procUnpin()
}

// Disable/enable preemption, implemented in runtime.
func runtime_procPin() int
func runtime_procUnpin()

// Store sets the value of the [Value] v to val.
// All calls to Store for a given Value must use values of the same concrete type.
// Store of an inconsistent type panics, as does Store(nil).
func (v *Value) Store(val any) {
	if val == nil {
		panic("sync/atomic: store of nil value into Value")
	}
	np := *(*abi.EmptyInterface)(unsafe.Pointer(&val))

	// Fast path: if the current published value is bytewise equal to
	// np, there's nothing to do. Skipping claim/publish here is what
	// keeps parallel Store of the same value (e.g. expvar's repeated
	// String.Set) from CAS-spinning every caller — under heavy
	// contention claim() serialises all writers, but a no-op store
	// can be observed lock-free via the same protocol Load uses.
	if s1 := LoadUint64(&v.seq); s1 != 0 && s1&1 == 0 {
		local := *v.readSlot(s1)
		if LoadUint64(&v.seq) == s1 && ifaceEq(local, np) {
			return
		}
	}

	cur := v.claim()
	if cur > 0 {
		curSlot := v.readSlot(cur)
		if curSlot.Type != np.Type {
			v.abandon(cur)
			panic("sync/atomic: store of inconsistently typed value into Value")
		}
	}
	v.publish(cur, np)
}

// ifaceEq reports whether two 32-byte iface headers are bytewise
// identical. Reads them as 4×uint64 so the compiler turns this into
// four cmp+jne pairs with no branches in the equal case.
func ifaceEq(x, y abi.EmptyInterface) bool {
	xp := (*[4]uint64)(unsafe.Pointer(&x))
	yp := (*[4]uint64)(unsafe.Pointer(&y))
	return xp[0] == yp[0] && xp[1] == yp[1] && xp[2] == yp[2] && xp[3] == yp[3]
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
	np := *(*abi.EmptyInterface)(unsafe.Pointer(&new))

	cur := v.claim()
	if cur > 0 {
		curSlot := v.readSlot(cur)
		if curSlot.Type != np.Type {
			v.abandon(cur)
			panic("sync/atomic: swap of inconsistently typed value into Value")
		}
		*(*abi.EmptyInterface)(unsafe.Pointer(&old)) = *curSlot
	}
	v.publish(cur, np)
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
	np := *(*abi.EmptyInterface)(unsafe.Pointer(&new))
	op := (*abi.EmptyInterface)(unsafe.Pointer(&old))
	if op.Type != nil && np.Type != op.Type {
		panic("sync/atomic: compare and swap of inconsistently typed values")
	}

	cur := v.claim()
	if cur == 0 {
		if old != nil {
			v.abandon(cur)
			return false
		}
		v.publish(cur, np)
		return true
	}
	curSlot := v.readSlot(cur)
	if curSlot.Type != np.Type {
		v.abandon(cur)
		panic("sync/atomic: compare and swap of inconsistently typed value into Value")
	}
	// Reconstruct the current `any` and compare via runtime equality.
	var cur_any any
	*(*abi.EmptyInterface)(unsafe.Pointer(&cur_any)) = *curSlot
	if cur_any != old {
		v.abandon(cur)
		return false
	}
	v.publish(cur, np)
	return true
}
