// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"
)

// sys/stat.h's file types and modes: Linux's.
const (
	S_IFMT   = 0o170000
	S_IFSOCK = 0o140000
	S_IFLNK  = 0o120000
	S_IFREG  = 0o100000
	S_IFBLK  = 0o060000
	S_IFDIR  = 0o040000
	S_IFCHR  = 0o020000
	S_IFIFO  = 0o010000
)

// cMode is the st_mode of a file of Go's mode m.
func cMode(m fs.FileMode) uint32 {
	r := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		r |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		r |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		r |= 0o1000
	}
	switch {
	case m.IsDir():
		r |= S_IFDIR
	case m&fs.ModeSymlink != 0:
		r |= S_IFLNK
	case m&fs.ModeNamedPipe != 0:
		r |= S_IFIFO
	case m&fs.ModeSocket != 0:
		r |= S_IFSOCK
	case m&fs.ModeCharDevice != 0:
		r |= S_IFCHR
	case m&fs.ModeDevice != 0:
		r |= S_IFBLK
	default:
		r |= S_IFREG
	}
	return r
}

// sysField returns the integer field of the system's stat structure
// (fi.Sys(), a syscall.Stat_t on Unix) that has one of the names.
func sysField(sys reflect.Value, names ...string) (uint64, bool) {
	if !sys.IsValid() {
		return 0, false
	}
	for _, n := range names {
		f := sys.FieldByName(n)
		switch {
		case !f.IsValid():
		case f.CanInt():
			return uint64(f.Int()), true
		case f.CanUint():
			return f.Uint(), true
		}
	}
	return 0, false
}

// sysTime returns a timespec field of the system's stat structure: one
// of the names, a struct of Sec and Nsec (Linux's Atim, Darwin's
// Atimespec), or name and name+"Nsec" (js's Atime and AtimeNsec).
func sysTime(sys reflect.Value, names ...string) (sec, nsec int64, ok bool) {
	if !sys.IsValid() {
		return 0, 0, false
	}
	for _, n := range names {
		f := sys.FieldByName(n)
		if f.IsValid() && f.Kind() == reflect.Struct {
			s, ok1 := sysField(f, "Sec")
			ns, ok2 := sysField(f, "Nsec")
			if ok1 && ok2 {
				return int64(s), int64(ns), true
			}
		}
	}
	if s, ok := sysField(sys, names[0]); ok {
		if ns, ok := sysField(sys, names[0]+"Nsec"); ok {
			return int64(s), int64(ns), true
		}
	}
	return 0, 0, false
}

// fillStat stores fi as Linux's struct stat at buf:
//
//	dev_t st_dev; ino_t st_ino; nlink_t st_nlink;	// 0, 8, 16
//	mode_t st_mode; uid_t st_uid; gid_t st_gid;	// 24, 28, 32
//	dev_t st_rdev; off_t st_size;			// 40, 48
//	blksize_t st_blksize; blkcnt_t st_blocks;	// 56, 64
//	struct timespec st_atim, st_mtim, st_ctim;	// 72, 88, 104
//	long __unused[3];				// 120 (144 bytes)
//
// What the system's stat structure does not have is made up: one link,
// 4096-byte blocks, and the modification time for the others.
func fillStat(buf Ptr, fi fs.FileInfo) {
	Memset(buf, 0, 144)
	sys := reflect.Indirect(reflect.ValueOf(fi.Sys()))
	if sys.IsValid() && sys.Kind() != reflect.Struct {
		sys = reflect.Value{}
	}
	get := func(def uint64, names ...string) uint64 {
		if v, ok := sysField(sys, names...); ok {
			return v
		}
		return def
	}
	mode := cMode(fi.Mode())
	if m, ok := sysField(sys, "Mode"); ok && m&S_IFMT != 0 {
		mode = uint32(m)
	}
	size := fi.Size()
	Store64(buf, get(0, "Dev"))
	Store64(Add(buf, 8), get(0, "Ino"))
	Store64(Add(buf, 16), get(1, "Nlink"))
	Store32(Add(buf, 24), mode)
	Store32(Add(buf, 28), uint32(get(uint64(uint32(os.Getuid())), "Uid")))
	Store32(Add(buf, 32), uint32(get(uint64(uint32(os.Getgid())), "Gid")))
	Store64(Add(buf, 40), get(0, "Rdev"))
	Store64(Add(buf, 48), uint64(size))
	storeLong(Add(buf, 56), int64(get(4096, "Blksize")))
	Store64(Add(buf, 64), get(uint64(size+511)/512, "Blocks"))
	mt := fi.ModTime()
	ts := func(off int, names ...string) {
		s, ns, ok := sysTime(sys, names...)
		if !ok {
			s, ns = mt.Unix(), int64(mt.Nanosecond())
		}
		Store64(Add(buf, off), uint64(s))
		storeLong(Add(buf, off+8), int64(ns))
	}
	ts(72, "Atim", "Atimespec", "Atime")
	ts(88, "Mtim", "Mtimespec", "Mtime")
	ts(104, "Ctim", "Ctimespec", "Ctime")
}

func statRet(buf Ptr, fi fs.FileInfo, err error) int32 {
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	fillStat(buf, fi)
	return 0
}

func Stat(path, buf Ptr) int32 {
	fi, err := os.Stat(GoString(path))
	return statRet(buf, fi, err)
}

func Lstat(path, buf Ptr) int32 {
	fi, err := os.Lstat(GoString(path))
	return statRet(buf, fi, err)
}

func Fstat(fd int32, buf Ptr) int32 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	fi, err := f.Stat()
	return statRet(buf, fi, err)
}

func Mkdir(path Ptr, mode uint32) int32 {
	return errRet(os.Mkdir(GoString(path), goMode(mode)))
}

func Chmod(path Ptr, mode uint32) int32 {
	return errRet(os.Chmod(GoString(path), goMode(mode)))
}

func Fchmod(fd int32, mode uint32) int32 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	return errRet(f.Chmod(goMode(mode)))
}

// Umask is umask: the system's, where it has one; elsewhere, only
// recorded.
func Umask(mask uint32) uint32 { return umask(mask & 0o777) }

// dirent.h

// d_type's values.
const (
	DT_UNKNOWN = 0
	DT_FIFO    = 1
	DT_CHR     = 2
	DT_DIR     = 4
	DT_BLK     = 6
	DT_REG     = 8
	DT_LNK     = 10
	DT_SOCK    = 12
)

// A Dir is DIR: a directory's entries, read when opened (and again when
// rewound), with "." and ".." first as the system lists them.
type Dir struct {
	path   string
	ents   []fs.DirEntry
	i      int
	ent    Ptr // the struct dirent readdir returns
	closed bool
}

var dirInfo = tinfoOf[Dir]()

func dirOf(p Ptr) *Dir {
	if p.obj == nil || p.info() != dirInfo || p.off != 0 {
		fault("not a DIR", Addr(p))
	}
	d := (*Dir)(p.obj)
	if d.closed {
		fault("use of a closed DIR", Addr(p))
	}
	return d
}

// dotEntry is the DirEntry of . or .. in a directory.
type dotEntry struct {
	name string
	path string
}

func (e dotEntry) Name() string               { return e.name }
func (e dotEntry) IsDir() bool                { return true }
func (e dotEntry) Type() fs.FileMode          { return fs.ModeDir }
func (e dotEntry) Info() (fs.FileInfo, error) { return os.Stat(e.path) }

func (d *Dir) read() error {
	ents, err := os.ReadDir(d.path)
	if err != nil {
		return err
	}
	d.ents = append([]fs.DirEntry{
		dotEntry{".", d.path},
		dotEntry{"..", filepath.Join(d.path, "..")},
	}, ents...)
	d.i = 0
	return nil
}

func Opendir(path Ptr) Ptr {
	name := GoString(path)
	fi, err := os.Stat(name)
	if err == nil && !fi.IsDir() {
		err = errnoErr(ENOTDIR)
	}
	p := New[Dir]()
	d := (*Dir)(p.obj)
	d.path = name
	if err == nil {
		err = d.read()
	}
	if err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	d.ent = Malloc(280)
	return p
}

// Readdir is readdir: the next entry, in an array the next call reuses,
// or null at the end:
//
//	ino_t d_ino; off_t d_off; unsigned short d_reclen;	// 0, 8, 16
//	unsigned char d_type; char d_name[256];		// 18, 19 (280 bytes)
func Readdir(p Ptr) Ptr {
	d := dirOf(p)
	if d.i >= len(d.ents) {
		return Ptr{}
	}
	e := d.ents[d.i]
	d.i++
	var ino uint64
	if fi, err := e.Info(); err == nil {
		ino, _ = sysField(reflect.Indirect(reflect.ValueOf(fi.Sys())), "Ino")
	}
	typ := uint8(DT_UNKNOWN)
	switch t := e.Type(); {
	case t.IsDir():
		typ = DT_DIR
	case t&fs.ModeSymlink != 0:
		typ = DT_LNK
	case t&fs.ModeNamedPipe != 0:
		typ = DT_FIFO
	case t&fs.ModeSocket != 0:
		typ = DT_SOCK
	case t&fs.ModeCharDevice != 0:
		typ = DT_CHR
	case t&fs.ModeDevice != 0:
		typ = DT_BLK
	case t.IsRegular():
		typ = DT_REG
	}
	name := e.Name()
	if len(name) > 255 {
		name = name[:255]
	}
	Store64(d.ent, ino)
	Store64(Add(d.ent, 8), uint64(d.i))
	Store16(Add(d.ent, 16), uint16((19+len(name)+1+7)&^7))
	Store8(Add(d.ent, 18), typ)
	copy(bytesAt(Add(d.ent, 19), uintptr(len(name)+1)), name+"\x00")
	return d.ent
}

func Closedir(p Ptr) int32 {
	dirOf(p).closed = true
	return 0
}

func Rewinddir(p Ptr) {
	d := dirOf(p)
	if d.read() != nil {
		d.ents, d.i = nil, 0
	}
}

// sys/time.h

// Gettimeofday is gettimeofday, which stores the time as a struct timeval
// {time_t tv_sec; suseconds_t tv_usec;} at tv.
func Gettimeofday(tv, tz Ptr) int32 {
	if Addr(tv) != 0 {
		now := time.Now()
		Store64(tv, uint64(now.Unix()))
		Store64(Add(tv, 8), uint64(now.Nanosecond()/1000))
	}
	if Addr(tz) != 0 {
		// struct timezone {int tz_minuteswest; int tz_dsttime;}: UTC, as glibc.
		Store32(tz, 0)
		Store32(Add(tz, 4), 0)
	}
	return 0
}

// stdlib.h's POSIX functions

func Setenv(name, value Ptr, overwrite int32) int32 {
	n := GoString(name)
	if !envName(n) {
		return -1
	}
	if _, ok := os.LookupEnv(n); ok && overwrite == 0 {
		return 0
	}
	return errRet(os.Setenv(n, GoString(value)))
}

func Unsetenv(name Ptr) int32 {
	n := GoString(name)
	if !envName(n) {
		return -1
	}
	return errRet(os.Unsetenv(n))
}

// envName reports whether n may name an environment variable: not empty
// and without '=' (errno EINVAL).
func envName(n string) bool {
	if n == "" || slices.Contains([]byte(n), '=') {
		SetErrno(EINVAL)
		return false
	}
	return true
}

var (
	mkstempMu sync.Mutex
	mkstempN  uint64
)

// Mkstemp is mkstemp: it replaces the XXXXXX that ends template with what
// makes the name of a new file, which it creates (mode 0600) and opens.
func Mkstemp(template Ptr) int32 {
	n := Strlen(template)
	if n < 6 || GoString(Add(template, int(n-6))) != "XXXXXX" {
		SetErrno(EINVAL)
		return -1
	}
	suffix := Add(template, int(n-6))
	const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for range 238328 { // TMP_MAX
		mkstempMu.Lock()
		mkstempN++
		x := uint64(os.Getpid())*2654435761 + mkstempN*0x9e3779b97f4a7c15 + uint64(time.Now().UnixNano())
		mkstempMu.Unlock()
		for i := range 6 {
			Store8(Add(suffix, i), digits[x%62])
			x /= 62
		}
		fd := open(GoString(template), O_RDWR|O_CREAT|O_EXCL, 0o600)
		if fd >= 0 || Errno() != EEXIST {
			return fd
		}
	}
	SetErrno(EEXIST)
	return -1
}

// Realpath is realpath: the absolute name of an existing file, without
// symbolic links, ., .. or extra slashes, at resolved or, if it is null,
// in an array realpath allocates.
func Realpath(path, resolved Ptr) Ptr {
	name := GoString(path)
	if _, err := os.Stat(name); err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	abs, err := filepath.Abs(name)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	if len(abs) >= 4096 { // PATH_MAX
		SetErrno(ENAMETOOLONG)
		return Ptr{}
	}
	if Addr(resolved) == 0 {
		return CString(abs)
	}
	copy(bytesAt(resolved, uintptr(len(abs)+1)), abs+"\x00")
	return resolved
}

// AlignedAlloc is aligned_alloc: size bytes of untyped memory, at a
// multiple of align, which must be a power of two (errno EINVAL).
func AlignedAlloc(align, size uint64) Ptr {
	if align == 0 || align&(align-1) != 0 {
		SetErrno(EINVAL)
		return Ptr{}
	}
	if align <= 8 {
		return Malloc(size)
	}
	return mallocAligned(uintptr(align), uintptr(size))
}

// PosixMemalign is posix_memalign: aligned_alloc, for an alignment that
// is a power of two and a multiple of sizeof(void *), into *memptr.
func PosixMemalign(memptr Ptr, align, size uint64) int32 {
	if align < 8 || align&(align-1) != 0 {
		return EINVAL
	}
	StorePtr(memptr, AlignedAlloc(align, size))
	return 0
}
