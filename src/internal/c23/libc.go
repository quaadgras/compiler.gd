// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"bufio"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"
)

// C's library, in Go: the functions the headers cmd/cc provides declare
// (cmd/cc's headers.go), with the Go types of their C types.

// CString allocates s as a C string.
func CString(s string) Ptr {
	p := Malloc(uint64(len(s) + 1))
	copy(unsafe.Slice((*byte)(p.obj), len(s)), s)
	return p
}

// GoString reads the C string at p.
func GoString(p Ptr) string {
	var b []byte
	for {
		c := Load8(p)
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
		p = Add(p, 1)
	}
}

// bytesAt reads n bytes at p.
func bytesAt(p Ptr, n uintptr) []byte {
	if n == 0 {
		return nil
	}
	a := check(p, n)
	return unsafe.Slice((*byte)(a), n)
}

// string.h

func Strlen(p Ptr) uint64 {
	n := uint64(0)
	for Load8(Add(p, int(n))) != 0 {
		n++
	}
	return n
}

func Strcmp(a, b Ptr) int32 {
	for {
		x, y := Load8(a), Load8(b)
		if x != y || x == 0 {
			return int32(x) - int32(y)
		}
		a, b = Add(a, 1), Add(b, 1)
	}
}

func Strncmp(a, b Ptr, n uint64) int32 {
	for ; n > 0; n-- {
		x, y := Load8(a), Load8(b)
		if x != y || x == 0 {
			return int32(x) - int32(y)
		}
		a, b = Add(a, 1), Add(b, 1)
	}
	return 0
}

func Strcpy(d, s Ptr) Ptr {
	r := d
	for {
		c := Load8(s)
		Store8(d, c)
		if c == 0 {
			return r
		}
		d, s = Add(d, 1), Add(s, 1)
	}
}

func Strncpy(d, s Ptr, n uint64) Ptr {
	r := d
	for ; n > 0; n-- {
		c := Load8(s)
		Store8(d, c)
		d = Add(d, 1)
		if c != 0 {
			s = Add(s, 1)
		}
	}
	return r
}

func Strcat(d, s Ptr) Ptr {
	Strcpy(Add(d, int(Strlen(d))), s)
	return d
}

func Strncat(d, s Ptr, n uint64) Ptr {
	e := Add(d, int(Strlen(d)))
	for ; n > 0; n-- {
		c := Load8(s)
		if c == 0 {
			break
		}
		Store8(e, c)
		e, s = Add(e, 1), Add(s, 1)
	}
	Store8(e, 0)
	return d
}

func Strchr(s Ptr, c int32) Ptr {
	for {
		x := Load8(s)
		if x == uint8(c) {
			return s
		}
		if x == 0 {
			return Ptr{}
		}
		s = Add(s, 1)
	}
}

func Strrchr(s Ptr, c int32) Ptr {
	var r Ptr
	for {
		x := Load8(s)
		if x == uint8(c) {
			r = s
		}
		if x == 0 {
			return r
		}
		s = Add(s, 1)
	}
}

func Strstr(s, sub Ptr) Ptr {
	h, n := GoString(s), GoString(sub)
	i := strings.Index(h, n)
	if i < 0 {
		return Ptr{}
	}
	return Add(s, i)
}

func Strspn(s, set Ptr) uint64 {
	h, cs := GoString(s), GoString(set)
	n := 0
	for n < len(h) && strings.IndexByte(cs, h[n]) >= 0 {
		n++
	}
	return uint64(n)
}

func Strcspn(s, set Ptr) uint64 {
	h, cs := GoString(s), GoString(set)
	n := 0
	for n < len(h) && strings.IndexByte(cs, h[n]) < 0 {
		n++
	}
	return uint64(n)
}

func Strdup(s Ptr) Ptr { return CString(GoString(s)) }

func Strndup(s Ptr, n uint64) Ptr {
	g := GoString(s)
	if uint64(len(g)) > n {
		g = g[:n]
	}
	return CString(g)
}

func Strnlen(p Ptr, max uint64) uint64 {
	n := uint64(0)
	for n < max && Load8(Add(p, int(n))) != 0 {
		n++
	}
	return n
}

func Strpbrk(s, set Ptr) Ptr {
	n := Strcspn(s, set)
	if Load8(Add(s, int(n))) == 0 {
		return Ptr{}
	}
	return Add(s, int(n))
}

// Strcoll is strcoll in the C locale: strcmp.
func Strcoll(a, b Ptr) int32 { return Strcmp(a, b) }

// MemsetExplicit is memset_explicit: memset, which the Go compiler does
// not remove.
func MemsetExplicit(p Ptr, c int32, n uint64) Ptr { return Memset(p, c, n) }

var strtokSave = New[unsafe.Pointer]()

// Strtok is strtok, whose state is its last string's rest.
func Strtok(s, sep Ptr) Ptr { return StrtokR(s, sep, strtokSave) }

// StrtokR is strtok_r.
func StrtokR(s, sep, save Ptr) Ptr {
	if Addr(s) == 0 {
		s = LoadPtr(save)
		if Addr(s) == 0 {
			return Ptr{}
		}
	}
	s = Add(s, int(Strspn(s, sep)))
	if Load8(s) == 0 {
		StorePtr(save, Ptr{})
		return Ptr{}
	}
	e := Add(s, int(Strcspn(s, sep)))
	if Load8(e) == 0 {
		StorePtr(save, Ptr{})
	} else {
		Store8(e, 0)
		StorePtr(save, Add(e, 1))
	}
	return s
}

// Memcpy is memcpy and memmove.
func Memcpy(dst, src Ptr, n uint64) Ptr {
	Memmove(dst, src, uintptr(n))
	return dst
}

func Memccpy(dst, src Ptr, c int32, n uint64) Ptr {
	for i := uint64(0); i < n; i++ {
		x := Load8(Add(src, int(i)))
		Store8(Add(dst, int(i)), x)
		if x == uint8(c) {
			return Add(dst, int(i+1))
		}
	}
	return Ptr{}
}

func Memcmp(a, b Ptr, n uint64) int32 {
	for i := uint64(0); i < n; i++ {
		x, y := Load8(Add(a, int(i))), Load8(Add(b, int(i)))
		if x != y {
			return int32(x) - int32(y)
		}
	}
	return 0
}

func Memchr(s Ptr, c int32, n uint64) Ptr {
	for i := uint64(0); i < n; i++ {
		if Load8(Add(s, int(i))) == uint8(c) {
			return Add(s, int(i))
		}
	}
	return Ptr{}
}

// stdlib.h

func Calloc(n, size uint64) Ptr { return Malloc(n * size) }

// Realloc is realloc: untyped memory, with what p's object holds. p is
// NULL or what malloc returned (checkRealloc).
func Realloc(p Ptr, n uint64) Ptr {
	checkRealloc(p)
	q := Malloc(n)
	if p.obj != nil {
		Memmove(q, p, min(uintptr(n), p.size))
	}
	return q
}

// Abort is abort: it calls SIGABRT's handler, if it has one, then ends
// the program.
func Abort() {
	sigMu.Lock()
	h := sigHandler[SIGABRT]
	sigMu.Unlock()
	if a := Addr(h); a != sigDfl && a != sigIgn {
		FuncOf[func(int32)](h)(SIGABRT)
	}
	abort()
}

func abort() {
	Fflush(Ptr{})
	panic(&Fault{"abort", 0})
}

// Exit is exit: it calls the functions atexit registered, flushes C's
// streams and ends the program.
func Exit(status int32) {
	runAtexit()
	Fflush(Ptr{})
	os.Exit(int(status))
}

// ExitNow is _Exit: exit without the functions atexit registered.
func ExitNow(status int32) {
	Fflush(Ptr{})
	os.Exit(int(status))
}

// cmpFunc is the Go function of a C comparison function, as qsort and
// bsearch take.
func cmpFunc(cmp Ptr) func(Ptr, Ptr) int32 { return FuncOf[func(Ptr, Ptr) int32](cmp) }

// qsorter sorts n elements of size bytes at base: it swaps them through
// a temporary object, so that the pointers they hold keep their
// capabilities.
type qsorter struct {
	base Ptr
	size int
	tmp  Ptr
	cmp  func(Ptr, Ptr) int32
}

func (q *qsorter) at(i int) Ptr       { return Add(q.base, i*q.size) }
func (q *qsorter) less(i, j int) bool { return q.cmp(q.at(i), q.at(j)) < 0 }
func (q *qsorter) swap(i, j int) {
	sz := uintptr(q.size)
	Memmove(q.tmp, q.at(i), sz)
	Memmove(q.at(i), q.at(j), sz)
	Memmove(q.at(j), q.tmp, sz)
}

// siftDown restores the heap of the first n elements from element i.
func (q *qsorter) siftDown(i, n int) {
	for {
		c := 2*i + 1
		if c >= n {
			return
		}
		if c+1 < n && q.less(c, c+1) {
			c++
		}
		if !q.less(i, c) {
			return
		}
		q.swap(i, c)
		i = c
	}
}

// Qsort is qsort: a heapsort, in place and in O(n log n).
func Qsort(base Ptr, n, size uint64, cmp Ptr) {
	if n < 2 || size == 0 {
		return
	}
	q := &qsorter{base: base, size: int(size), tmp: Malloc(size), cmp: cmpFunc(cmp)}
	k := int(n)
	for i := k/2 - 1; i >= 0; i-- {
		q.siftDown(i, k)
	}
	for end := k - 1; end > 0; end-- {
		q.swap(0, end)
		q.siftDown(0, end)
	}
}

func Bsearch(key, base Ptr, n, size uint64, cmp Ptr) Ptr {
	f := cmpFunc(cmp)
	lo, hi := uint64(0), n
	for lo < hi {
		m := lo + (hi-lo)/2
		e := Add(base, int(m*size))
		switch c := f(key, e); {
		case c == 0:
			return e
		case c < 0:
			hi = m
		default:
			lo = m + 1
		}
	}
	return Ptr{}
}

var (
	atexitMu sync.Mutex
	atexitFn []func() // adapted when registered: calling them needs no reflection
)

// Atexit is atexit: Exit calls f, after the functions registered later.
func Atexit(f Ptr) int32 {
	fn := FuncOf[func()](f)
	atexitMu.Lock()
	atexitFn = append(atexitFn, fn)
	atexitMu.Unlock()
	return 0
}

// runAtexit calls the functions atexit registered, the last first.
func runAtexit() {
	for {
		atexitMu.Lock()
		if len(atexitFn) == 0 {
			atexitMu.Unlock()
			return
		}
		f := atexitFn[len(atexitFn)-1]
		atexitFn = atexitFn[:len(atexitFn)-1]
		atexitMu.Unlock()
		f()
	}
}

func Abs(j int32) int32 {
	if j < 0 {
		return -j
	}
	return j
}

func Labs(j int64) int64 {
	if j < 0 {
		return -j
	}
	return j
}

// Div is div: it stores the div_t {quot, rem} of n/d at sret.
func Div(sret Ptr, n, d int32) {
	Store32(sret, uint32(n/d))
	Store32(Add(sret, 4), uint32(n%d))
}

// Ldiv is ldiv, lldiv and imaxdiv: it stores the ldiv_t {quot, rem} of
// n/d at sret.
func Ldiv(sret Ptr, n, d int64) {
	Store64(sret, uint64(n/d))
	Store64(Add(sret, 8), uint64(n%d))
}

func Atoi(s Ptr) int32   { return int32(Strtol(s, Ptr{}, 10)) }
func Atol(s Ptr) int64   { return Strtol(s, Ptr{}, 10) }
func Atof(s Ptr) float64 { return Strtod(s, Ptr{}) }

// strtoParse parses an integer as strtol does: the value's magnitude,
// whether it is negative, and how many bytes it took (0: none).
func strtoParse(s string, base int) (v uint64, neg, overflow bool, n int) {
	i := 0
	for i < len(s) && strings.IndexByte(" \t\n\v\f\r", s[i]) >= 0 {
		i++
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	switch {
	case (base == 0 || base == 16) && i+2 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') && digitVal(s[i+2]) < 16:
		i += 2
		base = 16
	case (base == 0 || base == 2) && i+2 < len(s) && s[i] == '0' && (s[i+1] == 'b' || s[i+1] == 'B') && digitVal(s[i+2]) < 2:
		i += 2
		base = 2
	case base == 0 && i < len(s) && s[i] == '0':
		base = 8
	case base == 0:
		base = 10
	}
	start := i
	for i < len(s) {
		d := digitVal(s[i])
		if d >= base {
			break
		}
		if v > (math.MaxUint64-uint64(d))/uint64(base) {
			overflow = true
		}
		v = v*uint64(base) + uint64(d)
		i++
	}
	if i == start {
		return 0, false, false, 0
	}
	return v, neg, overflow, i
}

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

func setEnd(end, s Ptr, n int) {
	if Addr(end) != 0 {
		StorePtr(end, Add(s, n))
	}
}

func Strtol(s, end Ptr, base int32) int64 {
	v, neg, of, n := strtoParse(GoString(s), int(base))
	setEnd(end, s, n)
	switch {
	case neg && (of || v > 1<<63):
		SetErrno(ERANGE)
		return math.MinInt64
	case !neg && (of || v > math.MaxInt64):
		SetErrno(ERANGE)
		return math.MaxInt64
	case neg:
		return -int64(v)
	}
	return int64(v)
}

func Strtoul(s, end Ptr, base int32) uint64 {
	v, neg, of, n := strtoParse(GoString(s), int(base))
	setEnd(end, s, n)
	if of {
		SetErrno(ERANGE)
		return math.MaxUint64
	}
	if neg {
		return -v
	}
	return v
}

func Strtod(s, end Ptr) float64  { return strtod(s, end, 64) }
func Strtof(s, end Ptr) float32  { return float32(strtod(s, end, 32)) }
func Strtold(s, end Ptr) float64 { return strtod(s, end, 64) }

// strtod is strtod of a float of the given bits.
func strtod(s, end Ptr, bits int) float64 {
	str := GoString(s)
	i := 0
	for i < len(str) && strings.IndexByte(" \t\n\v\f\r", str[i]) >= 0 {
		i++
	}
	// The longest prefix that parses.
	for j := len(str); j > i; j-- {
		t := str[i:j]
		if strings.Contains(t, "_") {
			continue
		}
		if k := strings.IndexAny(t, "xX"); k >= 0 && !strings.ContainsAny(t, "pP") {
			// C's hexadecimal floats need no exponent; Go's do.
			t += "p0"
		}
		f, err := parseFloat(t, bits)
		if err == nil || err.(*strconv.NumError).Err == strconv.ErrRange {
			if err != nil {
				SetErrno(ERANGE)
			}
			setEnd(end, s, j)
			return f
		}
	}
	setEnd(end, s, 0)
	return 0
}

var randState uint64 = 1

func Rand() int32 {
	randState = randState*6364136223846793005 + 1442695040888963407
	return int32(randState >> 33)
}

func Srand(seed uint32) { randState = uint64(seed) }

func Getenv(name Ptr) Ptr {
	v, ok := os.LookupEnv(GoString(name))
	if !ok {
		return Ptr{}
	}
	return CString(v)
}

// errno.h

// errnoObj is errno: a C int, which C reads and writes through the
// pointer ErrnoLoc returns.
var errnoObj = New[int32]()

// ErrnoLoc returns the pointer to errno (__cc_errno).
func ErrnoLoc() Ptr { return errnoObj }

// Errno returns errno.
func Errno() int32 { return int32(Load32(errnoObj)) }

// SetErrno sets errno.
func SetErrno(e int32) { Store32(errnoObj, uint32(e)) }

// stdio.h

// A File is C's FILE. It is reading while r is not nil: its writes are
// flushed first, and a write after reads moves the file back to where C
// read up to.
type File struct {
	fd     int32 // the file descriptor, whose file it reads and writes
	w      *bufio.Writer
	r      *bufio.Reader
	unget  []byte // what ungetc pushed back, the next last
	eof    bool
	err    bool
	unbuf  bool
	closed bool
	rd, wr bool     // the mode allows reading, writing
	mem    *memFile // fmemopen's and open_memstream's memory, which it reads and writes in place of fd's file
}

var (
	stdioOnce sync.Once
	stdio     [3]Ptr
	files     []*File
	filesMu   sync.Mutex
	fileInfo  = tinfoOf[File]()
)

func fileOf(p Ptr) *File {
	if p.obj == nil || p.info() != fileInfo || p.off != 0 {
		fault("not a FILE", Addr(p))
	}
	f := (*File)(p.obj)
	if f.closed {
		fault("use of a closed FILE", Addr(p))
	}
	return f
}

// newFile returns a stream of the file descriptor fd.
func newFile(fd int32, rd, wr bool) Ptr {
	p := New[File]()
	fl := (*File)(p.obj)
	fl.fd = fd
	fl.w = bufio.NewWriter(fdIO{fl})
	fl.rd, fl.wr = rd, wr
	filesMu.Lock()
	files = append(files, fl)
	filesMu.Unlock()
	return p
}

// Stdio returns stdin (0), stdout (1) or stderr (2).
func Stdio(n int32) Ptr {
	stdioOnce.Do(func() {
		stdio[0] = newFile(0, true, false)
		stdio[1] = newFile(1, false, true)
		stdio[2] = newFile(2, false, true)
		(*File)(stdio[2].obj).unbuf = true
	})
	return stdio[n]
}

// unread ends reading: it moves the file back over what was read ahead.
func (f *File) unread() {
	if f.r == nil {
		return
	}
	if n := f.r.Buffered() + len(f.unget); n > 0 {
		f.seek(int64(-n), io.SeekCurrent)
	}
	f.r, f.unget = nil, nil
}

// badf fails an operation the stream's mode does not allow, as glibc
// does: it sets the error indicator and errno to EBADF.
func (f *File) badf() {
	f.err = true
	SetErrno(EBADF)
}

func (f *File) write(b []byte) bool {
	if !f.wr {
		f.badf()
		return false
	}
	f.unread()
	_, err := f.w.Write(b)
	if err == nil && f.unbuf {
		err = f.w.Flush()
	}
	if err != nil {
		f.fail(err)
		return false
	}
	return true
}

// fail records a write's error: it sets the error indicator and errno,
// and drops what was buffered.
func (f *File) fail(err error) {
	f.err = true
	setErrnoOf(err)
	f.w.Reset(fdIO{f})
}

func (f *File) flush() int32 {
	if err := f.w.Flush(); err != nil {
		f.fail(err)
		return -1
	}
	return 0
}

// Fflush flushes a stream, or every stream if p is null.
func Fflush(p Ptr) int32 {
	if p.obj == nil {
		filesMu.Lock()
		defer filesMu.Unlock()
		r := int32(0)
		for _, f := range files {
			r |= f.flush()
		}
		return r
	}
	return fileOf(p).flush()
}

func out() *File { return fileOf(Stdio(1)) }

func Printf(format Ptr, va ...any) int32 { return Vprintf(format, VaStart(va)) }
func Fprintf(f, format Ptr, va ...any) int32 {
	return Vfprintf(f, format, VaStart(va))
}
func Sprintf(s, format Ptr, va ...any) int32 { return Vsprintf(s, format, VaStart(va)) }
func Snprintf(s Ptr, n uint64, format Ptr, va ...any) int32 {
	return Vsnprintf(s, n, format, VaStart(va))
}

func Vprintf(format, ap Ptr) int32 { return Vfprintf(Stdio(1), format, ap) }

func Vfprintf(f, format, ap Ptr) int32 {
	b := formatC(GoString(format), vaOf(ap))
	if !fileOf(f).write(b) {
		return -1
	}
	return int32(len(b))
}

func Vsprintf(s, format, ap Ptr) int32 {
	b := formatC(GoString(format), vaOf(ap))
	copy(bytesAt(s, uintptr(len(b)+1)), append(b, 0))
	return int32(len(b))
}

func Vsnprintf(s Ptr, n uint64, format, ap Ptr) int32 {
	b := formatC(GoString(format), vaOf(ap))
	if n > 0 {
		k := min(uint64(len(b)), n-1)
		copy(bytesAt(s, uintptr(k+1)), append(b[:k:k], 0))
	}
	return int32(len(b))
}

func Puts(s Ptr) int32 {
	if !out().write(append([]byte(GoString(s)), '\n')) {
		return -1
	}
	return 0
}

func Fputs(s, f Ptr) int32 {
	if !fileOf(f).write([]byte(GoString(s))) {
		return -1
	}
	return 0
}

func Putchar(c int32) int32 { return Fputc(c, Stdio(1)) }

func Fputc(c int32, f Ptr) int32 {
	if !fileOf(f).write([]byte{byte(c)}) {
		return -1
	}
	return int32(uint8(c))
}

func Fwrite(p Ptr, size, n uint64, f Ptr) uint64 {
	if size == 0 || !fileOf(f).write(bytesAt(p, uintptr(size*n))) {
		return 0
	}
	return n
}

// readable reports whether the stream's mode allows reading, and fails
// the stream if not.
func (f *File) readable() bool {
	if !f.rd {
		f.badf()
	}
	return f.rd
}

func (f *File) reader() *bufio.Reader {
	if f.r == nil {
		if f.fd == 0 {
			Fflush(Ptr{})
		} else {
			f.flush()
		}
		f.r = bufio.NewReader(fdIO{f})
	}
	return f.r
}

func (f *File) readErr(err error) {
	if err == io.EOF {
		f.eof = true
	} else {
		f.err = true
	}
}

func (f *File) getc() int32 {
	if n := len(f.unget); n > 0 {
		c := f.unget[n-1]
		f.unget = f.unget[:n-1]
		return int32(c)
	}
	if !f.readable() {
		return -1
	}
	c, err := f.reader().ReadByte()
	if err != nil {
		f.readErr(err)
		return -1
	}
	return int32(c)
}

func Fgetc(f Ptr) int32 { return fileOf(f).getc() }

func Getchar() int32 { return Fgetc(Stdio(0)) }

func Ungetc(c int32, p Ptr) int32 {
	if c == -1 {
		return -1
	}
	f := fileOf(p)
	if !f.readable() {
		return -1
	}
	f.reader()
	f.unget = append(f.unget, byte(c))
	f.eof = false
	return int32(uint8(c))
}

func Fgets(s Ptr, n int32, f Ptr) Ptr {
	if n <= 0 {
		return Ptr{}
	}
	fl := fileOf(f)
	i := int32(0)
	for i < n-1 {
		c := fl.getc()
		if c < 0 {
			if fl.err {
				return Ptr{}
			}
			break
		}
		Store8(Add(s, int(i)), uint8(c))
		i++
		if c == '\n' {
			break
		}
	}
	if i == 0 && n > 1 {
		return Ptr{}
	}
	Store8(Add(s, int(i)), 0)
	return s
}

func Fread(p Ptr, size, n uint64, f Ptr) uint64 {
	if size == 0 || n == 0 {
		return 0
	}
	fl := fileOf(f)
	if !fl.readable() {
		return 0
	}
	b := bytesAt(p, uintptr(size*n))
	k := 0
	for k < len(b) && len(fl.unget) > 0 {
		b[k] = byte(fl.getc())
		k++
	}
	r := fl.reader()
	for k < len(b) {
		m, err := r.Read(b[k:])
		k += m
		if err != nil {
			fl.readErr(err)
			break
		}
	}
	return uint64(k) / size
}

// openMode returns the flags of fopen's mode: r, w or a, then any of +
// (update), b (no effect) and x (the file must not exist).
func openMode(m string) (flag int, rd, wr, ok bool) {
	switch {
	case strings.HasPrefix(m, "r"):
		flag, rd = os.O_RDONLY, true
	case strings.HasPrefix(m, "w"):
		flag, wr = os.O_WRONLY|os.O_CREATE|os.O_TRUNC, true
	case strings.HasPrefix(m, "a"):
		flag, wr = os.O_WRONLY|os.O_CREATE|os.O_APPEND, true
	default:
		return 0, false, false, false
	}
	for _, c := range m[1:] {
		switch c {
		case '+':
			flag = flag&^(os.O_RDONLY|os.O_WRONLY) | os.O_RDWR
			rd, wr = true, true
		case 'x':
			flag |= os.O_EXCL
		case 'b':
		default:
			return 0, false, false, false
		}
	}
	return flag, rd, wr, true
}

// Fopen is fopen.
func Fopen(name, mode Ptr) Ptr {
	flag, rd, wr, ok := openMode(GoString(mode))
	if !ok {
		SetErrno(EINVAL)
		return Ptr{}
	}
	f, err := os.OpenFile(GoString(name), flag, 0o666)
	if err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	return newFile(fdNew(f, cflags(flag)), rd, wr)
}

// Freopen is freopen: it closes the stream's file and opens name in its
// place, or, if name is null, the stream's file again with the new mode.
// The stream is closed if that fails.
func Freopen(name, mode, p Ptr) Ptr {
	f := fileOf(p)
	if f.mem != nil { // a memory stream has no file to reopen
		Fclose(p)
		SetErrno(EBADF)
		return Ptr{}
	}
	f.flush()
	flag, rd, wr, ok := openMode(GoString(mode))
	var path string
	if Addr(name) != 0 {
		path = GoString(name)
	} else if of := f.osFile(); of != nil {
		path = of.Name()
	}
	fdClose(f.fd)
	if !ok {
		SetErrno(EINVAL)
		f.close()
		return Ptr{}
	}
	nf, err := os.OpenFile(path, flag, 0o666)
	if err != nil {
		setErrnoOf(err)
		f.close()
		return Ptr{}
	}
	fdSet(f.fd, nf, cflags(flag))
	f.w.Reset(fdIO{f})
	f.r, f.unget = nil, nil
	f.eof, f.err = false, false
	f.rd, f.wr = rd, wr
	return p
}

// close marks the stream closed.
func (f *File) close() {
	f.closed = true
	filesMu.Lock()
	for i, g := range files {
		if g == f {
			files = append(files[:i], files[i+1:]...)
			break
		}
	}
	filesMu.Unlock()
}

// tmpnamMu guards tmpnamBuf, tmpnam's array for a null s.
var (
	tmpnamMu  sync.Mutex
	tmpnamBuf Ptr
	tmpnamN   uint64
)

// Tmpnam is tmpnam: a name in /tmp of no existing file, at s or, if s is
// null, in an array the next call reuses. Its names are at most
// L_tmpnam (20) bytes long, with the null.
func Tmpnam(s Ptr) Ptr {
	tmpnamMu.Lock()
	defer tmpnamMu.Unlock()
	if Addr(s) == 0 {
		if tmpnamBuf.obj == nil {
			tmpnamBuf = Malloc(20)
		}
		s = tmpnamBuf
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for range 238328 { // TMP_MAX
		tmpnamN++
		x := uint64(os.Getpid())*2654435761 + tmpnamN*0x9e3779b97f4a7c15 + uint64(time.Now().UnixNano())
		name := []byte("/tmp/tmpnam_")
		for range 6 {
			name = append(name, digits[x%62])
			x /= 62
		}
		if _, err := os.Lstat(string(name)); err == nil {
			continue
		}
		copy(bytesAt(s, uintptr(len(name)+1)), append(name, 0))
		return s
	}
	return Ptr{}
}

func Fclose(p Ptr) int32 {
	f := fileOf(p)
	r := f.flush()
	if f.mem != nil {
		f.mem.close()
		f.close()
		return r
	}
	if err := fdClose(f.fd); err != nil {
		setErrnoOf(err)
		r = -1
	}
	f.close()
	return r
}

// Tmpfile is tmpfile: a new file, opened w+, removed when closed (on
// Unix, at once).
func Tmpfile() Ptr {
	f, err := os.CreateTemp("", "tmpf")
	if err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	os.Remove(f.Name())
	return newFile(fdNew(f, O_RDWR), true, true)
}

func Fseek(p Ptr, off int64, whence int32) int32 {
	f := fileOf(p)
	if whence < 0 || whence > 2 {
		SetErrno(EINVAL)
		return -1
	}
	if f.flush() != 0 {
		return -1
	}
	if f.r != nil && whence == io.SeekCurrent {
		off -= int64(f.r.Buffered() + len(f.unget))
	}
	f.r, f.unget = nil, nil
	if _, err := f.seek(off, int(whence)); err != nil {
		setErrnoOf(err)
		return -1
	}
	f.eof = false
	return 0
}

func Ftell(p Ptr) int64 {
	f := fileOf(p)
	pos, err := f.seek(0, io.SeekCurrent)
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	if f.r != nil {
		return pos - int64(f.r.Buffered()+len(f.unget))
	}
	return pos + int64(f.w.Buffered())
}

func Rewind(p Ptr) {
	Fseek(p, 0, io.SeekStart)
	fileOf(p).err = false
}

func Feof(p Ptr) int32   { return B2I(fileOf(p).eof) }
func Ferror(p Ptr) int32 { return B2I(fileOf(p).err) }

func Clearerr(p Ptr) {
	f := fileOf(p)
	f.eof, f.err = false, false
}

// Setvbuf is setvbuf: _IONBF (2) makes the stream unbuffered; line
// buffering is full buffering. C's buffer is not used.
func Setvbuf(p, buf Ptr, mode int32, size uint64) int32 {
	if mode < 0 || mode > 2 {
		return -1
	}
	fileOf(p).unbuf = mode == 2
	return 0
}

func Setbuf(p, buf Ptr) {
	fileOf(p).unbuf = Addr(buf) == 0
}

func Remove(name Ptr) int32 {
	if err := os.Remove(GoString(name)); err != nil {
		setErrnoOf(err)
		return -1
	}
	return 0
}

func Rename(old, new Ptr) int32 {
	if err := os.Rename(GoString(old), GoString(new)); err != nil {
		setErrnoOf(err)
		return -1
	}
	return 0
}

// Perror is perror: s, then the message of errno, on stderr.
func Perror(s Ptr) {
	msg := strerror(Errno())
	if Addr(s) != 0 && Load8(s) != 0 {
		msg = GoString(s) + ": " + msg
	}
	fileOf(Stdio(2)).write([]byte(msg + "\n"))
}

// formatC formats as C's printf.
// widthModifier parses C23's length modifiers wN and wfN (intN_t,
// int_fastN_t) at the start of f: the size of the integer, in bytes, and
// how long the modifier is.
func widthModifier(f string) (size, n int, ok bool) {
	if len(f) < 2 || f[0] != 'w' {
		return 0, 0, false
	}
	k := 1
	fast := f[k] == 'f'
	if fast {
		k++
	}
	j := k
	for j < len(f) && f[j] >= '0' && f[j] <= '9' {
		j++
	}
	switch f[k:j] {
	case "8":
		size = 1
	case "16":
		size = 2
	case "32":
		size = 4
	case "64":
		size = 8
	default:
		return 0, 0, false
	}
	if fast && (size == 2 || size == 4) {
		size = int(longSize) // int_fast16_t and int_fast32_t are long (int where long has 32 bits)
	}
	return size, j, true
}

func formatC(f string, ap *VaList) []byte {
	var out []byte
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			out = append(out, f[i])
			continue
		}
		i++
		var minus, plus, space, alt, zero bool
	flags:
		for ; i < len(f); i++ {
			switch f[i] {
			case '-':
				minus = true
			case '+':
				plus = true
			case ' ':
				space = true
			case '#':
				alt = true
			case '0':
				zero = true
			default:
				break flags
			}
		}
		width, prec := 0, -1
		if i < len(f) && f[i] == '*' {
			width = int(int32(toInt64(ap.next())))
			if width < 0 {
				minus, width = true, -width
			}
			i++
		} else {
			for i < len(f) && f[i] >= '0' && f[i] <= '9' {
				width = width*10 + int(f[i]-'0')
				i++
			}
		}
		if i < len(f) && f[i] == '.' {
			i++
			prec = 0
			if i < len(f) && f[i] == '*' {
				prec = int(int32(toInt64(ap.next())))
				i++
			} else {
				for i < len(f) && f[i] >= '0' && f[i] <= '9' {
					prec = prec*10 + int(f[i]-'0')
					i++
				}
			}
		}
		size := 4 // 1 hh, 2 h, 8 l ll z j t
		nl := 0   // how many l: %ls and %lc are wide
		for i < len(f) && strings.IndexByte("hlLqjzt", f[i]) >= 0 {
			if f[i] == 'l' {
				nl++
			}
			if f[i] == 'h' {
				if size == 2 {
					size = 1
				} else {
					size = 2
				}
			} else {
				size = 8
				if strings.IndexByte("lzt", f[i]) >= 0 && f[i-1] != 'l' && (i+1 >= len(f) || f[i+1] != 'l') {
					size = int(longSize) // long, size_t, ptrdiff_t; long long is "ll"
				}
			}
			i++
		}
		if n, k, ok := widthModifier(f[i:]); ok {
			size, i = n, i+k
		}
		if i >= len(f) {
			break
		}
		conv := f[i]
		var body []byte
		sign, prefix := "", ""
		numeric := false
		switch conv {
		case '%':
			out = append(out, '%')
			continue
		case 'd', 'i':
			v := intArg(ap, size)
			numeric = true
			if v < 0 {
				sign = "-"
				body = strconv.AppendUint(nil, uint64(-v), 10)
			} else {
				body = strconv.AppendUint(nil, uint64(v), 10)
				if plus {
					sign = "+"
				} else if space {
					sign = " "
				}
			}
			body = precDigits(body, prec)
		case 'u', 'x', 'X', 'o', 'b', 'B':
			v := uintArg(ap, size)
			numeric = true
			base := 10
			switch conv {
			case 'x', 'X':
				base = 16
			case 'o':
				base = 8
			case 'b', 'B':
				base = 2
			}
			body = strconv.AppendUint(nil, v, base)
			if conv == 'X' {
				body = []byte(strings.ToUpper(string(body)))
			}
			body = precDigits(body, prec)
			if alt && v != 0 {
				switch conv {
				case 'x':
					prefix = "0x"
				case 'X':
					prefix = "0X"
				case 'b':
					prefix = "0b"
				case 'B':
					prefix = "0B"
				}
			}
			if alt && conv == 'o' && (len(body) == 0 || body[0] != '0') {
				body = append([]byte{'0'}, body...)
			}
		case 'c':
			if nl == 1 {
				body = utf8.AppendRune(nil, rune(uint32(toInt64(ap.next()))))
				break
			}
			body = []byte{byte(toInt64(ap.next()))}
		case 's':
			p, _ := ap.next().(Ptr)
			var s string
			switch {
			case Addr(p) == 0:
				s = "(null)"
			case nl == 1:
				// A wide string, as UTF-8: the precision counts bytes,
				// of whole characters.
				var b []byte
				for k := 0; ; k++ {
					c := Load32(Add(p, 4*k))
					if c == 0 {
						break
					}
					n := len(b)
					b = utf8.AppendRune(b, rune(c))
					if prec >= 0 && len(b) > prec {
						b = b[:n]
						break
					}
				}
				s = string(b)
			case prec >= 0:
				var b []byte
				for k := 0; k < prec; k++ {
					c := Load8(Add(p, k))
					if c == 0 {
						break
					}
					b = append(b, c)
				}
				s = string(b)
			default:
				s = GoString(p)
			}
			body = []byte(s)
		case 'p':
			p, _ := ap.next().(Ptr)
			if Addr(p) == 0 {
				body = []byte("(nil)")
			} else {
				body = strconv.AppendUint([]byte("0x"), uint64(Addr(p)), 16)
			}
		case 'f', 'F', 'e', 'E', 'g', 'G', 'a', 'A':
			a := ap.next()
			v, ok := a.(float64)
			if !ok {
				fault("printf: a non-double argument for a floating conversion", 0)
			}
			numeric = true
			if math.Signbit(v) && !math.IsNaN(v) {
				sign = "-"
				v = -v
			} else if plus {
				sign = "+"
			} else if space {
				sign = " "
			}
			body = formatFloat(v, conv, prec, alt, sign == "-")
			if math.IsInf(v, 0) || math.IsNaN(v) {
				zero = false
			}
		case 'n':
			p, _ := ap.next().(Ptr)
			Store32(p, uint32(len(out)))
			continue
		default:
			out = append(out, '%', conv)
			continue
		}
		n := len(sign) + len(prefix) + len(body)
		pad := width - n
		switch {
		case pad <= 0:
			out = append(append(append(out, sign...), prefix...), body...)
		case minus:
			out = append(append(append(out, sign...), prefix...), body...)
			out = append(out, strings.Repeat(" ", pad)...)
		case zero && numeric && (prec < 0 || strings.IndexByte("fFeEgGaA", conv) >= 0):
			out = append(append(out, sign...), prefix...)
			out = append(out, strings.Repeat("0", pad)...)
			out = append(out, body...)
		default:
			out = append(out, strings.Repeat(" ", pad)...)
			out = append(append(append(out, sign...), prefix...), body...)
		}
	}
	return out
}

func intArg(ap *VaList, size int) int64 {
	v := toInt64(ap.next())
	switch size {
	case 1:
		return int64(int8(v))
	case 2:
		return int64(int16(v))
	case 8:
		return v
	}
	return int64(int32(v))
}

func uintArg(ap *VaList, size int) uint64 {
	v := uint64(toInt64(ap.next()))
	switch size {
	case 1:
		return uint64(uint8(v))
	case 2:
		return uint64(uint16(v))
	case 8:
		return v
	}
	return uint64(uint32(v))
}

func toInt64(a any) int64 {
	switch v := a.(type) {
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		return int64(v)
	case Ptr:
		return int64(Addr(v))
	}
	fault("printf: a non-integer argument for an integer conversion", 0)
	return 0
}

func precDigits(b []byte, prec int) []byte {
	if prec == 0 && len(b) == 1 && b[0] == '0' {
		return nil
	}
	if prec > len(b) {
		return append([]byte(strings.Repeat("0", prec-len(b))), b...)
	}
	return b
}

// formatFloat formats v, not negative, as C's %f, %e, %g or %a do.
// formatFloat formats v, whose sign is neg's (printf writes the sign: v is
// its magnitude), as printf's conversion conv does.
func formatFloat(v float64, conv byte, prec int, alt, neg bool) []byte {
	upper := conv >= 'A' && conv <= 'Z'
	var s string
	switch {
	case math.IsInf(v, 0):
		s = "inf"
	case math.IsNaN(v):
		s = "nan"
	default:
		if prec < 0 && conv != 'a' && conv != 'A' {
			prec = 6
		}
		switch conv | 0x20 {
		case 'f':
			s = fmtFloat(v, 'f', prec, neg)
			if alt && prec == 0 {
				s += "."
			}
		case 'e':
			s = cExp(fmtFloat(v, 'e', prec, neg))
			if alt && prec == 0 {
				s = strings.Replace(s, "e", ".e", 1)
			}
		case 'g':
			p := prec
			if p == 0 {
				p = 1
			}
			x := 0
			if v != 0 {
				e := fmtFloat(v, 'e', p-1, neg)
				x, _ = strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
			}
			if p > x && x >= -4 {
				s = fmtFloat(v, 'f', p-1-x, neg)
			} else {
				s = cExp(fmtFloat(v, 'e', p-1, neg))
			}
			if !alt {
				mant, exp := s, ""
				if i := strings.IndexByte(s, 'e'); i >= 0 {
					mant, exp = s[:i], s[i:]
				}
				if strings.Contains(mant, ".") {
					mant = strings.TrimRight(strings.TrimRight(mant, "0"), ".")
				}
				s = mant + exp
			}
		case 'a':
			s = fmtFloat(v, 'x', prec, neg)
			// Go writes p+01; C writes p+1.
			if i := strings.IndexByte(s, 'p'); i >= 0 {
				e, _ := strconv.Atoi(s[i+1:])
				sign := "+"
				if e < 0 {
					sign = "-"
					e = -e
				}
				s = s[:i+1] + sign + strconv.Itoa(e)
			}
		}
	}
	if upper {
		s = strings.ToUpper(s)
	}
	return []byte(s)
}

// cExp makes Go's exponent C's: at least two digits.
func cExp(s string) string {
	i := strings.IndexByte(s, 'e')
	if i < 0 {
		return s
	}
	exp := s[i+2:]
	if len(exp) < 2 {
		exp = "0" + exp
	}
	return s[:i+2] + exp
}
