// Code generated from _gen/allocators.go using 'go generate'; DO NOT EDIT.

package ssa

import (
	"internal/unsafeheader"
	"math/bits"
	"unsafe"
)

// cacheFree has the free lists of a Cache, by the logarithm of the size.
type cacheFree struct {
	ValueSlice   [27][][]*Value
	LimitSlice   [27][][]limit
	SparseSet    [27][]*sparseSet
	SparseMap    [27][]*sparseMap
	SparseMapPos [27][]*sparseMapPos
}

func (c *Cache) allocValueSlice(n int) []*Value {
	var s []*Value
	n2 := n
	if n2 < 32 {
		n2 = 32
	}
	b := bits.Len(uint(n2 - 1))
	l := &c.free.ValueSlice[b-5]
	if k := len(*l); k == 0 {
		s = make([]*Value, 1<<b)
	} else {
		s = (*l)[k-1]
		(*l)[k-1] = nil
		*l = (*l)[:k-1]
	}
	s = s[:n]
	return s
}
func (c *Cache) freeValueSlice(s []*Value) {
	clear(s)
	b := bits.Len(uint(cap(s)) - 1)
	c.free.ValueSlice[b-5] = append(c.free.ValueSlice[b-5], s)
}
func (c *Cache) allocLimitSlice(n int) []limit {
	var s []limit
	n2 := n
	if n2 < 8 {
		n2 = 8
	}
	b := bits.Len(uint(n2 - 1))
	l := &c.free.LimitSlice[b-3]
	if k := len(*l); k == 0 {
		s = make([]limit, 1<<b)
	} else {
		s = (*l)[k-1]
		(*l)[k-1] = nil
		*l = (*l)[:k-1]
	}
	s = s[:n]
	return s
}
func (c *Cache) freeLimitSlice(s []limit) {
	clear(s)
	b := bits.Len(uint(cap(s)) - 1)
	c.free.LimitSlice[b-3] = append(c.free.LimitSlice[b-3], s)
}
func (c *Cache) allocSparseSet(n int) *sparseSet {
	var s *sparseSet
	n2 := n
	if n2 < 32 {
		n2 = 32
	}
	b := bits.Len(uint(n2 - 1))
	l := &c.free.SparseSet[b-5]
	if k := len(*l); k == 0 {
		s = newSparseSet(1 << b)
	} else {
		s = (*l)[k-1]
		(*l)[k-1] = nil
		*l = (*l)[:k-1]
	}
	return s
}
func (c *Cache) freeSparseSet(s *sparseSet) {
	s.clear()
	b := bits.Len(uint(s.cap()) - 1)
	c.free.SparseSet[b-5] = append(c.free.SparseSet[b-5], s)
}
func (c *Cache) allocSparseMap(n int) *sparseMap {
	var s *sparseMap
	n2 := n
	if n2 < 32 {
		n2 = 32
	}
	b := bits.Len(uint(n2 - 1))
	l := &c.free.SparseMap[b-5]
	if k := len(*l); k == 0 {
		s = newSparseMap(1 << b)
	} else {
		s = (*l)[k-1]
		(*l)[k-1] = nil
		*l = (*l)[:k-1]
	}
	return s
}
func (c *Cache) freeSparseMap(s *sparseMap) {
	s.clear()
	b := bits.Len(uint(s.cap()) - 1)
	c.free.SparseMap[b-5] = append(c.free.SparseMap[b-5], s)
}
func (c *Cache) allocSparseMapPos(n int) *sparseMapPos {
	var s *sparseMapPos
	n2 := n
	if n2 < 32 {
		n2 = 32
	}
	b := bits.Len(uint(n2 - 1))
	l := &c.free.SparseMapPos[b-5]
	if k := len(*l); k == 0 {
		s = newSparseMapPos(1 << b)
	} else {
		s = (*l)[k-1]
		(*l)[k-1] = nil
		*l = (*l)[:k-1]
	}
	return s
}
func (c *Cache) freeSparseMapPos(s *sparseMapPos) {
	s.clear()
	b := bits.Len(uint(s.cap()) - 1)
	c.free.SparseMapPos[b-5] = append(c.free.SparseMapPos[b-5], s)
}
func (c *Cache) allocBlockSlice(n int) []*Block {
	var base *Value
	var derived *Block
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocValueSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]*Block)(unsafe.Pointer(&s))
}
func (c *Cache) freeBlockSlice(s []*Block) {
	var base *Value
	var derived *Block
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeValueSlice(*(*[]*Value)(unsafe.Pointer(&b)))
}
func (c *Cache) allocInt64(n int) []int64 {
	var base limit
	var derived int64
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]int64)(unsafe.Pointer(&s))
}
func (c *Cache) freeInt64(s []int64) {
	var base limit
	var derived int64
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocIntSlice(n int) []int {
	var base limit
	var derived int
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]int)(unsafe.Pointer(&s))
}
func (c *Cache) freeIntSlice(s []int) {
	var base limit
	var derived int
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocInt32Slice(n int) []int32 {
	var base limit
	var derived int32
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]int32)(unsafe.Pointer(&s))
}
func (c *Cache) freeInt32Slice(s []int32) {
	var base limit
	var derived int32
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocInt8Slice(n int) []int8 {
	var base limit
	var derived int8
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]int8)(unsafe.Pointer(&s))
}
func (c *Cache) freeInt8Slice(s []int8) {
	var base limit
	var derived int8
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocBoolSlice(n int) []bool {
	var base limit
	var derived bool
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]bool)(unsafe.Pointer(&s))
}
func (c *Cache) freeBoolSlice(s []bool) {
	var base limit
	var derived bool
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocIDSlice(n int) []ID {
	var base limit
	var derived ID
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]ID)(unsafe.Pointer(&s))
}
func (c *Cache) freeIDSlice(s []ID) {
	var base limit
	var derived ID
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocUintSlice(n int) []uint {
	var base limit
	var derived uint
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]uint)(unsafe.Pointer(&s))
}
func (c *Cache) freeUintSlice(s []uint) {
	var base limit
	var derived uint
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
func (c *Cache) allocKnownBitsEntriesSlice(n int) []knownBitsEntry {
	var base limit
	var derived knownBitsEntry
	if unsafe.Sizeof(base)%unsafe.Sizeof(derived) != 0 {
		panic("bad")
	}
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := c.allocLimitSlice(int((uintptr(n) + scale - 1) / scale))
	s := unsafeheader.Slice{
		Data: unsafe.Pointer(&b[0]),
		Len:  n,
		Cap:  cap(b) * int(scale),
	}
	return *(*[]knownBitsEntry)(unsafe.Pointer(&s))
}
func (c *Cache) freeKnownBitsEntriesSlice(s []knownBitsEntry) {
	var base limit
	var derived knownBitsEntry
	scale := unsafe.Sizeof(base) / unsafe.Sizeof(derived)
	b := unsafeheader.Slice{
		Data: unsafe.Pointer(&s[0]),
		Len:  int((uintptr(len(s)) + scale - 1) / scale),
		Cap:  int((uintptr(cap(s)) + scale - 1) / scale),
	}
	c.freeLimitSlice(*(*[]limit)(unsafe.Pointer(&b)))
}
