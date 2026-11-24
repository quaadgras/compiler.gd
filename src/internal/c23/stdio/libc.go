// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	c23 "internal/c23"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

func out() *c23.File { return c23.FileOf(c23.Stdio(1)) }

func Printf(format c23.Ptr, va ...any) int32 { return Vprintf(format, c23.VaStart(va)) }

func Fprintf(f, format c23.Ptr, va ...any) int32 {
	return Vfprintf(f, format, c23.VaStart(va))
}

func Sprintf(s, format c23.Ptr, va ...any) int32 { return Vsprintf(s, format, c23.VaStart(va)) }

func Snprintf(s c23.Ptr, n uint64, format c23.Ptr, va ...any) int32 {
	return Vsnprintf(s, n, format, c23.VaStart(va))
}

func Vprintf(format, ap c23.Ptr) int32 { return Vfprintf(c23.Stdio(1), format, ap) }

func Vfprintf(f, format, ap c23.Ptr) int32 {
	b := c23.FormatC(c23.GoString(format), c23.VaOf(ap))
	if !c23.FileOf(f).Write(b) {
		return -1
	}
	return int32(len(b))
}

func Vsprintf(s, format, ap c23.Ptr) int32 {
	b := c23.FormatC(c23.GoString(format), c23.VaOf(ap))
	copy(c23.BytesAt(s, uintptr(len(b)+1)), append(b, 0))
	return int32(len(b))
}

func Vsnprintf(s c23.Ptr, n uint64, format, ap c23.Ptr) int32 {
	b := c23.FormatC(c23.GoString(format), c23.VaOf(ap))
	if n > 0 {
		k := min(uint64(len(b)), n-1)
		copy(c23.BytesAt(s, uintptr(k+1)), append(b[:k:k], 0))
	}
	return int32(len(b))
}

func Puts(s c23.Ptr) int32 {
	if !out().Write(append([]byte(c23.GoString(s)), '\n')) {
		return -1
	}
	return 0
}

func Fputs(s, f c23.Ptr) int32 {
	if !c23.FileOf(f).Write([]byte(c23.GoString(s))) {
		return -1
	}
	return 0
}

func Putchar(c int32) int32 { return Fputc(c, c23.Stdio(1)) }

func Fputc(c int32, f c23.Ptr) int32 {
	if !c23.FileOf(f).Write([]byte{byte(c)}) {
		return -1
	}
	return int32(uint8(c))
}

func Fwrite(p c23.Ptr, size, n uint64, f c23.Ptr) uint64 {
	if size == 0 || !c23.FileOf(f).Write(c23.BytesAt(p, uintptr(size*n))) {
		return 0
	}
	return n
}

func Fgetc(f c23.Ptr) int32 { return c23.FileOf(f).Getc() }

func Getchar() int32 { return Fgetc(c23.Stdio(0)) }

func Ungetc(c int32, p c23.Ptr) int32 {
	if c == -1 {
		return -1
	}
	f := c23.FileOf(p)
	if !f.Readable() {
		return -1
	}
	f.Reader()
	f.Unget = append(f.Unget, byte(c))
	f.Eof = false
	return int32(uint8(c))
}

func Fgets(s c23.Ptr, n int32, f c23.Ptr) c23.Ptr {
	if n <= 0 {
		return c23.Ptr(nil)
	}
	fl := c23.FileOf(f)
	i := int32(0)
	for i < n-1 {
		c := fl.Getc()
		if c < 0 {
			if fl.Err {
				return c23.Ptr(nil)
			}
			break
		}
		c23.Store8(c23.Add(s, int(i)), uint8(c))
		i++
		if c == '\n' {
			break
		}
	}
	if i == 0 && n > 1 {
		return c23.Ptr(nil)
	}
	c23.Store8(c23.Add(s, int(i)), 0)
	return s
}

func Fread(p c23.Ptr, size, n uint64, f c23.Ptr) uint64 {
	if size == 0 || n == 0 {
		return 0
	}
	fl := c23.FileOf(f)
	if !fl.Readable() {
		return 0
	}
	b := c23.BytesAt(p, uintptr(size*n))
	k := 0
	for k < len(b) && len(fl.Unget) > 0 {
		b[k] = byte(fl.Getc())
		k++
	}
	r := fl.Reader()
	for k < len(b) {
		m, err := r.Read(b[k:])
		k += m
		if err != nil {
			fl.ReadErr(err)
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
func Fopen(name, mode c23.Ptr) c23.Ptr {
	flag, rd, wr, ok := openMode(c23.GoString(mode))
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr(nil)
	}
	f, err := os.OpenFile(c23.GoString(name), flag, 0o666)
	if err != nil {
		c23.SetErrnoOf(err)
		return c23.Ptr(nil)
	}
	return c23.NewFile(c23.FdNew(f, cflags(flag)), rd, wr)
}

// Freopen is freopen: it closes the stream's file and opens name in its
// place, or, if name is null, the stream's file again with the new mode.
// The stream is closed if that fails.
func Freopen(name, mode, p c23.Ptr) c23.Ptr {
	f := c23.FileOf(p)
	if f.Mem != nil { // a memory stream has no file to reopen
		Fclose(p)
		c23.SetErrno(c23.EBADF)
		return c23.Ptr(nil)
	}
	f.Flush()
	flag, rd, wr, ok := openMode(c23.GoString(mode))
	var path string
	if c23.Addr(name) != 0 {
		path = c23.GoString(name)
	} else if of := f.OsFile(); of != nil {
		path = of.Name()
	}
	c23.FdClose(f.Fd)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		f.Close()
		return c23.Ptr(nil)
	}
	nf, err := os.OpenFile(path, flag, 0o666)
	if err != nil {
		c23.SetErrnoOf(err)
		f.Close()
		return c23.Ptr(nil)
	}
	fdSet(f.Fd, nf, cflags(flag))
	f.W.Reset(c23.FdIO{F: f})
	f.R, f.Unget = nil, nil
	f.Eof, f.Err = false, false
	f.Rd, f.Wr = rd, wr
	return p
}

var tmpnamMu sync.Mutex

var tmpnamBuf c23.Ptr

var tmpnamN uint64

// Tmpnam is tmpnam: a name in /tmp of no existing file, at s or, if s is
// null, in an array the next call reuses. Its names are at most
// L_tmpnam (20) bytes long, with the null.
func Tmpnam(s c23.Ptr) c23.Ptr {
	tmpnamMu.Lock()
	defer tmpnamMu.Unlock()
	if c23.Addr(s) == 0 {
		if tmpnamBuf == nil {
			tmpnamBuf = c23.Malloc(20)
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
		copy(c23.BytesAt(s, uintptr(len(name)+1)), append(name, 0))
		return s
	}
	return c23.Ptr(nil)
}

func Fclose(p c23.Ptr) int32 {
	f := c23.FileOf(p)
	r := f.Flush()
	if f.Mem != nil {
		f.Mem.Close()
		f.Close()
		return r
	}
	if err := c23.FdClose(f.Fd); err != nil {
		c23.SetErrnoOf(err)
		r = -1
	}
	f.Close()
	return r
}

// Tmpfile is tmpfile: a new file, opened w+, removed when closed (on
// Unix, at once).
func Tmpfile() c23.Ptr {
	f, err := os.CreateTemp("", "tmpf")
	if err != nil {
		c23.SetErrnoOf(err)
		return c23.Ptr(nil)
	}
	os.Remove(f.Name())
	return c23.NewFile(c23.FdNew(f, c23.O_RDWR), true, true)
}

func Fseek(p c23.Ptr, off int64, whence int32) int32 {
	f := c23.FileOf(p)
	if whence < 0 || whence > 2 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if f.Flush() != 0 {
		return -1
	}
	if f.R != nil && whence == io.SeekCurrent {
		off -= int64(f.R.Buffered() + len(f.Unget))
	}
	f.R, f.Unget = nil, nil
	if _, err := f.Seek(off, int(whence)); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	f.Eof = false
	return 0
}

func Ftell(p c23.Ptr) int64 {
	f := c23.FileOf(p)
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	if f.R != nil {
		return pos - int64(f.R.Buffered()+len(f.Unget))
	}
	return pos + int64(f.W.Buffered())
}

func Rewind(p c23.Ptr) {
	Fseek(p, 0, io.SeekStart)
	c23.FileOf(p).Err = false
}

func Feof(p c23.Ptr) int32 { return c23.B2I(c23.FileOf(p).Eof) }

func Ferror(p c23.Ptr) int32 { return c23.B2I(c23.FileOf(p).Err) }

func Clearerr(p c23.Ptr) {
	f := c23.FileOf(p)
	f.Eof, f.Err = false, false
}

// Setvbuf is setvbuf: _IONBF (2) makes the stream unbuffered; line
// buffering is full buffering. C's buffer is not used.
func Setvbuf(p, buf c23.Ptr, mode int32, size uint64) int32 {
	if mode < 0 || mode > 2 {
		return -1
	}
	c23.FileOf(p).Unbuf = mode == 2
	return 0
}

func Setbuf(p, buf c23.Ptr) {
	c23.FileOf(p).Unbuf = c23.Addr(buf) == 0
}

func Remove(name c23.Ptr) int32 {
	if err := os.Remove(c23.GoString(name)); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return 0
}

func Rename(old, new c23.Ptr) int32 {
	if err := os.Rename(c23.GoString(old), c23.GoString(new)); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return 0
}

// Perror is perror: s, then the message of errno, on stderr.
func Perror(s c23.Ptr) {
	msg := c23.ErrnoString(c23.Errno())
	if c23.Addr(s) != 0 && c23.Load8(s) != 0 {
		msg = c23.GoString(s) + ": " + msg
	}
	c23.FileOf(c23.Stdio(2)).Write([]byte(msg + "\n"))
}
