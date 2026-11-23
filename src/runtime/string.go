// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/bytealg"
	"internal/goarch"
	"internal/goos"
	"internal/runtime/math"
	"internal/runtime/sys"
	"internal/strconv"
	"unsafe"
)

// The constant is known to the compiler.
// There is no fundamental theory behind this number.
const tmpStringBufSize = 32

type tmpBuf [tmpStringBufSize]byte

// concatstrings implements a Go string concatenation x+y+z+...
// The operands are passed in the slice a.
// If buf != nil, the compiler has determined that the result does not
// escape the calling function, so the string data can be stored in buf
// if small enough.
func concatstrings(buf *tmpBuf, a []string) string {
	idx := 0
	l := 0
	count := 0
	for i, x := range a {
		n := len(x)
		if n == 0 {
			continue
		}
		if l+n < l {
			throw("string concatenation too long")
		}
		l += n
		count++
		idx = i
	}
	if count == 0 {
		return ""
	}

	// If there is just one string and either it is not on the stack
	// or our result does not escape the calling frame (buf != nil),
	// then we can return that string directly.
	if count == 1 && (buf != nil || !stringDataOnStack(a[idx])) {
		return a[idx]
	}
	// gd: 1..15-byte results are inline. Zero alloc and no dependency
	// on buf — we only touch a local 16-byte staging buffer.
	if l <= abi.StringInlineCap {
		var tmp [16]byte
		off := 0
		for _, x := range a {
			off += copy(tmp[off:], x)
		}
		return abi.StringFromInlineBytes(&tmp, l)
	}
	s, b := rawstringtmp(buf, l)
	for _, x := range a {
		n := copy(b, x)
		b = b[n:]
	}
	if !storesOrdered && (buf == nil || l > len(buf)) {
		// The bytes of a new string are seen before it is: what is
		// there before them is what a freed object left.
		publicationBarrier()
	}
	return s
}

// concatstring2 helps make the callsite smaller (compared to concatstrings),
// and we think this is currently more valuable than omitting one call in the
// chain, the same goes for concatstring{3,4,5}.
func concatstring2(buf *tmpBuf, a0, a1 string) string {
	return concatstrings(buf, []string{a0, a1})
}

func concatstring3(buf *tmpBuf, a0, a1, a2 string) string {
	return concatstrings(buf, []string{a0, a1, a2})
}

func concatstring4(buf *tmpBuf, a0, a1, a2, a3 string) string {
	return concatstrings(buf, []string{a0, a1, a2, a3})
}

func concatstring5(buf *tmpBuf, a0, a1, a2, a3, a4 string) string {
	return concatstrings(buf, []string{a0, a1, a2, a3, a4})
}

// concatbytes implements a Go string concatenation x+y+z+... returning a slice
// of bytes.
// The operands are passed in the slice a.
func concatbytes(buf *tmpBuf, a []string) []byte {
	l := 0
	for _, x := range a {
		n := len(x)
		if l+n < l {
			throw("string concatenation too long")
		}
		l += n
	}
	if l == 0 {
		// This is to match the return type of the non-optimized concatenation.
		return []byte{}
	}

	var b []byte
	if buf != nil && l <= len(buf) {
		*buf = tmpBuf{}
		b = buf[:l]
	} else {
		b = rawbyteslice(l)
	}
	offset := 0
	for _, x := range a {
		copy(b[offset:], x)
		offset += len(x)
	}
	if !storesOrdered && (buf == nil || l > len(buf)) {
		publicationBarrier()
	}

	return b
}

// concatbyte2 helps make the callsite smaller (compared to concatbytes),
// and we think this is currently more valuable than omitting one call in
// the chain, the same goes for concatbyte{3,4,5}.
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

// slicebytetostring converts a byte slice to a string.
// It is inserted by the compiler into generated code.
// ptr is a pointer to the first element of the slice;
// n is the length of the slice.
// Buf is a fixed-size buffer for the result,
// it is not nil if the result does not escape.
func slicebytetostring(buf *tmpBuf, ptr *byte, n int) string {
	if n == 0 {
		// Turns out to be a relatively common case.
		// Consider that you want to parse out data between parens in "foo()bar",
		// you find the indices and convert the subslice to string.
		return ""
	}
	if raceenabled {
		racereadrangepc(unsafe.Pointer(ptr),
			uintptr(n),
			sys.GetCallerPC(),
			abi.FuncPCABIInternal(slicebytetostring))
	}
	if msanenabled {
		msanread(unsafe.Pointer(ptr), uintptr(n))
	}
	if asanenabled {
		asanread(unsafe.Pointer(ptr), uintptr(n))
	}
	// gd: 1..15-byte results are inline — zero alloc, no dependency on
	// buf. Supersedes stock's n==1 staticuint64s shortcut on 64-bit.
	if n <= abi.StringInlineCap {
		return inlineStringFromBytes(ptr, n)
	}
	if n == 1 {
		p := unsafe.Pointer(&staticuint64s[*ptr])
		if goarch.BigEndian {
			p = add(p, 7)
		}
		return unsafe.String((*byte)(p), 1)
	}

	var p unsafe.Pointer
	if buf != nil && n <= len(buf) {
		p = unsafe.Pointer(buf)
	} else {
		p = mallocgc(uintptr(n), nil, false)
	}
	memmove(p, unsafe.Pointer(ptr), uintptr(n))
	if !storesOrdered && (buf == nil || n > len(buf)) {
		publicationBarrier()
	}
	return unsafe.String((*byte)(p), n)
}

// stringDataOnStack reports whether the string's data is
// stored on the current goroutine's stack.
func stringDataOnStack(s string) bool {
	sh := stringStructOf(&s)
	if sh.isInline() {
		return false
	}
	ptr := uintptr(sh.str)
	stk := getg().stack
	return stk.lo <= ptr && ptr < stk.hi
}

func rawstringtmp(buf *tmpBuf, l int) (s string, b []byte) {
	if buf != nil && l <= len(buf) {
		b = buf[:l]
		s = slicebytetostringtmp(&b[0], len(b))
	} else {
		s, b = rawstring(l)
	}
	return
}

// slicebytetostringtmp returns a "string" referring to the actual []byte bytes.
//
// Callers need to ensure that the returned string will not be used after
// the calling goroutine modifies the original slice or synchronizes with
// another goroutine.
//
// The function is only called when instrumenting
// and otherwise intrinsified by the compiler.
//
// Some internal compiler optimizations use this function.
//   - Used for m[T1{... Tn{..., string(k), ...} ...}] and m[string(k)]
//     where k is []byte, T1 to Tn is a nesting of struct and array literals.
//   - Used for "<"+string(b)+">" concatenation where b is []byte.
//   - Used for string(b)=="foo" comparison where b is []byte.
func slicebytetostringtmp(ptr *byte, n int) string {
	if raceenabled && n > 0 {
		racereadrangepc(unsafe.Pointer(ptr),
			uintptr(n),
			sys.GetCallerPC(),
			abi.FuncPCABIInternal(slicebytetostringtmp))
	}
	if msanenabled && n > 0 {
		msanread(unsafe.Pointer(ptr), uintptr(n))
	}
	if asanenabled && n > 0 {
		asanread(unsafe.Pointer(ptr), uintptr(n))
	}
	return unsafe.String(ptr, n)
}

func stringtoslicebyte(buf *tmpBuf, s string) []byte {
	var b []byte
	if buf != nil && len(s) <= len(buf) {
		*buf = tmpBuf{}
		b = buf[:len(s)]
	} else {
		b = rawbyteslice(len(s))
	}
	copy(b, s)
	if !storesOrdered && (buf == nil || len(s) > len(buf)) {
		publicationBarrier()
	}
	return b
}

func stringtoslicerune(buf *[tmpStringBufSize]rune, s string) []rune {
	// two passes.
	// unlike slicerunetostring, no race because strings are immutable.
	n := 0
	for range s {
		n++
	}

	var a []rune
	if buf != nil && n <= len(buf) {
		*buf = [tmpStringBufSize]rune{}
		a = buf[:n]
	} else {
		a = rawruneslice(n)
	}

	n = 0
	for _, r := range s {
		a[n] = r
		n++
	}
	if !storesOrdered && (buf == nil || n > len(buf)) {
		publicationBarrier()
	}
	return a
}

func slicerunetostring(buf *tmpBuf, a []rune) string {
	if raceenabled && len(a) > 0 {
		racereadrangepc(unsafe.Pointer(&a[0]),
			uintptr(len(a))*unsafe.Sizeof(a[0]),
			sys.GetCallerPC(),
			abi.FuncPCABIInternal(slicerunetostring))
	}
	if msanenabled && len(a) > 0 {
		msanread(unsafe.Pointer(&a[0]), uintptr(len(a))*unsafe.Sizeof(a[0]))
	}
	if asanenabled && len(a) > 0 {
		asanread(unsafe.Pointer(&a[0]), uintptr(len(a))*unsafe.Sizeof(a[0]))
	}
	var dum [4]byte
	size1 := 0
	for _, r := range a {
		size1 += encoderune(dum[:], r)
	}
	// gd: if the final encoding certainly fits inline, encode into a
	// local 16-byte staging buffer and pack directly — no rawstringtmp
	// (no heap alloc, no buf dep).
	if size1 >= 1 && size1 <= abi.StringInlineCap {
		var tmp [16]byte
		size2 := 0
		for _, r := range a {
			if size2 >= size1 {
				break
			}
			size2 += encoderune(tmp[size2:], r)
		}
		return inlineStringFromBytes(&tmp[0], size2)
	}
	s, b := rawstringtmp(buf, size1+3)
	size2 := 0
	for _, r := range a {
		// check for race
		if size2 >= size1 {
			break
		}
		size2 += encoderune(b[size2:], r)
	}
	if !storesOrdered && (buf == nil || size1+3 > len(buf)) {
		publicationBarrier()
	}
	return s[:size2]
}

// stringStruct is the runtime layout of a Go string: two words, as in
// stock Go, but with 8-byte words a string of 1 to 15 bytes is inline:
// str has bit 63 set, the length at bit 56 and bytes 8..14 below it, and
// len is bytes 0..7. See internal/abi/string.go and doc/gd/sso-string.md.
// The collector scans str as a pointer; an inline one is no address in
// the heap, which it ignores.
type stringStruct struct {
	str unsafe.Pointer
	len int
}

// Variant with *byte pointer type for DWARF debugging.
type stringStructDWARF struct {
	str *byte
	len int
}

func stringStructOf(sp *string) *stringStruct {
	return (*stringStruct)(unsafe.Pointer(sp))
}

// isInline reports whether the string is inline.
//
//go:nosplit
func (s *stringStruct) isInline() bool {
	return goarch.PtrSize == 8 && int64(uintptr(s.str)) < 0
}

// length returns the length of the string.
//
//go:nosplit
func (s *stringStruct) length() int {
	if s.isInline() {
		return int(uint64(uintptr(s.str)) >> abi.StringTagShift & 15)
	}
	return s.len
}

// data returns the address of the string's bytes: str, or for an inline
// string buf, to which it copies them. Callers must not keep it past buf.
//
//go:nosplit
func (s *stringStruct) data(buf *[16]byte) unsafe.Pointer {
	if s.isInline() {
		*buf, _ = abi.StringInlineBytes(*(*string)(unsafe.Pointer(s)))
		return noescape(unsafe.Pointer(buf))
	}
	return s.str
}

// stringSameBacking reports whether a and b share the same underlying
// byte storage. Two heap-rep strings share backing iff their data
// pointers are equal. Two inline strings share "backing" iff their
// words are equal (the same content: there is no distinct backing for
// inline bytes). Mixed rep is never shared: an inline copy of a heap
// string's content has independent storage.
//
// Intended for tests and clone-detection paths that need to ask "did
// this op copy the bytes?" — a question unsafe.StringData pointer
// identity can no longer answer for inline strings.
//
//go:nosplit
func stringSameBacking(a, b string) bool {
	ah := (*stringStruct)(unsafe.Pointer(&a))
	bh := (*stringStruct)(unsafe.Pointer(&b))
	if ah.isInline() && bh.isInline() {
		return *ah == *bh
	}
	return ah.str == bh.str
}

// stringDataHeap returns a stable pointer to the bytes of s. For heap-
// rep inputs it returns s.str unchanged (matching stock unsafe.StringData
// semantics). For inline inputs it allocates a 1..15-byte heap buffer,
// copies the inline bytes in, and returns the buffer — so the returned
// pointer never aliases the caller's stack and survives as long as the
// caller holds it.
//
// Used by the compiler for user-visible unsafe.StringData calls via
// walkUnsafeData. Runtime-internal callers that consume the pointer
// within a single call should use stringStruct.data instead —
// stringDataHeap's alloc path is not reachable from //go:nowritebarrierrec
// contexts (writeErrStr, dloggerImpl.s, heapdump.*).
func stringDataHeap(s string) *byte {
	sh := (*stringStruct)(unsafe.Pointer(&s))
	if !sh.isInline() {
		return (*byte)(sh.str)
	}
	b, n := abi.StringInlineBytes(s)
	p := mallocgc(uintptr(n), nil, false)
	memmove(p, noescape(unsafe.Pointer(&b)), uintptr(n))
	if !storesOrdered {
		publicationBarrier()
	}
	return (*byte)(p)
}

// cgoStringNormalize ensures s is not inline before it is passed to a
// cgo C function, which reads a string as its address and its length
// (_GoString_). Cgo's generated Go stubs call this helper on each
// string argument.
//
// Other strings are returned unchanged. An inline one is copied to a
// len-sized heap buffer.
//
//go:linkname cgoStringNormalize
func cgoStringNormalize(s string) string {
	sh := (*stringStruct)(unsafe.Pointer(&s))
	if !sh.isInline() {
		return s
	}
	b, n := abi.StringInlineBytes(s)
	p := mallocgc(uintptr(n), nil, false)
	memmove(p, noescape(unsafe.Pointer(&b)), uintptr(n))
	if !storesOrdered {
		publicationBarrier()
	}
	return unsafe.String((*byte)(p), n)
}

// inlineStringFromBytes packs 1..15 bytes starting at ptr into an
// inline string. Zero allocations; the returned string is
// self-contained, so callers can hand it back without worrying about
// the source buffer's lifetime. 64-bit only; callers must gate on
// goarch.PtrSize == 8 (abi.StringInlineCap).
//
//go:nosplit
func inlineStringFromBytes(ptr *byte, n int) string {
	var buf [16]byte
	memmove(noescape(unsafe.Pointer(&buf[0])), unsafe.Pointer(ptr), uintptr(n))
	return abi.StringFromInlineBytes(&buf, n)
}

// stringcopy implements copy(dst, src) where src is a string, inline or
// not.
//
// Returns the number of bytes copied: min(dstLen, len(src)).
//
//go:nosplit
func stringcopy(dst *byte, dstLen int, s string) int {
	n := len(s)
	if n > dstLen {
		n = dstLen
	}
	if n == 0 {
		return 0
	}
	var buf [16]byte
	sh := (*stringStruct)(unsafe.Pointer(&s))
	memmove(unsafe.Pointer(dst), sh.data(&buf), uintptr(n))
	return n
}

// sliceinlinestring is called by the compiler to implement OSLICESTR
// when the source string is inline. It copies bytes[lo:hi] into a fresh
// inline string and returns it as a new string value — no pointer into
// the caller's frame is exposed. Bounds have already been checked by
// the caller.
//
//go:nosplit
func sliceinlinestring(s string, lo, hi int) string {
	n := hi - lo
	if n == 0 {
		return ""
	}
	b, _ := abi.StringInlineBytes(s)
	var r [16]byte
	copy(r[:n], b[lo:hi])
	return abi.StringFromInlineBytes(&r, n)
}

func intstring(buf *[4]byte, v int64) (s string) {
	if int64(rune(v)) != v {
		v = runeError
	}
	// gd: a rune encodes to 1..4 bytes, always inline with 8-byte
	// words. Zero alloc regardless of buf.
	if abi.StringInlineCap > 0 {
		var tmp [4]byte
		n := encoderune(tmp[:], rune(v))
		return inlineStringFromBytes(&tmp[0], n)
	}
	var b []byte
	if buf != nil {
		b = buf[:]
		s = slicebytetostringtmp(&b[0], len(b))
	} else {
		s, b = rawstring(4)
	}
	n := encoderune(b, rune(v))
	if !storesOrdered && buf == nil {
		publicationBarrier()
	}
	return s[:n]
}

// rawstring allocates storage for a new string. The returned
// string and byte slice both refer to the same storage.
// The storage is not zeroed. Callers should use
// b to set the string contents and then drop b, and where stores are not
// in order (storesOrdered) call publicationBarrier before the string can
// reach another goroutine: what is there before is a freed object's.
func rawstring(size int) (s string, b []byte) {
	p := mallocgc(uintptr(size), nil, false)
	return unsafe.String((*byte)(p), size), unsafe.Slice((*byte)(p), size)
}

// rawbyteslice allocates a new byte slice. The byte slice is not zeroed.
func rawbyteslice(size int) (b []byte) {
	cap := roundupsize(uintptr(size), true)
	p := mallocgc(cap, nil, false)
	if cap != uintptr(size) {
		memclrNoHeapPointers(add(p, uintptr(size)), cap-uintptr(size))
	}

	return unsafe.Slice((*byte)(p), cap)[:size]
}

// rawruneslice allocates a new rune slice. The rune slice is not zeroed.
func rawruneslice(size int) (b []rune) {
	if uintptr(size) > maxAlloc/4 {
		throw("out of memory")
	}
	mem := roundupsize(uintptr(size)*4, true)
	p := mallocgc(mem, nil, false)
	if mem != uintptr(size)*4 {
		memclrNoHeapPointers(add(p, uintptr(size)*4), mem-uintptr(size)*4)
	}

	return unsafe.Slice((*rune)(p), mem/4)[:size]
}

// used by cmd/cgo
func gobytes(p *byte, n int) (b []byte) {
	if n == 0 {
		return make([]byte, 0)
	}

	if n < 0 || uintptr(n) > maxAlloc {
		panic(errorString("gobytes: length out of range"))
	}

	bp := mallocgc(uintptr(n), nil, false)
	memmove(bp, unsafe.Pointer(p), uintptr(n))
	if !storesOrdered {
		publicationBarrier()
	}

	return unsafe.Slice((*byte)(bp), n)
}

// This is exported via linkname to assembly in syscall (for Plan9) and cgo.
//
//go:linkname gostring
func gostring(p *byte) string {
	l := findnull(p)
	if l == 0 {
		return ""
	}
	s, b := rawstring(l)
	memmove(unsafe.Pointer(&b[0]), unsafe.Pointer(p), uintptr(l))
	if !storesOrdered {
		publicationBarrier()
	}
	return s
}

// internal_syscall_gostring is a version of gostring for internal/syscall/unix.
//
//go:linkname internal_syscall_gostring internal/syscall/unix.gostring
func internal_syscall_gostring(p *byte) string {
	return gostring(p)
}

func gostringn(p *byte, l int) string {
	if l == 0 {
		return ""
	}
	s, b := rawstring(l)
	memmove(unsafe.Pointer(&b[0]), unsafe.Pointer(p), uintptr(l))
	if !storesOrdered {
		publicationBarrier()
	}
	return s
}

// parseByteCount parses a string that represents a count of bytes.
//
// s must match the following regular expression:
//
//	^[0-9]+(([KMGT]i)?B)?$
//
// In other words, an integer byte count with an optional unit
// suffix. Acceptable suffixes include one of
// - KiB, MiB, GiB, TiB which represent binary IEC/ISO 80000 units, or
// - B, which just represents bytes.
//
// Returns an int64 because that's what its callers want and receive,
// but the result is always non-negative.
func parseByteCount(s string) (int64, bool) {
	// The empty string is not valid.
	if s == "" {
		return 0, false
	}
	// Handle the easy non-suffix case.
	last := s[len(s)-1]
	if last >= '0' && last <= '9' {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	// Failing a trailing digit, this must always end in 'B'.
	// Also at this point there must be at least one digit before
	// that B.
	if last != 'B' || len(s) < 2 {
		return 0, false
	}
	// The one before that must always be a digit or 'i'.
	if c := s[len(s)-2]; c >= '0' && c <= '9' {
		// Trivial 'B' suffix.
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	} else if c != 'i' {
		return 0, false
	}
	// Finally, we need at least 4 characters now, for the unit
	// prefix and at least one digit.
	if len(s) < 4 {
		return 0, false
	}
	power := 0
	switch s[len(s)-3] {
	case 'K':
		power = 1
	case 'M':
		power = 2
	case 'G':
		power = 3
	case 'T':
		power = 4
	default:
		// Invalid suffix.
		return 0, false
	}
	m := uint64(1)
	for i := 0; i < power; i++ {
		m *= 1024
	}
	n, err := strconv.ParseInt(s[:len(s)-3], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	un := uint64(n)
	if un > math.MaxUint64/m {
		// Overflow.
		return 0, false
	}
	un *= m
	if un > uint64(math.MaxInt64) {
		// Overflow.
		return 0, false
	}
	return int64(un), true
}

//go:nosplit
func findnull(s *byte) int {
	if s == nil {
		return 0
	}

	// Avoid IndexByteString on Plan 9 because it uses SSE instructions
	// on x86 machines, and those are classified as floating point instructions,
	// which are illegal in a note handler.
	if GOOS == "plan9" {
		p := (*[maxAlloc/2 - 1]byte)(unsafe.Pointer(s))
		l := 0
		for p[l] != 0 {
			l++
		}
		return l
	}

	// pageSize is the unit we scan at a time looking for NULL.
	// It must be the minimum page size for any architecture Go
	// runs on. It's okay (just a minor performance loss) if the
	// actual system page size is larger than this value.
	// For Android, we set the page size to the MTE size, as MTE
	// might be enforced. See issue 59090.
	const pageSize = 4096*(1-goos.IsAndroid) + 16*goos.IsAndroid

	offset := 0
	ptr := unsafe.Pointer(s)
	// IndexByteString uses wide reads, so we need to be careful
	// with page boundaries. Call IndexByteString on
	// [ptr, endOfPage) interval.
	safeLen := int(pageSize - uintptr(ptr)%pageSize)

	for {
		t := *(*string)(unsafe.Pointer(&stringStruct{str: ptr, len: safeLen}))
		// Check one page at a time.
		if i := bytealg.IndexByteString(t, 0); i != -1 {
			return offset + i
		}
		// Move to next page
		ptr = unsafe.Pointer(uintptr(ptr) + uintptr(safeLen))
		offset += safeLen
		safeLen = pageSize
	}
}

func findnullw(s *uint16) int {
	if s == nil {
		return 0
	}
	p := (*[maxAlloc/2/2 - 1]uint16)(unsafe.Pointer(s))
	l := 0
	for p[l] != 0 {
		l++
	}
	return l
}

//go:nosplit
func gostringnocopy(str *byte) string {
	ss := stringStruct{str: unsafe.Pointer(str), len: findnull(str)}
	s := *(*string)(unsafe.Pointer(&ss))
	return s
}

func gostringw(strw *uint16) string {
	var buf [8]byte
	str := (*[maxAlloc/2/2 - 1]uint16)(unsafe.Pointer(strw))
	n1 := 0
	for i := 0; str[i] != 0; i++ {
		n1 += encoderune(buf[:], rune(str[i]))
	}
	s, b := rawstring(n1 + 4)
	n2 := 0
	for i := 0; str[i] != 0; i++ {
		// check for race
		if n2 >= n1 {
			break
		}
		n2 += encoderune(b[n2:], rune(str[i]))
	}
	b[n2] = 0 // for luck
	if !storesOrdered {
		publicationBarrier()
	}
	return s[:n2]
}
