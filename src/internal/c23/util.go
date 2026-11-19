// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// The common extensions of glibc and musl: getline, asprintf, err.h,
// libgen.h, malloc.h, sys/random.h, sys/uio.h, sys/utsname.h, grp.h,
// syslog.h, langinfo.h and others.

// progname is glibc's program_invocation_short_name (musl's __progname):
// the last element of argv[0].
func progname() string {
	if len(os.Args) == 0 {
		return ""
	}
	return filepath.Base(os.Args[0])
}

// stdio.h

// Getdelim is getdelim: it reads up to and including delim into *lineptr,
// which it grows with realloc (*n is its size), and ends it with a null.
// It returns the number of bytes read, or -1 at the end of the file or
// for an error.
func Getdelim(lineptr, n Ptr, delim int32, stream Ptr) int64 {
	f := fileOf(stream)
	if Addr(lineptr) == 0 || Addr(n) == 0 {
		SetErrno(EINVAL)
		return -1
	}
	if !f.readable() {
		return -1
	}
	var b []byte
	for {
		c := f.getc()
		if c < 0 {
			break
		}
		b = append(b, byte(c))
		if byte(c) == byte(delim) {
			break
		}
	}
	if len(b) == 0 {
		return -1
	}
	buf, size := LoadPtr(lineptr), loadLong(n)
	if Addr(buf) == 0 {
		size = 0
	}
	if int64(len(b)+1) > size {
		size = max(int64(len(b)+1), 120)
		buf = Realloc(buf, uint64(size))
		StorePtr(lineptr, buf)
		storeLong(n, size)
	}
	copy(bytesAt(buf, uintptr(len(b)+1)), append(b, 0))
	return int64(len(b))
}

// Getline is getline: getdelim of '\n'.
func Getline(lineptr, n, stream Ptr) int64 { return Getdelim(lineptr, n, '\n', stream) }

func Asprintf(strp, format Ptr, va ...any) int32 { return Vasprintf(strp, format, VaStart(va)) }

// Vasprintf is vasprintf: it formats into memory it allocates, whose
// address it stores at *strp.
func Vasprintf(strp, format, ap Ptr) int32 {
	b := formatC(GoString(format), vaOf(ap))
	StorePtr(strp, CString(string(b)))
	return int32(len(b))
}

func Dprintf(fd int32, format Ptr, va ...any) int32 {
	return Vdprintf(fd, format, VaStart(va))
}

// Vdprintf is vdprintf: it formats to the file descriptor fd, with one
// write.
func Vdprintf(fd int32, format, ap Ptr) int32 {
	b := formatC(GoString(format), vaOf(ap))
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	n, err := f.Write(b)
	if err != nil && n == 0 {
		setErrnoOf(err)
		return -1
	}
	return int32(n)
}

// string.h

// Strsep is strsep: the token at *stringp, which it ends at the first
// byte of delim with a null, and *stringp past that (or null).
func Strsep(stringp, delim Ptr) Ptr {
	s := LoadPtr(stringp)
	if Addr(s) == 0 {
		return Ptr{}
	}
	d := GoString(delim)
	for p := s; ; p = Add(p, 1) {
		c := Load8(p)
		if c == 0 {
			StorePtr(stringp, Ptr{})
			return s
		}
		if strings.IndexByte(d, c) >= 0 {
			Store8(p, 0)
			StorePtr(stringp, Add(p, 1))
			return s
		}
	}
}

// signalText are the signals' descriptions, glibc's (Linux's numbers).
var signalText = [...]string{
	1: "Hangup", 2: "Interrupt", 3: "Quit", 4: "Illegal instruction",
	5: "Trace/breakpoint trap", 6: "Aborted", 7: "Bus error",
	8: "Floating point exception", 9: "Killed", 10: "User defined signal 1",
	11: "Segmentation fault", 12: "User defined signal 2", 13: "Broken pipe",
	14: "Alarm clock", 15: "Terminated", 16: "Stack fault", 17: "Child exited",
	18: "Continued", 19: "Stopped (signal)", 20: "Stopped", 21: "Stopped (tty input)",
	22: "Stopped (tty output)", 23: "Urgent I/O condition", 24: "CPU time limit exceeded",
	25: "File size limit exceeded", 26: "Virtual timer expired",
	27: "Profiling timer expired", 28: "Window changed", 29: "I/O possible",
	30: "Power failure", 31: "Bad system call",
}

var (
	strsignalMu  sync.Mutex
	strsignalMsg = map[int32]Ptr{}
)

// Strsignal is strsignal: sig's description, as glibc words it, in an
// array C must not modify.
func Strsignal(sig int32) Ptr {
	var s string
	switch {
	case sig > 0 && int(sig) < len(signalText):
		s = signalText[sig]
	case sig >= 34 && sig < nsig:
		s = "Real-time signal " + strconv.Itoa(int(sig-34))
	default:
		s = "Unknown signal " + strconv.Itoa(int(sig))
	}
	strsignalMu.Lock()
	defer strsignalMu.Unlock()
	p, ok := strsignalMsg[sig]
	if !ok {
		p = CString(s)
		strsignalMsg[sig] = p
	}
	return p
}

// stdlib.h

// Reallocarray is reallocarray: realloc of nmemb*size bytes, or null
// (errno ENOMEM) if that is more than an object may have.
func Reallocarray(p Ptr, nmemb, size uint64) Ptr {
	const maxSize = uint64(^uintptr(0) >> 1) // size_t's, which an object's size must not pass
	if size != 0 && nmemb > maxSize/size {
		SetErrno(ENOMEM)
		return Ptr{}
	}
	return Realloc(p, nmemb*size)
}

// QsortR is glibc's qsort_r: qsort with a comparison function that
// takes arg, after the elements.
func QsortR(base Ptr, n, size uint64, cmp, arg Ptr) {
	if n < 2 || size == 0 {
		return
	}
	f := FuncOf[func(Ptr, Ptr, Ptr) int32](cmp)
	q := &qsorter{base: base, size: int(size), tmp: Malloc(size), cmp: func(a, b Ptr) int32 { return f(a, b, arg) }}
	k := int(n)
	for i := k/2 - 1; i >= 0; i-- {
		q.siftDown(i, k)
	}
	for end := k - 1; end > 0; end-- {
		q.swap(0, end)
		q.siftDown(0, end)
	}
}

// Mkdtemp is mkdtemp: it replaces the XXXXXX that ends template with
// what makes the name of a new directory, which it creates (mode 0700).
func Mkdtemp(template Ptr) Ptr {
	n := Strlen(template)
	if n < 6 || GoString(Add(template, int(n-6))) != "XXXXXX" {
		SetErrno(EINVAL)
		return Ptr{}
	}
	suffix := Add(template, int(n-6))
	const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for range 238328 { // TMP_MAX
		x := runtimeRand()
		for i := range 6 {
			Store8(Add(suffix, i), digits[x%62])
			x /= 62
		}
		err := os.Mkdir(GoString(template), 0o700)
		if err == nil {
			return template
		}
		if !isErr(err, os.ErrExist) {
			setErrnoOf(err)
			return Ptr{}
		}
	}
	SetErrno(EEXIST)
	return Ptr{}
}

// err.h

// warnf writes the program's name, then what fmt formats (unless fmt is
// null), then, if errno is true, ": " and the message of errno, on
// stderr.
func warnf(format, ap Ptr, errno bool) {
	e := Errno()
	msg := progname() + ": "
	if Addr(format) != 0 {
		msg += string(formatC(GoString(format), vaOf(ap)))
		if errno {
			msg += ": "
		}
	}
	if errno {
		msg += strerror(e)
	}
	Fflush(Stdio(1))
	fileOf(Stdio(2)).write([]byte(msg + "\n"))
}

func Warn(format Ptr, va ...any)  { warnf(format, VaStart(va), true) }
func Vwarn(format, ap Ptr)        { warnf(format, ap, true) }
func Warnx(format Ptr, va ...any) { warnf(format, VaStart(va), false) }
func Vwarnx(format, ap Ptr)       { warnf(format, ap, false) }

func Err(eval int32, format Ptr, va ...any) { Verr(eval, format, VaStart(va)) }
func Verr(eval int32, format, ap Ptr) {
	warnf(format, ap, true)
	Exit(eval)
}
func Errx(eval int32, format Ptr, va ...any) { Verrx(eval, format, VaStart(va)) }
func Verrx(eval int32, format, ap Ptr) {
	warnf(format, ap, false)
	Exit(eval)
}

// libgen.h

var (
	dotOnce sync.Once
	dotStr  Ptr
)

// dot is the "." basename and dirname return for an empty path.
func dot() Ptr {
	dotOnce.Do(func() { dotStr = CString(".") })
	return dotStr
}

// Basename is POSIX's basename (glibc's __xpg_basename): the last element
// of path, whose trailing slashes it removes in place.
func Basename(path Ptr) Ptr {
	if Addr(path) == 0 || Load8(path) == 0 {
		return dot()
	}
	i := int(Strlen(path)) - 1
	for ; i > 0 && Load8(Add(path, i)) == '/'; i-- {
		Store8(Add(path, i), 0)
	}
	for ; i > 0 && Load8(Add(path, i-1)) != '/'; i-- {
	}
	return Add(path, i)
}

// Dirname is dirname: path without its last element, which it removes
// in place, as musl does ("//" is "/", where glibc keeps "//").
func Dirname(path Ptr) Ptr {
	if Addr(path) == 0 || Load8(path) == 0 {
		return dot()
	}
	at := func(i int) byte { return Load8(Add(path, i)) }
	i := int(Strlen(path)) - 1
	for ; at(i) == '/'; i-- {
		if i == 0 {
			Store8(Add(path, 1), 0)
			return path
		}
	}
	for ; at(i) != '/'; i-- {
		if i == 0 {
			return dot()
		}
	}
	for ; at(i) == '/'; i-- {
		if i == 0 {
			Store8(Add(path, 1), 0)
			return path
		}
	}
	Store8(Add(path, i+1), 0)
	return path
}

// malloc.h

// MallocUsableSize is malloc_usable_size: the size of what malloc
// returned, which is what it was asked for.
func MallocUsableSize(p Ptr) uint64 {
	if p.obj == nil {
		return 0
	}
	return uint64(p.size)
}

// Memalign is memalign: aligned_alloc, with the alignment rounded up to
// a power of two, as glibc does.
func Memalign(align, size uint64) Ptr {
	a := uint64(1)
	for a < align {
		a <<= 1
	}
	return AlignedAlloc(a, size)
}

func Valloc(size uint64) Ptr { return AlignedAlloc(4096, size) }
func Pvalloc(size uint64) Ptr {
	return AlignedAlloc(4096, (size+4095)&^4095)
}

// sys/random.h

// runtimeRand is the runtime's random number generator: ChaCha8, seeded
// from the system's entropy, which math/rand/v2's top-level functions use.
//
//go:linkname runtimeRand runtime.rand
func runtimeRand() uint64

// randomFill fills b with random bytes.
func randomFill(b []byte) {
	for i := 0; i < len(b); i += 8 {
		x := runtimeRand()
		for j := i; j < i+8 && j < len(b); j++ {
			b[j] = byte(x)
			x >>= 8
		}
	}
}

// Getrandom is getrandom: buflen random bytes, from the runtime's
// generator, whatever the flags (GRND_NONBLOCK, GRND_RANDOM and
// GRND_INSECURE): it never blocks.
func Getrandom(buf Ptr, n uint64, flags uint32) int64 {
	if flags&^7 != 0 {
		SetErrno(EINVAL)
		return -1
	}
	randomFill(bytesAt(buf, uintptr(n)))
	return int64(n)
}

// Getentropy is getentropy: at most 256 random bytes (errno EIO for
// more).
func Getentropy(buf Ptr, n uint64) int32 {
	if n > 256 {
		SetErrno(EIO)
		return -1
	}
	randomFill(bytesAt(buf, uintptr(n)))
	return 0
}

// sys/uio.h

// iovecs returns the cnt struct iovec {void *iov_base; size_t iov_len}
// at iov, or ok false (errno EINVAL).
func iovecs(iov Ptr, cnt int32) (bases []Ptr, lens []int, total int, ok bool) {
	if cnt < 0 || cnt > 1024 {
		SetErrno(EINVAL)
		return nil, nil, 0, false
	}
	for i := range int(cnt) {
		e := Add(iov, i*2*int(wordSize))
		n := loadLong(Add(e, int(wordSize)))
		if n < 0 || int64(total)+n > 1<<62 {
			SetErrno(EINVAL)
			return nil, nil, 0, false
		}
		bases = append(bases, LoadPtr(e))
		lens = append(lens, int(n))
		total += int(n)
	}
	return bases, lens, total, true
}

// Readv is readv: one read, into the buffers in order.
func Readv(fd int32, iov Ptr, cnt int32) int64 {
	bases, lens, total, ok := iovecs(iov, cnt)
	if !ok {
		return -1
	}
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	b := make([]byte, total)
	n, err := f.Read(b)
	if err != nil && n == 0 && total > 0 {
		if err == io.EOF {
			return 0
		}
		setErrnoOf(err)
		return -1
	}
	rest := b[:n]
	for i, base := range bases {
		k := min(lens[i], len(rest))
		copy(bytesAt(base, uintptr(k)), rest[:k])
		rest = rest[k:]
	}
	return int64(n)
}

// Writev is writev: the buffers in order, with one write.
func Writev(fd int32, iov Ptr, cnt int32) int64 {
	bases, lens, total, ok := iovecs(iov, cnt)
	if !ok {
		return -1
	}
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	b := make([]byte, 0, total)
	for i, base := range bases {
		b = append(b, bytesAt(base, uintptr(lens[i]))...)
	}
	n, err := f.Write(b)
	if err != nil && n == 0 && total > 0 {
		setErrnoOf(err)
		return -1
	}
	return int64(n)
}

// sys/utsname.h

// unameSys are uname's sysname of GOOS.
var unameSys = map[string]string{
	"linux": "Linux", "android": "Linux", "darwin": "Darwin", "ios": "Darwin",
	"freebsd": "FreeBSD", "netbsd": "NetBSD", "openbsd": "OpenBSD",
	"dragonfly": "DragonFly", "solaris": "SunOS", "illumos": "SunOS",
	"aix": "AIX", "windows": "Windows_NT", "plan9": "Plan9", "js": "js",
	"wasip1": "WASI",
}

// unameMachine are uname's machine of GOARCH, as Linux names them.
var unameMachine = map[string]string{
	"amd64": "x86_64", "386": "i686", "arm64": "aarch64", "arm": "armv7l",
	"loong64": "loongarch64", "wasm": "wasm32",
}

// Uname is uname: sysname from GOOS, as the system calls itself; nodename
// from os.Hostname; release, version and domainname from Linux's
// /proc/sys/kernel (elsewhere, empty); machine from GOARCH, as Linux
// calls it (x86_64 for amd64, and on darwin arm64).
func Uname(name Ptr) int32 {
	sys := unameSys[runtime.GOOS]
	if sys == "" {
		sys = runtime.GOOS
	}
	mach := unameMachine[runtime.GOARCH]
	if mach == "" || runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		mach = runtime.GOARCH
	}
	host, _ := os.Hostname()
	kernel := func(f string) string {
		b, err := os.ReadFile("/proc/sys/kernel/" + f)
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(string(b), "\n")
	}
	fields := [6]string{sys, host, kernel("osrelease"), kernel("version"), mach, kernel("domainname")}
	if runtime.GOOS == "linux" && fields[5] == "" {
		fields[5] = "(none)"
	}
	b := bytesAt(name, 6*65)
	clear(b)
	for i, s := range fields {
		copy(b[i*65:i*65+64], s)
	}
	return 0
}

// grp.h

// group is C's struct group.
type group struct {
	name, passwd unsafe.Pointer
	gid          uint32
	mem          unsafe.Pointer
}

var (
	grMu  sync.Mutex
	grBuf Ptr
)

// lookupGroup finds the group of the name, or of gid if the name is "",
// in /etc/group: its fields.
func lookupGroup(name string, gid uint32) ([]string, bool) {
	f, err := os.Open("/etc/group")
	if err != nil {
		return nil, false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		e := strings.Split(s.Text(), ":")
		if len(e) != 4 || strings.HasPrefix(e[0], "#") {
			continue
		}
		if name != "" && e[0] == name {
			return e, true
		}
		if name == "" {
			if g, err := strconv.ParseUint(e[2], 10, 32); err == nil && uint32(g) == gid {
				return e, true
			}
		}
	}
	return nil, false
}

// grResult returns the static struct group of e, or null.
func grResult(e []string, ok bool) Ptr {
	if !ok {
		return Ptr{}
	}
	grMu.Lock()
	defer grMu.Unlock()
	if grBuf.obj == nil {
		grBuf = New[group]()
	}
	gid, _ := strconv.ParseUint(e[2], 10, 32)
	var members []string
	if e[3] != "" {
		members = strings.Split(e[3], ",")
	}
	mem := NewArray[unsafe.Pointer](uintptr(len(members)+1) * wordSize)
	for i, m := range members {
		StorePtr(Add(mem, i*int(wordSize)), CString(m))
	}
	var gr group
	StorePtr(grBuf, CString(e[0]))
	StorePtr(Add(grBuf, int(unsafe.Offsetof(gr.passwd))), CString(e[1]))
	Store32(Add(grBuf, int(unsafe.Offsetof(gr.gid))), uint32(gid))
	StorePtr(Add(grBuf, int(unsafe.Offsetof(gr.mem))), mem)
	return grBuf
}

// Getgrnam is getgrnam: the group of the name in /etc/group, or null.
func Getgrnam(name Ptr) Ptr {
	n := GoString(name)
	if n == "" {
		return Ptr{}
	}
	return grResult(lookupGroup(n, 0))
}

// Getgrgid is getgrgid: the group of gid in /etc/group, or null.
func Getgrgid(gid uint32) Ptr { return grResult(lookupGroup("", gid)) }

// syslog.h: there is no system logger to send to. Messages go to stderr
// with openlog's LOG_PERROR, as "ident[pid]: message", as glibc and musl
// write them there, and are otherwise dropped.

var syslogState = struct {
	sync.Mutex
	ident  string
	opened bool
	option int32
	mask   int32
}{mask: 0xff}

const (
	logPID    = 0x01
	logPerror = 0x20
)

func Openlog(ident Ptr, option, facility int32) {
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	st.ident = ""
	if Addr(ident) != 0 {
		st.ident = GoString(ident)
	}
	st.option = option
	st.opened = true
}

func Closelog() {
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	st.ident, st.option, st.opened = "", 0, false
}

// Setlogmask is setlogmask: it sets the mask of the priorities logged,
// unless mask is 0, and returns the previous one.
func Setlogmask(mask int32) int32 {
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	old := st.mask
	if mask != 0 {
		st.mask = mask
	}
	return old
}

func Syslog(priority int32, format Ptr, va ...any) { Vsyslog(priority, format, VaStart(va)) }

// Vsyslog is vsyslog. The format's %m is the message of errno.
func Vsyslog(priority int32, format, ap Ptr) {
	e := Errno()
	st := &syslogState
	st.Lock()
	defer st.Unlock()
	if st.mask&(1<<(priority&7)) == 0 || st.option&logPerror == 0 {
		return
	}
	f := strings.ReplaceAll(GoString(format), "%m", strings.ReplaceAll(strerror(e), "%", "%%"))
	msg := string(formatC(f, vaOf(ap)))
	ident := st.ident
	if ident == "" {
		ident = progname()
	}
	if st.option&logPID != 0 {
		ident += "[" + strconv.Itoa(os.Getpid()) + "]"
	}
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fileOf(Stdio(2)).write([]byte(ident + ": " + msg))
}

// langinfo.h

// NlLanginfo is nl_langinfo, of the C locale: its CODESET is "UTF-8",
// which is what the C library's multibyte functions read and write.
func NlLanginfo(item int32) Ptr {
	langinfoMu.Lock()
	defer langinfoMu.Unlock()
	p, ok := langinfoStr[item]
	if !ok {
		p = CString(langinfo(item))
		langinfoStr[item] = p
	}
	return p
}

var (
	langinfoMu  sync.Mutex
	langinfoStr = map[int32]Ptr{}
)

func langinfo(item int32) string {
	switch {
	case item == 14: // CODESET
		return "UTF-8"
	case item == 0x10000: // RADIXCHAR
		return "."
	case item >= 0x20000 && item <= 0x20006: // ABDAY_1 ...
		return time.Weekday(item - 0x20000).String()[:3]
	case item >= 0x20007 && item <= 0x2000D: // DAY_1 ...
		return time.Weekday(item - 0x20007).String()
	case item >= 0x2000E && item <= 0x20019: // ABMON_1 ...
		return time.Month(item - 0x2000E + 1).String()[:3]
	case item >= 0x2001A && item <= 0x20025: // MON_1 ...
		return time.Month(item - 0x2001A + 1).String()
	}
	switch item {
	case 0x20026:
		return "AM"
	case 0x20027:
		return "PM"
	case 0x20028:
		return "%a %b %e %H:%M:%S %Y"
	case 0x20029:
		return "%m/%d/%y"
	case 0x2002A:
		return "%H:%M:%S"
	case 0x2002B:
		return "%I:%M:%S %p"
	case 0x50000:
		return "^[yY]"
	case 0x50001:
		return "^[nN]"
	}
	return ""
}

// sys/file.h

const (
	LOCK_SH = 1
	LOCK_EX = 2
	LOCK_NB = 4
	LOCK_UN = 8
)

// Flock is flock: a shared (LOCK_SH) or exclusive (LOCK_EX) lock of fd's
// whole file, or its release (LOCK_UN); with LOCK_NB, errno EWOULDBLOCK
// (EAGAIN) rather than waiting for another process's lock.
func Flock(fd int32, op int32) int32 {
	switch op &^ LOCK_NB {
	case LOCK_SH, LOCK_EX, LOCK_UN:
	default:
		SetErrno(EINVAL)
		return -1
	}
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	return errRet(flockFile(f, op))
}
