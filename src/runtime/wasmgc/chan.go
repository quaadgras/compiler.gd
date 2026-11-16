// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Channels. There is one thread, so no locks: a channel operation runs
// to the point where it parks the goroutine.
//
// The compiler knows that the length and the capacity of a channel are
// its first two words. It gives makechan one more argument than it
// declares: the zero value of an element that is an object, or nil.

type hchan struct {
	qcount   uint // the elements in the buffer: must be first
	dataqsiz uint // the size of the buffer: must be second
	closed   bool
	elemtype *_type
	zero     unsafe.Pointer // the zero value, if the elements are objects
	buf      mapstore
	sendx    uint
	recvx    uint
	recvq    waitq
	sendq    waitq
	timer    bool   // the channel of a timer: it looks unbuffered
	tm       *timer // its timer, which may be due (see maybeRunChan)
	bubble   *synctestBubble // the synctest bubble it was made in
}

// A sudog is a goroutine waiting on a channel.
type sudog struct {
	g        *g
	c        *hchan
	elem     unsafe.Pointer // what to send, or where to receive: may be nil
	next     *sudog
	prev     *sudog
	success  bool // the operation was done, rather than the channel closed
	isSelect bool
}

type waitq struct {
	first *sudog
	last  *sudog
}

func (q *waitq) enqueue(sg *sudog) {
	sg.next = nil
	sg.prev = q.last
	if q.last == nil {
		q.first = sg
	} else {
		q.last.next = sg
	}
	q.last = sg
}

func (q *waitq) remove(sg *sudog) {
	if sg.prev == nil && sg.next == nil && q.first != sg {
		return // not in the queue
	}
	if sg.prev != nil {
		sg.prev.next = sg.next
	} else {
		q.first = sg.next
	}
	if sg.next != nil {
		sg.next.prev = sg.prev
	} else {
		q.last = sg.prev
	}
	sg.prev, sg.next = nil, nil
}

// dequeue returns the first goroutine waiting that was not already
// woken by another channel of its select.
func (q *waitq) dequeue() *sudog {
	for {
		sg := q.first
		if sg == nil {
			return nil
		}
		q.remove(sg)
		if sg.isSelect {
			if sg.g.selectDone {
				continue
			}
			sg.g.selectDone = true
		}
		return sg
	}
}

func makechan(t *abi.ChanType, size int, zero unsafe.Pointer) *hchan {
	elem := t.Elem
	if size < 0 || elem.Size_ != 0 && uintptr(size) > maxLinear/elem.Size_ {
		panic(plainError("makechan: size out of range"))
	}
	memstatsMallocs++
	c := &hchan{dataqsiz: uint(size), elemtype: elem, zero: zero, bubble: curg.bubble}
	if size > 0 {
		c.buf = newmapstore(elem, size)
	}
	return c
}

func makechan64(t *abi.ChanType, size int64, zero unsafe.Pointer) *hchan {
	if int64(int(size)) != size {
		panic(plainError("makechan: size out of range"))
	}
	return makechan(t, int(size), zero)
}

// gccopy copies the Go value in the object src to the object dst, of
// the same type. The compiler replaces a call to it.
func gccopy(dst, src unsafe.Pointer)

// copyelem copies an element of c from src to dst, if dst is not nil.
//
// TODO: a dst that is a pointer into an object.
func (c *hchan) copyelem(dst, src unsafe.Pointer) {
	switch {
	case dst == nil:
	case isRefType(c.elemtype):
		*(*unsafe.Pointer)(dst) = *(*unsafe.Pointer)(src)
	case c.zero != nil:
		gccopy(dst, src)
	default:
		memmove(dst, src, c.elemtype.Size_)
	}
}

// clearelem sets the element of c at dst to the zero value.
func (c *hchan) clearelem(dst unsafe.Pointer) {
	switch {
	case dst == nil:
	case isRefType(c.elemtype):
		*(*unsafe.Pointer)(dst) = nil
	case c.zero != nil:
		gccopy(dst, c.zero)
	default:
		memclrNoHeapPointers(dst, c.elemtype.Size_)
	}
}

// buffer adds the element at ep to the buffer of c.
func (c *hchan) buffer(ep unsafe.Pointer) {
	c.buf.put(int(c.sendx), ep)
	c.sendx++
	if c.sendx == c.dataqsiz {
		c.sendx = 0
	}
	c.qcount++
}

// unbuffer takes the first element of the buffer of c, to ep.
func (c *hchan) unbuffer(ep unsafe.Pointer) {
	c.copyelem(ep, c.buf.ptr(int(c.recvx)))
	if c.buf.objs != nil {
		objarrayset(c.buf.objs, int(c.recvx), nil)
	}
	c.recvx++
	if c.recvx == c.dataqsiz {
		c.recvx = 0
	}
	c.qcount--
}

func chansend1(c *hchan, elem unsafe.Pointer) {
	chansend(c, elem, true)
}

// chansend sends the element at ep on c. If it cannot without waiting
// and block is not set, it returns false.
func chansend(c *hchan, ep unsafe.Pointer, block bool) bool {
	if c == nil {
		if !block {
			return false
		}
		goparkIdle("chan send (nil chan)", nil)
		throw("unreachable")
	}
	if c.bubble != nil && curg.bubble != c.bubble {
		fatal("send on synctest channel from outside bubble")
	}
	if c.closed {
		panic(plainError("send on closed channel"))
	}
	if sg := c.recvq.dequeue(); sg != nil {
		// A receiver is waiting: hand it the element.
		c.copyelem(sg.elem, ep)
		sg.success = true
		sg.g.param = sg
		goready(sg.g)
		return true
	}
	if c.qcount < c.dataqsiz {
		c.buffer(ep)
		return true
	}
	if !block {
		return false
	}
	sg := &sudog{g: curg, c: c, elem: ep}
	c.sendq.enqueue(sg)
	c.park("chan send")
	if !sg.success {
		panic(plainError("send on closed channel"))
	}
	return true
}

func chanrecv1(c *hchan, elem unsafe.Pointer) {
	chanrecv(c, elem, true)
}

func chanrecv2(c *hchan, elem unsafe.Pointer) (received bool) {
	_, received = chanrecv(c, elem, true)
	return
}

// chanrecv receives an element of c to ep, which may be nil. If it
// cannot without waiting and block is not set, it returns (false,
// false). Otherwise selected is true, and received is whether an
// element was received rather than the channel closed.
func chanrecv(c *hchan, ep unsafe.Pointer, block bool) (selected, received bool) {
	if c == nil {
		if !block {
			return false, false
		}
		goparkIdle("chan receive (nil chan)", nil)
		throw("unreachable")
	}
	if c.bubble != nil && curg.bubble != c.bubble {
		fatal("receive on synctest channel from outside bubble")
	}
	if c.timer {
		maybeRunChan(c)
	}
	if sg := c.sendq.dequeue(); sg != nil {
		// A sender is waiting. Without a buffer, take its element;
		// with one, which is full, take the first of the buffer and
		// add the sender's.
		if c.dataqsiz == 0 {
			c.copyelem(ep, sg.elem)
		} else {
			c.unbuffer(ep)
			c.buffer(sg.elem)
		}
		sg.success = true
		sg.g.param = sg
		goready(sg.g)
		return true, true
	}
	if c.qcount > 0 {
		c.unbuffer(ep)
		return true, true
	}
	if c.closed {
		c.clearelem(ep)
		return true, false
	}
	if !block {
		return false, false
	}
	sg := &sudog{g: curg, c: c, elem: ep}
	c.recvq.enqueue(sg)
	if c.timer {
		blockTimerChan(c)
	}
	c.park("chan receive")
	if c.timer {
		unblockTimerChan(c)
	}
	return true, sg.success
}

func closechan(c *hchan) {
	if c == nil {
		panic(plainError("close of nil channel"))
	}
	if c.bubble != nil && curg.bubble != c.bubble {
		fatal("close of synctest channel from outside bubble")
	}
	if c.closed {
		panic(plainError("close of closed channel"))
	}
	c.closed = true
	// The receivers get the zero value; the senders panic.
	for {
		sg := c.recvq.dequeue()
		if sg == nil {
			break
		}
		c.clearelem(sg.elem)
		sg.success = false
		sg.g.param = sg
		goready(sg.g)
	}
	for {
		sg := c.sendq.dequeue()
		if sg == nil {
			break
		}
		sg.success = false
		sg.g.param = sg
		goready(sg.g)
	}
}

func selectnbsend(c *hchan, elem unsafe.Pointer) (selected bool) {
	return chansend(c, elem, false)
}

func selectnbrecv(elem unsafe.Pointer, c *hchan) (selected, received bool) {
	return chanrecv(c, elem, false)
}

// block is select with no cases.
func block() {
	goparkIdle("select (no cases)", nil)
}

// park parks the goroutine on c: durably blocked if c is in its
// bubble.
func (c *hchan) park(why string) {
	if c.bubble != nil {
		goparkIdle(why, nil)
	} else {
		gopark(why)
	}
}

// A scase is a case of a select: the compiler builds an array of
// them, the sends first.
type scase struct {
	c    *hchan
	elem unsafe.Pointer
}

var selectSeed uint32 = 0x9E3779B9

// selectgo implements the select statement. It returns the index of
// the case chosen (-1 for the default, if block is not set) and, if
// that was a receive, whether a value was received.
func selectgo(cas0 *scase, order0 *uint16, pc0 *uintptr, nsends, nrecvs int, block bool) (int, bool) {
	ncases := nsends + nrecvs
	cases := unsafe.Slice(cas0, ncases)

	// Try each case, starting from a random one.
	selectSeed ^= selectSeed << 13
	selectSeed ^= selectSeed >> 17
	selectSeed ^= selectSeed << 5
	for n := 0; n < ncases; n++ {
		i := (n + int(selectSeed%uint32(ncases))) % ncases
		cas := &cases[i]
		c := cas.c
		if c == nil {
			continue
		}
		if i < nsends {
			if c.closed {
				panic(plainError("send on closed channel"))
			}
			if chansend(c, cas.elem, false) {
				return i, false
			}
		} else if selected, received := chanrecv(c, cas.elem, false); selected {
			return i, received
		}
	}
	if !block {
		return -1, false
	}

	// Wait on all of them.
	gp := curg
	gp.selectDone = false
	gp.param = nil
	sgs := make([]*sudog, ncases)
	allSynctest := true
	for i := range cases {
		cas := &cases[i]
		if cas.c == nil {
			continue
		}
		if cas.c.bubble == nil {
			allSynctest = false
		}
		sg := &sudog{g: gp, c: cas.c, elem: cas.elem, isSelect: true}
		sgs[i] = sg
		if i < nsends {
			cas.c.sendq.enqueue(sg)
		} else {
			cas.c.recvq.enqueue(sg)
			if cas.c.timer {
				blockTimerChan(cas.c)
			}
		}
	}
	if allSynctest && gp.bubble != nil {
		goparkIdle("select", nil)
	} else {
		gopark("select")
	}
	for i := nsends; i < ncases; i++ {
		if c := cases[i].c; c != nil && c.timer {
			unblockTimerChan(c)
		}
	}

	// One of them woke the goroutine: leave the queues of the others.
	chosen := -1
	for i, sg := range sgs {
		if sg == nil {
			continue
		}
		if sg == gp.param {
			chosen = i
			continue
		}
		if i < nsends {
			sg.c.sendq.remove(sg)
		} else {
			sg.c.recvq.remove(sg)
		}
	}
	gp.param = nil
	gp.selectDone = false
	if chosen < 0 {
		throw("select: woken by no case")
	}
	sg := sgs[chosen]
	if chosen < nsends && !sg.success {
		panic(plainError("send on closed channel"))
	}
	return chosen, sg.success
}

func chanlen(c *hchan) int {
	if c == nil {
		return 0
	}
	if c.timer {
		return 0
	}
	return int(c.qcount)
}

func chancap(c *hchan) int {
	if c == nil {
		return 0
	}
	if c.timer {
		return 0
	}
	return int(c.dataqsiz)
}
