// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"bufio"
	"bytes"
	"io"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"
)

// C's library, in Go: the functions the headers cmd/cc provides declare
// (cmd/cc's headers.go), with the Go types of their C types.

// CString allocates s as a C string.
func CString(s string) *Void {
	p := Malloc(uint64(len(s) + 1))
	copy(slice(unsafe.Pointer(p), uintptr(len(s))), s)
	return p
}

// GoString reads the C string at p, which ends in what p points into.
func GoString(p *Void) string { return string(StringAt(p)) }

// StringView is GoString without the copy: the string is C's own bytes,
// for a caller that reads it and does not keep it, as printf reads its
// format. (A program that changes the bytes meanwhile changes what is
// read, and nothing else: the length is the one that was found.)
func StringView(p *Void) string {
	b := StringAt(p)
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// string.h

// Memcmp is memcmp: of bytes that are in what a and b point into, as
// far as it reads them, which is to the first that differ.
func Memcmp(a, b *Void, n uint64) int32 {
	if n == 0 {
		return 0
	}
	x, y := BytesFrom(a), BytesFrom(b)
	k := min(n, uint64(len(x)), uint64(len(y)))
	x, y = x[:k], y[:k]
	if !bytes.Equal(x, y) {
		for i := range x {
			if x[i] != y[i] {
				return int32(x[i]) - int32(y[i])
			}
		}
	}
	if k < n {
		outside(a, uintptr(min(n, uint64(^uintptr(0)))), uintptr(k), true)
	}
	return 0
}

// stdlib.h

// Calloc is calloc: NULL if n objects of size bytes are more than an
// object can be.
func Calloc(n, size uint64) *Void {
	if size != 0 && n > uint64(^uintptr(0)>>1)/size {
		SetErrno(ENOMEM)
		return nil
	}
	return Malloc(n * size)
}

// Realloc is realloc: untyped memory, with what p's object holds. p is
// NULL or what malloc returned (checkRealloc).
func Realloc(p *Void, n uint64) *Void { return ReallocRaw(p, n) }

// Abort is abort: it calls SIGABRT's handler, if it has one, then ends
// the program.
func Abort() {
	SigMu.Lock()
	h := SigActs[SIGABRT].Handler
	SigMu.Unlock()
	if a := Addr(h); a != SigDfl && a != SigIgn {
		SigSelf().Run(SIGABRT, SigInfo{Code: SI_TKILL})
	}
	abort()
}

func abort() {
	Fflush((*Void)(nil))
	panic(&Fault{"abort", 0})
}

// Exit is exit: it calls the functions atexit registered, flushes C's
// streams and ends the program.
func Exit(status int32) {
	runAtexit()
	Fflush((*Void)(nil))
	os.Exit(int(status))
}

// cmpFunc is the Go function of a C comparison function, as qsort and
// bsearch take.
func CmpFunc(cmp *Void) func(*Void, *Void) int32 { return FuncOf[func(*Void, *Void) int32](cmp) }

var (
	AtexitMu sync.Mutex
	AtexitFn []func() // adapted when registered: calling them needs no reflection
)

// runAtexit calls the functions atexit registered, the last first.
func runAtexit() {
	for {
		AtexitMu.Lock()
		if len(AtexitFn) == 0 {
			AtexitMu.Unlock()
			return
		}
		f := AtexitFn[len(AtexitFn)-1]
		AtexitFn = AtexitFn[:len(AtexitFn)-1]
		AtexitMu.Unlock()
		f()
	}
}

// strtoParse parses an integer as strtol does: the value's magnitude,
// whether it is negative, and how many bytes it took (0: none).
func StrtoParse(s string, base int) (v uint64, neg, overflow bool, n int) {
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

func SetEnd(end, s *Void, n int) {
	if Addr(end) != 0 {
		StorePtr(end, Add(s, n))
	}
}

// errno.h

// errno is each thread's own: a C int in the goroutine's gTable, which C
// reads and writes through the pointer ErrnoLoc returns. It is made when
// the thread first sets errno or takes its address: until then it is 0,
// as a new thread's is.

// ErrnoLoc returns the pointer to the calling thread's errno
// (__cc_errno).
func ErrnoLoc() *Void {
	tb := GSelf()
	if tb.errnoP == nil {
		tb.errno = New[int32]()
		tb.errnoP = (*int32)(Check(tb.errno, 4))
	}
	return tb.errno
}

// Errno returns errno.
func Errno() int32 {
	tb := (*gTable)(*gTableSlot())
	if tb == nil || tb.errnoP == nil {
		return 0
	}
	return *tb.errnoP
}

// SetErrno sets errno.
func SetErrno(e int32) {
	tb := (*gTable)(*gTableSlot())
	if tb == nil || tb.errnoP == nil {
		Store32(ErrnoLoc(), uint32(e))
		return
	}
	*tb.errnoP = e
}

// stdio.h

// A File is C's FILE. It is reading while r is not nil: its writes are
// flushed first, and a write after reads moves the file back to where C
// read up to.
type File struct {
	Fd     int32 // the file descriptor, whose file it reads and writes
	W      *bufio.Writer
	R      *bufio.Reader
	Unget  []byte // what ungetc pushed back, the next last
	Eof    bool
	Err    bool
	Unbuf  bool
	Line   bool // line buffered: a write with a newline in it is flushed
	closed bool
	Rd, Wr bool      // the mode allows reading, writing
	Mem    MemStream // fmemopen's and open_memstream's memory, which it reads and writes in place of fd's file
}

var (
	stdioOnce sync.Once
	stdio     [3]*Void
	Files     []*File
	FilesMu   sync.Mutex
)

// FileOf returns the stream that p, a FILE *, points to: p is what
// NewFile or NewStream returned (a Handle), and not memory that C
// filled in to look like a stream.
func FileOf(p *Void) *File {
	f, ok := HandleOf[*File](p)
	if !ok {
		RaiseFault("not a FILE", Addr(p))
	}
	if f.closed {
		RaiseFault("use of a closed FILE", Addr(p))
	}
	return f
}

// newFile returns a stream of the file descriptor fd.
func NewFile(fd int32, rd, wr bool) *Void {
	fl := &File{Fd: fd, Rd: rd, Wr: wr}
	fl.W = bufio.NewWriter(FdIO{fl})
	return NewStream(fl)
}

// NewStream returns the FILE * for C of the stream fl, which is flushed
// with the others from here on.
func NewStream(fl *File) *Void {
	flushOnce.Do(func() { setFlush(flushAtEnd) })
	FilesMu.Lock()
	Files = append(Files, fl)
	FilesMu.Unlock()
	return NewHandle(fl)
}

// Stdio returns stdin (0), stdout (1) or stderr (2). stdout is line
// buffered if it is a character device, as a terminal is (and a page's
// output with GOOS=js): what C prints is then in its place among what
// Go prints.
func Stdio(n int32) *Void {
	stdioOnce.Do(func() {
		stdio[0] = NewFile(0, true, false)
		stdio[1] = NewFile(1, false, true)
		stdio[2] = NewFile(2, false, true)
		FileOf(stdio[2]).Unbuf = true
		if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&fs.ModeCharDevice != 0 {
			FileOf(stdio[1]).Line = true
		}
	})
	return stdio[n]
}

var flushOnce sync.Once

// setFlush gives the runtime the function that flushes C's streams,
// which it calls as the program ends: when main returns, in os.Exit,
// and where the program stops for a load or a store that is not
// allowed. So what C printed is not lost with its buffers.
//
//go:linkname setFlush runtime.c23SetFlush
func setFlush(f func())

// flushAtEnd flushes every stream as the program ends. A program that
// ends in Fflush has the lock: its streams stay as they are.
func flushAtEnd() {
	if !FilesMu.TryLock() {
		return
	}
	for _, f := range Files {
		f.Flush()
	}
	FilesMu.Unlock()
}

// unread ends reading: it moves the file back over what was read ahead.
func (f *File) unread() {
	if f.R == nil {
		return
	}
	if n := f.R.Buffered() + len(f.Unget); n > 0 {
		f.Seek(int64(-n), io.SeekCurrent)
	}
	f.R, f.Unget = nil, nil
}

// badf fails an operation the stream's mode does not allow, as glibc
// does: it sets the error indicator and errno to EBADF.
func (f *File) badf() {
	f.Err = true
	SetErrno(EBADF)
}

func (f *File) Write(b []byte) bool {
	if !f.Wr {
		f.badf()
		return false
	}
	f.unread()
	_, err := f.W.Write(b)
	if err == nil && (f.Unbuf || f.Line && bytes.IndexByte(b, '\n') >= 0) {
		err = f.W.Flush()
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
	f.Err = true
	SetErrnoOf(err)
	f.W.Reset(FdIO{f})
}

func (f *File) Flush() int32 {
	if err := f.W.Flush(); err != nil {
		f.fail(err)
		return -1
	}
	return 0
}

// Fflush flushes a stream, or every stream if p is null.
func Fflush(p *Void) int32 {
	if p == nil {
		FilesMu.Lock()
		defer FilesMu.Unlock()
		r := int32(0)
		for _, f := range Files {
			r |= f.Flush()
		}
		return r
	}
	return FileOf(p).Flush()
}

// readable reports whether the stream's mode allows reading, and fails
// the stream if not.
func (f *File) Readable() bool {
	if !f.Rd {
		f.badf()
	}
	return f.Rd
}

func (f *File) Reader() *bufio.Reader {
	if f.R == nil {
		if f.Fd == 0 {
			Fflush((*Void)(nil))
		} else {
			f.Flush()
		}
		f.R = bufio.NewReader(FdIO{f})
	}
	return f.R
}

func (f *File) ReadErr(err error) {
	if err == io.EOF {
		f.Eof = true
	} else {
		f.Err = true
	}
}

func (f *File) Getc() int32 {
	if n := len(f.Unget); n > 0 {
		c := f.Unget[n-1]
		f.Unget = f.Unget[:n-1]
		return int32(c)
	}
	if !f.Readable() {
		return -1
	}
	c, err := f.Reader().ReadByte()
	if err != nil {
		f.ReadErr(err)
		return -1
	}
	return int32(c)
}

// close marks the stream closed.
func (f *File) Close() {
	f.closed = true
	FilesMu.Lock()
	for i, g := range Files {
		if g == f {
			Files = append(Files[:i], Files[i+1:]...)
			break
		}
	}
	FilesMu.Unlock()
}

// tmpnamMu guards tmpnamBuf, tmpnam's array for a null s.
var ()

// formatC formats as C's printf.
// widthModifier parses C23's length modifiers wN and wfN (intN_t,
// int_fastN_t) at the start of f: the size of the integer, in bytes, and
// how long the modifier is.
func WidthModifier(f string) (size, n int, ok bool) {
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
		size = int(LongSize) // int_fast16_t and int_fast32_t are long (int where long has 32 bits)
	}
	return size, j, true
}

func FormatC(f string, ap *VaList) []byte { return AppendFormatC(nil, f, ap) }

// AppendFormatC is FormatC that appends to out: printf formats into a
// buffer of its frame.
func AppendFormatC(out []byte, f string, ap *VaList) []byte {
	var fbuf [72]byte // where a number's digits are written
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
			width = int(int32(ToInt64(ap.Next())))
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
				prec = int(int32(ToInt64(ap.Next())))
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
					size = int(LongSize) // long, size_t, ptrdiff_t; long long is "ll"
				}
			}
			i++
		}
		if n, k, ok := WidthModifier(f[i:]); ok {
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
				body = strconv.AppendUint(fbuf[:0], uint64(-v), 10)
			} else {
				body = strconv.AppendUint(fbuf[:0], uint64(v), 10)
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
			body = strconv.AppendUint(fbuf[:0], v, base)
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
				body = utf8.AppendRune(nil, rune(uint32(ToInt64(ap.Next()))))
				break
			}
			body = []byte{byte(ToInt64(ap.Next()))}
		case 's':
			p, _ := ap.Next().(*Void)
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
				// The string's own bytes, which are copied to
				// out and not kept.
				body = StringAt(p)
				s = ""
			}
			if s != "" {
				body = []byte(s)
			}
		case 'p':
			p, _ := ap.Next().(*Void)
			if Addr(p) == 0 {
				body = []byte("(nil)")
			} else {
				body = strconv.AppendUint([]byte("0x"), uint64(Addr(p)), 16)
			}
		case 'f', 'F', 'e', 'E', 'g', 'G', 'a', 'A':
			a := ap.Next()
			v, ok := a.(float64)
			if !ok {
				RaiseFault("printf: a non-double argument for a floating conversion", 0)
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
			body = appendFloat(fbuf[:0], v, conv, prec, alt, sign == "-")
			if math.IsInf(v, 0) || math.IsNaN(v) {
				zero = false
			}
		case 'n':
			p, _ := ap.Next().(*Void)
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
	v := ToInt64(ap.Next())
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
	v := uint64(ToInt64(ap.Next()))
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

func ToInt64(a any) int64 {
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
	case *Void:
		return int64(Addr(v))
	}
	RaiseFault("printf: a non-integer argument for an integer conversion", 0)
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

// appendFloat is formatFloat, appended to dst: the conversions that most
// are, of a number that is finite with the rounding to nearest that a
// thread has unless it changed it, are made once, by strconv, with
// nothing allocated if dst has the room. %g is C's: with P the
// precision (1 for 0) and X the exponent that %e would write with P-1
// digits after the point, it is %f with P-1-X digits if P > X >= -4, and
// %e otherwise, without the zeros that end the fraction unless the flag
// # is given. The digits of the two are the same P digits, rounded at
// the same place, so they are converted once, as %e, and the point is
// put where %f has it.
func appendFloat(dst []byte, v float64, conv byte, prec int, alt, neg bool) []byte {
	lower := conv | 0x20
	if math.IsInf(v, 0) || math.IsNaN(v) || lower == 'a' || CurRound() != FeToNearest {
		return append(dst, formatFloat(v, conv, prec, alt, neg)...)
	}
	if prec < 0 {
		prec = 6
	}
	start := len(dst)
	switch lower {
	case 'f':
		dst = strconv.AppendFloat(dst, v, 'f', prec, 64)
		if alt && prec == 0 {
			dst = append(dst, '.')
		}
	case 'e':
		dst = strconv.AppendFloat(dst, v, 'e', prec, 64)
		if alt && prec == 0 {
			return append(dst[:start], formatFloat(v, conv, prec, alt, neg)...)
		}
	case 'g':
		if alt {
			// (Rare: as it was.)
			return append(dst, formatFloat(v, conv, prec, alt, neg)...)
		}
		p := prec
		if p == 0 {
			p = 1
		}
		var ebuf [40]byte
		var e []byte
		if p <= 17 {
			e = strconv.AppendFloat(ebuf[:0], v, 'e', p-1, 64)
		} else {
			e = strconv.AppendFloat(nil, v, 'e', p-1, 64)
		}
		// e is d[.ddd]e±xx: its digits, and its exponent.
		i := bytes.IndexByte(e, 'e')
		x := 0
		for _, c := range e[i+2:] {
			x = x*10 + int(c-'0')
		}
		if e[i+1] == '-' {
			x = -x
		}
		if v == 0 {
			x = 0
		}
		mant := e[:i] // d or d.ddd
		if p > x && x >= -4 {
			// As %f: the same digits, with the point after x+1 of
			// them, or after a 0 and -x-1 more of them.
			digit := func(k int) byte { // the k'th of the p digits
				if k == 0 {
					return mant[0]
				}
				return mant[k+1]
			}
			if x >= 0 {
				for k := 0; k <= x; k++ {
					dst = append(dst, digit(k))
				}
				if x+1 < p || alt {
					dst = append(dst, '.')
				}
				for k := x + 1; k < p; k++ {
					dst = append(dst, digit(k))
				}
			} else {
				dst = append(dst, '0', '.')
				for k := -1; k > x; k-- {
					dst = append(dst, '0')
				}
				for k := 0; k < p; k++ {
					dst = append(dst, digit(k))
				}
			}
			if !alt {
				// Without the zeros that end the fraction, and
				// without the point if nothing is left after it.
				if bytes.IndexByte(dst[start:], '.') >= 0 {
					for dst[len(dst)-1] == '0' {
						dst = dst[:len(dst)-1]
					}
					if dst[len(dst)-1] == '.' {
						dst = dst[:len(dst)-1]
					}
				}
			}
		} else {
			if !alt && len(mant) > 1 {
				for mant[len(mant)-1] == '0' {
					mant = mant[:len(mant)-1]
				}
				if mant[len(mant)-1] == '.' {
					mant = mant[:len(mant)-1]
				}
			} else if alt && len(mant) == 1 {
				return append(dst[:start], formatFloat(v, conv, prec, alt, neg)...)
			}
			dst = append(dst, mant...)
			dst = append(dst, e[i:]...)
		}
	default:
		return append(dst, formatFloat(v, conv, prec, alt, neg)...)
	}
	if conv < 'a' {
		for k := start; k < len(dst); k++ {
			if c := dst[k]; c >= 'a' && c <= 'z' {
				dst[k] = c - 0x20
			}
		}
	}
	return dst
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

// A MemStream is the memory of a stream that fmemopen or open_memstream
// made (internal/c23/stdio's MemFile): an interface, so that only
// programs that make one link it.
type MemStream interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Seek(off int64, whence int) (int64, error)
	Close()
}
