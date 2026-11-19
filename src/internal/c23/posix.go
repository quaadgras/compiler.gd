// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

// POSIX's file descriptors (unistd.h, fcntl.h): small ints that name
// os.Files in a table of the program's own, which C's streams use too.
// The numbers are not the system's: they are allocated as POSIX says,
// the lowest free first, with 0, 1 and 2 standard input, output and
// error.

// fcntl.h's values: Linux's.
const (
	O_RDONLY    = 0
	O_WRONLY    = 1
	O_RDWR      = 2
	O_ACCMODE   = 3
	O_CREAT     = 0o100
	O_EXCL      = 0o200
	O_NOCTTY    = 0o400
	O_TRUNC     = 0o1000
	O_APPEND    = 0o2000
	O_NONBLOCK  = 0o4000
	O_DSYNC     = 0o10000
	O_LARGEFILE = 0o100000
	O_DIRECTORY = 0o200000
	O_NOFOLLOW  = 0o400000
	O_CLOEXEC   = 0o2000000
	O_SYNC      = 0o4010000

	F_DUPFD         = 0
	F_GETFD         = 1
	F_SETFD         = 2
	F_GETFL         = 3
	F_SETFL         = 4
	F_DUPFD_CLOEXEC = 1030
	FD_CLOEXEC      = 1
)

// An ofile is an open file: what the descriptors dup makes share.
type ofile struct {
	f     *os.File
	sp    Special    // what it is if not an os.File (f is nil)
	ra    *ReadAhead // f read ahead, once poll or O_NONBLOCK needs it (special.go)
	refs  int
	flags int32 // as F_GETFL returns them
}

type fdEntry struct {
	of      *ofile
	cloexec bool
}

var (
	fdMu  sync.Mutex
	fdTab = []*fdEntry{
		{of: &ofile{f: os.Stdin, refs: 1, flags: O_RDONLY}},
		{of: &ofile{f: os.Stdout, refs: 1, flags: O_WRONLY}},
		{of: &ofile{f: os.Stderr, refs: 1, flags: O_WRONLY}},
	}
)

// cflags are the flags F_GETFL returns of a file os.OpenFile opened with
// flag.
func cflags(flag int) int32 {
	var r int32
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		r = O_WRONLY
	case os.O_RDWR:
		r = O_RDWR
	}
	if flag&os.O_APPEND != 0 {
		r |= O_APPEND
	}
	if flag&os.O_SYNC != 0 {
		r |= O_SYNC
	}
	return r | O_LARGEFILE
}

// fdInstall puts e at the lowest free descriptor from min on.
// fdMu must be held.
func fdInstall(e *fdEntry, min int32) int32 {
	fd := int(min)
	for fd < len(fdTab) && fdTab[fd] != nil {
		fd++
	}
	for fd >= len(fdTab) {
		fdTab = append(fdTab, nil)
	}
	fdTab[fd] = e
	return int32(fd)
}

// fdNew returns a new descriptor of f.
func fdNew(f *os.File, flags int32) int32 {
	fdMu.Lock()
	defer fdMu.Unlock()
	return fdInstall(&fdEntry{of: &ofile{f: f, refs: 1, flags: flags}}, 0)
}

// fdSet makes fd, which is free, a descriptor of f.
func fdSet(fd int32, f *os.File, flags int32) {
	fdMu.Lock()
	defer fdMu.Unlock()
	for int(fd) >= len(fdTab) {
		fdTab = append(fdTab, nil)
	}
	fdTab[fd] = &fdEntry{of: &ofile{f: f, refs: 1, flags: flags}}
}

// fdEnt returns fd's entry, or nil (errno EBADF).
// fdMu must be held.
func fdEnt(fd int32) *fdEntry {
	if fd < 0 || int(fd) >= len(fdTab) || fdTab[fd] == nil {
		return nil
	}
	return fdTab[fd]
}

// fdFile returns fd's open file, or nil.
func fdFile(fd int32) *ofile {
	fdMu.Lock()
	defer fdMu.Unlock()
	if e := fdEnt(fd); e != nil {
		return e.of
	}
	return nil
}

// fdOS returns fd's os.File, or nil and sets errno to EBADF (or ESPIPE
// for a socket).
func fdOS(fd int32) *os.File {
	if of := fdFile(fd); of != nil {
		if of.sp != nil {
			SetErrno(ESPIPE)
			return nil
		}
		return of.f
	}
	SetErrno(EBADF)
	return nil
}

// release drops a descriptor of of: the last closes the file, unless it
// is one of os's standard files. fdMu must be held.
func (of *ofile) release() error {
	of.refs--
	if of.refs > 0 || of.f == os.Stdin || of.f == os.Stdout || of.f == os.Stderr {
		return nil
	}
	if of.sp != nil {
		return of.sp.Close()
	}
	return of.f.Close()
}

// fdClose frees fd.
func fdClose(fd int32) error {
	fdMu.Lock()
	defer fdMu.Unlock()
	e := fdEnt(fd)
	if e == nil {
		return errnoErr(EBADF)
	}
	fdTab[fd] = nil
	return e.of.release()
}

// fdIO reads and writes a stream's file: its descriptor's, whatever that
// is now (dup2 may change it).
type fdIO struct{ f *File }

func (d fdIO) Write(b []byte) (int, error) {
	if d.f.mem != nil {
		return d.f.mem.write(b)
	}
	of := fdFile(d.f.fd)
	if of == nil {
		return 0, errnoErr(EBADF)
	}
	return of.write(b)
}

func (d fdIO) Read(b []byte) (int, error) {
	if d.f.mem != nil {
		return d.f.mem.read(b)
	}
	of := fdFile(d.f.fd)
	if of == nil {
		return 0, errnoErr(EBADF)
	}
	return of.read(b)
}

// osFile returns the stream's os.File, or nil.
func (f *File) osFile() *os.File {
	if f.mem != nil {
		return nil
	}
	if of := fdFile(f.fd); of != nil {
		return of.f
	}
	return nil
}

func (f *File) seek(off int64, whence int) (int64, error) {
	if f.mem != nil {
		return f.mem.seek(off, whence)
	}
	of := f.osFile()
	if of == nil {
		return 0, errnoErr(EBADF)
	}
	return of.Seek(off, whence)
}

// Fileno is fileno.
func Fileno(p Ptr) int32 {
	f := fileOf(p)
	if fdFile(f.fd) == nil {
		SetErrno(EBADF)
		return -1
	}
	return f.fd
}

// Fdopen is fdopen: a stream of fd, which fclose closes.
func Fdopen(fd int32, mode Ptr) Ptr {
	_, rd, wr, ok := openMode(GoString(mode))
	if !ok {
		SetErrno(EINVAL)
		return Ptr{}
	}
	of := fdFile(fd)
	if of == nil {
		SetErrno(EBADF)
		return Ptr{}
	}
	if acc := of.flags & O_ACCMODE; rd && acc == O_WRONLY || wr && acc == O_RDONLY {
		SetErrno(EINVAL)
		return Ptr{}
	}
	return newFile(fd, rd, wr)
}

// unistd.h

func Read(fd int32, buf Ptr, n uint64) int64 {
	f := fdFile(fd)
	if f == nil {
		SetErrno(EBADF)
		return -1
	}
	k, err := f.read(bytesAt(buf, uintptr(n)))
	if err != nil && err != io.EOF {
		setErrnoOf(err)
		return -1
	}
	return int64(k)
}

func Write(fd int32, buf Ptr, n uint64) int64 {
	f := fdFile(fd)
	if f == nil {
		SetErrno(EBADF)
		return -1
	}
	k, err := f.write(bytesAt(buf, uintptr(n)))
	if err != nil && k == 0 {
		setErrnoOf(err)
		return -1
	}
	return int64(k)
}

func Close(fd int32) int32 {
	if err := fdClose(fd); err != nil {
		setErrnoOf(err)
		return -1
	}
	return 0
}

func Lseek(fd int32, off int64, whence int32) int64 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	if whence < 0 || whence > 2 {
		SetErrno(EINVAL)
		return -1
	}
	pos, err := f.Seek(off, int(whence))
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	return pos
}

// Unlink is unlink, which does not remove directories (EISDIR, as Linux).
func Unlink(path Ptr) int32 {
	name := GoString(path)
	if fi, err := os.Lstat(name); err == nil && fi.IsDir() {
		SetErrno(EISDIR)
		return -1
	}
	return errRet(os.Remove(name))
}

// Rmdir is rmdir, which removes only directories.
func Rmdir(path Ptr) int32 {
	name := GoString(path)
	fi, err := os.Lstat(name)
	if err != nil {
		return errRet(err)
	}
	if !fi.IsDir() {
		SetErrno(ENOTDIR)
		return -1
	}
	return errRet(os.Remove(name))
}

// errRet returns 0 for no error, or -1 with errno set to err's.
func errRet(err error) int32 {
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	return 0
}

// access's modes.
const (
	F_OK = 0
	X_OK = 1
	W_OK = 2
	R_OK = 4
)

func Access(path Ptr, mode int32) int32 {
	if mode&^7 != 0 {
		SetErrno(EINVAL)
		return -1
	}
	return errRet(access(GoString(path), uint32(mode)))
}

func Getpid() int32  { return int32(os.Getpid()) }
func Getppid() int32 { return int32(os.Getppid()) }
func Getuid() uint32 { return uint32(os.Getuid()) }
func Geteuid() uint32 {
	return uint32(os.Geteuid())
}
func Getgid() uint32  { return uint32(os.Getgid()) }
func Getegid() uint32 { return uint32(os.Getegid()) }

// Isatty is isatty: whether fd is a terminal (errno ENOTTY if not).
func Isatty(fd int32) int32 {
	f := fdOS(fd)
	if f == nil {
		if Errno() == ESPIPE {
			SetErrno(ENOTTY)
		}
		return 0
	}
	if !isatty(f) {
		SetErrno(ENOTTY)
		return 0
	}
	return 1
}

// Getcwd is getcwd, which allocates the array if buf is null (as glibc:
// of size bytes, or as many as it takes if size is 0).
func Getcwd(buf Ptr, size uint64) Ptr {
	wd, err := os.Getwd()
	if err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	n := uint64(len(wd) + 1)
	if Addr(buf) == 0 {
		if size == 0 {
			size = n
		}
		if n > size {
			SetErrno(ERANGE)
			return Ptr{}
		}
		buf = Malloc(size)
	} else if size == 0 {
		SetErrno(EINVAL)
		return Ptr{}
	} else if n > size {
		SetErrno(ERANGE)
		return Ptr{}
	}
	copy(bytesAt(buf, uintptr(n)), wd+"\x00")
	return buf
}

func Chdir(path Ptr) int32 { return errRet(os.Chdir(GoString(path))) }

func Sleep(s uint32) uint32 {
	time.Sleep(time.Duration(s) * time.Second)
	return 0
}

func Usleep(us uint32) int32 {
	time.Sleep(time.Duration(us) * time.Microsecond)
	return 0
}

// ExitRaw is _exit: the program ends at once, without flushing C's
// streams.
func ExitRaw(status int32) { os.Exit(int(status)) }

func Dup(fd int32) int32 { return dupFrom(fd, 0, false) }

// dupFrom makes the lowest free descriptor from min on one of fd's file.
func dupFrom(fd, min int32, cloexec bool) int32 {
	fdMu.Lock()
	defer fdMu.Unlock()
	e := fdEnt(fd)
	if e == nil || min < 0 {
		SetErrno(If(e == nil, int32(EBADF), EINVAL))
		return -1
	}
	e.of.refs++
	return fdInstall(&fdEntry{of: e.of, cloexec: cloexec}, min)
}

func Dup2(fd, fd2 int32) int32 {
	fdMu.Lock()
	defer fdMu.Unlock()
	e := fdEnt(fd)
	if e == nil || fd2 < 0 || fd2 >= 1<<20 {
		SetErrno(EBADF)
		return -1
	}
	if fd == fd2 {
		return fd2
	}
	for int(fd2) >= len(fdTab) {
		fdTab = append(fdTab, nil)
	}
	if old := fdTab[fd2]; old != nil {
		old.of.release()
	}
	e.of.refs++
	fdTab[fd2] = &fdEntry{of: e.of}
	return fd2
}

// Pipe is pipe: fds[0] reads what fds[1] writes.
func Pipe(fds Ptr) int32 {
	r, w, err := os.Pipe()
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	Store32(fds, uint32(fdNew(r, O_RDONLY)))
	Store32(Add(fds, 4), uint32(fdNew(w, O_WRONLY)))
	return 0
}

// sysconf's names.
const (
	_SC_CLK_TCK          = 2
	_SC_PAGESIZE         = 30
	_SC_NPROCESSORS_CONF = 83
	_SC_NPROCESSORS_ONLN = 84
)

func Sysconf(name int32) int64 {
	switch name {
	case _SC_CLK_TCK:
		return 100
	case _SC_PAGESIZE:
		return int64(os.Getpagesize())
	case _SC_NPROCESSORS_CONF, _SC_NPROCESSORS_ONLN:
		return int64(runtime.NumCPU())
	}
	SetErrno(EINVAL)
	return -1
}

// Gethostname is gethostname: a name that does not fit is truncated,
// and the result is -1 with errno ENAMETOOLONG, as glibc.
func Gethostname(name Ptr, n uint64) int32 {
	h, err := os.Hostname()
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	b := append([]byte(h), 0)
	if uint64(len(b)) > n {
		copy(bytesAt(name, uintptr(n)), b)
		SetErrno(ENAMETOOLONG)
		return -1
	}
	copy(bytesAt(name, uintptr(len(b))), b)
	return 0
}

// fcntl.h

// Open is open: the mode, if flags has O_CREAT, is its third argument.
func Open(path Ptr, flags int32, va ...any) int32 {
	var mode uint32
	if flags&O_CREAT != 0 && len(va) > 0 {
		mode = uint32(toInt64(va[0]))
	}
	return open(GoString(path), flags, mode)
}

func Creat(path Ptr, mode uint32) int32 {
	return open(GoString(path), O_WRONLY|O_CREAT|O_TRUNC, mode)
}

func open(name string, flags int32, mode uint32) int32 {
	var flag int
	switch flags & O_ACCMODE {
	case O_RDONLY:
		flag = os.O_RDONLY
	case O_WRONLY:
		flag = os.O_WRONLY
	case O_RDWR:
		flag = os.O_RDWR
	default:
		SetErrno(EINVAL)
		return -1
	}
	for _, m := range [...]struct {
		c int32
		g int
	}{{O_CREAT, os.O_CREATE}, {O_EXCL, os.O_EXCL}, {O_TRUNC, os.O_TRUNC}, {O_APPEND, os.O_APPEND}, {O_SYNC, os.O_SYNC}} {
		if flags&m.c == m.c {
			flag |= m.g
		}
	}
	if flags&O_NOFOLLOW != 0 {
		if fi, err := os.Lstat(name); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			SetErrno(ELOOP)
			return -1
		}
	}
	f, err := os.OpenFile(name, flag, goMode(mode))
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	if flags&O_DIRECTORY != 0 {
		if fi, err := f.Stat(); err != nil || !fi.IsDir() {
			f.Close()
			SetErrno(ENOTDIR)
			return -1
		}
	}
	kept := flags &^ (O_CREAT | O_EXCL | O_NOCTTY | O_TRUNC | O_CLOEXEC)
	fdMu.Lock()
	defer fdMu.Unlock()
	return fdInstall(&fdEntry{of: &ofile{f: f, refs: 1, flags: kept | O_LARGEFILE}, cloexec: flags&O_CLOEXEC != 0}, 0)
}

// goMode is the os.FileMode of C's permission bits.
func goMode(mode uint32) os.FileMode {
	m := os.FileMode(mode & 0o777)
	if mode&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if mode&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if mode&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}

// Fcntl is fcntl, for F_DUPFD, F_DUPFD_CLOEXEC, F_GETFD, F_SETFD (FD_CLOEXEC,
// which only F_GETFD reads back), F_GETFL and F_SETFL (O_APPEND and
// O_NONBLOCK, which only F_GETFL reads back), and the advisory record locks
// F_GETLK, F_SETLK and F_SETLKW (the system's, where it has them).
func Fcntl(fd, cmd int32, va ...any) int32 {
	var arg int32
	if len(va) > 0 && cmd != F_GETLK && cmd != F_SETLK && cmd != F_SETLKW {
		arg = int32(toInt64(va[0]))
	}
	switch cmd {
	case F_DUPFD, F_DUPFD_CLOEXEC:
		return dupFrom(fd, arg, cmd == F_DUPFD_CLOEXEC)
	case F_GETLK, F_SETLK, F_SETLKW:
		return fcntlLock(fd, cmd, VaArg[Ptr](VaStart(va)))
	}
	fdMu.Lock()
	defer fdMu.Unlock()
	e := fdEnt(fd)
	if e == nil {
		SetErrno(EBADF)
		return -1
	}
	switch cmd {
	case F_GETFD:
		return B2I(e.cloexec)
	case F_SETFD:
		e.cloexec = arg&FD_CLOEXEC != 0
		return 0
	case F_GETFL:
		return e.of.flags
	case F_SETFL:
		const settable = O_APPEND | O_NONBLOCK
		e.of.flags = e.of.flags&^settable | arg&settable
		return 0
	}
	SetErrno(EINVAL)
	return -1
}
