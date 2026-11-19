// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !ape && !apegen

package c23

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// More of POSIX: what SQLite's Unix VFS and shell use.

// unistd.h

// Ftruncate is ftruncate.
func Ftruncate(fd int32, length int64) int32 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	if length < 0 {
		SetErrno(EINVAL)
		return -1
	}
	return errRet(f.Truncate(length))
}

// Truncate is truncate.
func Truncate(path Ptr, length int64) int32 {
	if length < 0 {
		SetErrno(EINVAL)
		return -1
	}
	return errRet(os.Truncate(GoString(path), length))
}

// Fsync is fsync.
func Fsync(fd int32) int32 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	return errRet(f.Sync())
}

// Fdatasync is fdatasync, which syncs the metadata too.
func Fdatasync(fd int32) int32 { return Fsync(fd) }

// cid is the int os takes for a uid_t or gid_t: -1 (unchanged) for
// (uid_t)-1.
func cid(id uint32) int { return int(int32(id)) }

func Chown(path Ptr, uid, gid uint32) int32 {
	return errRet(os.Chown(GoString(path), cid(uid), cid(gid)))
}

func Lchown(path Ptr, uid, gid uint32) int32 {
	return errRet(os.Lchown(GoString(path), cid(uid), cid(gid)))
}

func Fchown(fd int32, uid, gid uint32) int32 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	return errRet(f.Chown(cid(uid), cid(gid)))
}

// Readlink is readlink: the link's contents, truncated to n bytes and not
// terminated.
func Readlink(path, buf Ptr, n uint64) int64 {
	s, err := os.Readlink(GoString(path))
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	k := min(uint64(len(s)), n)
	copy(bytesAt(buf, uintptr(k)), s)
	return int64(k)
}

func Symlink(target, link Ptr) int32 {
	return errRet(os.Symlink(GoString(target), GoString(link)))
}

func Link(old, new Ptr) int32 {
	return errRet(os.Link(GoString(old), GoString(new)))
}

// Pread is pread: it reads at off, and the file's offset stays.
func Pread(fd int32, buf Ptr, n uint64, off int64) int64 {
	f := fdOS(fd)
	if f == nil {
		return -1
	}
	if off < 0 {
		SetErrno(EINVAL)
		return -1
	}
	k, err := f.ReadAt(bytesAt(buf, uintptr(n)), off)
	if err != nil && err != io.EOF {
		setErrnoOf(err)
		return -1
	}
	return int64(k)
}

// Pwrite is pwrite: it writes at off, and the file's offset stays. A file
// opened O_APPEND is appended to, as Linux does.
func Pwrite(fd int32, buf Ptr, n uint64, off int64) int64 {
	of := fdFile(fd)
	if of == nil {
		SetErrno(EBADF)
		return -1
	}
	if off < 0 {
		SetErrno(EINVAL)
		return -1
	}
	b := bytesAt(buf, uintptr(n))
	var k int
	var err error
	if of.flags&O_APPEND != 0 {
		k, err = of.f.Write(b)
	} else {
		k, err = of.f.WriteAt(b, off)
	}
	if err != nil && k == 0 {
		setErrnoOf(err)
		return -1
	}
	return int64(k)
}

func Getpagesize() int32 { return int32(os.Getpagesize()) }

// fcntl.h's advisory record locks.

// fcntl's lock commands and struct flock's types: Linux's.
const (
	F_GETLK  = 5
	F_SETLK  = 6
	F_SETLKW = 7
	F_RDLCK  = 0
	F_WRLCK  = 1
	F_UNLCK  = 2
)

// A flock is C's struct flock {short l_type; short l_whence; off_t
// l_start; off_t l_len; pid_t l_pid;}, which is at offsets 0, 2, 8, 16
// and 24.
type flock struct {
	typ, whence int16
	start, len  int64
	pid         int32
}

// fcntlLock is fcntl's F_GETLK, F_SETLK and F_SETLKW of the struct flock
// at p.
func fcntlLock(fd, cmd int32, p Ptr) int32 {
	of := fdFile(fd)
	if of == nil {
		SetErrno(EBADF)
		return -1
	}
	l := flock{
		typ:    int16(Load16(p)),
		whence: int16(Load16(Add(p, 2))),
		start:  int64(Load64(Add(p, 8))),
		len:    int64(Load64(Add(p, 16))),
	}
	if l.typ < F_RDLCK || l.typ > F_UNLCK || l.whence < 0 || l.whence > 2 {
		SetErrno(EINVAL)
		return -1
	}
	acc := of.flags & O_ACCMODE
	if cmd != F_GETLK && (l.typ == F_RDLCK && acc == O_WRONLY || l.typ == F_WRLCK && acc == O_RDONLY) {
		SetErrno(EBADF)
		return -1
	}
	if err := lockFile(of.f, cmd, &l); err != nil {
		setErrnoOf(err)
		return -1
	}
	if cmd == F_GETLK {
		Store16(p, uint16(l.typ))
		if l.typ != F_UNLCK {
			Store16(Add(p, 2), uint16(l.whence))
			Store64(Add(p, 8), uint64(l.start))
			Store64(Add(p, 16), uint64(l.len))
			Store32(Add(p, 24), uint32(l.pid))
		}
	}
	return 0
}

// sys/ioctl.h

// ioctl's requests: Linux's.
const (
	TIOCGWINSZ = 0x5413
	FIONREAD   = 0x541B
	FIONBIO    = 0x5421
	FIONCLEX   = 0x5450
	FIOCLEX    = 0x5451
)

// Ioctl is ioctl, for FIONREAD (the bytes a read would return: what is
// left of a regular file, or what the system says of a pipe or terminal),
// TIOCGWINSZ (a terminal's struct winsize), FIONBIO, FIOCLEX and
// FIONCLEX. Other requests fail with ENOTTY.
func Ioctl(fd int32, req uint64, va ...any) int32 {
	var arg Ptr
	if len(va) > 0 {
		arg = VaArg[Ptr](VaStart(va))
	}
	of := fdFile(fd)
	if of == nil {
		SetErrno(EBADF)
		return -1
	}
	switch req {
	case FIONREAD:
		var n int64
		if fi, err := of.f.Stat(); err == nil && fi.Mode().IsRegular() {
			pos, err := of.f.Seek(0, io.SeekCurrent)
			if err != nil {
				setErrnoOf(err)
				return -1
			}
			n = max(fi.Size()-pos, 0)
		} else {
			var err error
			if n, err = ioctlInRead(of.f); err != nil {
				setErrnoOf(err)
				return -1
			}
		}
		Store32(arg, uint32(n))
		return 0
	case TIOCGWINSZ:
		ws, err := ioctlWinsize(of.f)
		if err != nil {
			setErrnoOf(err)
			return -1
		}
		for i, v := range ws {
			Store16(Add(arg, 2*i), v)
		}
		return 0
	case FIONBIO:
		fdMu.Lock()
		defer fdMu.Unlock()
		if Load32(arg) != 0 {
			of.flags |= O_NONBLOCK
		} else {
			of.flags &^= O_NONBLOCK
		}
		return 0
	case FIOCLEX, FIONCLEX:
		fdMu.Lock()
		defer fdMu.Unlock()
		if e := fdEnt(fd); e != nil {
			e.cloexec = req == FIOCLEX
		}
		return 0
	}
	SetErrno(ENOTTY)
	return -1
}

// stdio.h's popen and pclose

var (
	popenMu sync.Mutex
	popens  = map[*File]*exec.Cmd{}
)

// Popen is popen: a stream that reads the standard output of the shell's
// command (mode "r") or writes its standard input ("w"). The command's
// other standard files are the program's.
func Popen(command, mode Ptr) Ptr {
	m := GoString(mode)
	rd := strings.HasPrefix(m, "r")
	if !rd && !strings.HasPrefix(m, "w") || strings.Trim(m[1:], "e") != "" {
		SetErrno(EINVAL)
		return Ptr{}
	}
	r, w, err := os.Pipe()
	if err != nil {
		setErrnoOf(err)
		return Ptr{}
	}
	cmd := exec.Command("/bin/sh", "-c", GoString(command))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	mine, theirs := r, w
	if rd {
		cmd.Stdout = w
	} else {
		cmd.Stdin = r
		mine, theirs = w, r
	}
	err = cmd.Start()
	theirs.Close()
	if err != nil {
		mine.Close()
		setErrnoOf(err)
		return Ptr{}
	}
	p := newFile(fdNew(mine, If[int32](rd, O_RDONLY, O_WRONLY)), rd, !rd)
	popenMu.Lock()
	popens[fileOf(p)] = cmd
	popenMu.Unlock()
	return p
}

// Pclose is pclose: it closes the stream and waits for its command, whose
// status it returns (as wait does), or -1 (errno ECHILD) for a stream
// popen did not open.
func Pclose(p Ptr) int32 {
	f := fileOf(p)
	popenMu.Lock()
	cmd := popens[f]
	delete(popens, f)
	popenMu.Unlock()
	if cmd == nil {
		SetErrno(ECHILD)
		return -1
	}
	Fclose(p)
	return waitStatus(cmd, cmd.Wait())
}

// time.h's POSIX functions

// clock_gettime's clocks.
const (
	CLOCK_REALTIME           = 0
	CLOCK_MONOTONIC          = 1
	CLOCK_PROCESS_CPUTIME_ID = 2
	CLOCK_THREAD_CPUTIME_ID  = 3
)

func storeTimespec(ts Ptr, sec, nsec int64) {
	Store64(ts, uint64(sec))
	storeLong(Add(ts, 8), nsec)
}

// ClockGettime is clock_gettime. The processor-time clocks give the time
// since the program started, as clock does.
func ClockGettime(id int32, ts Ptr) int32 {
	var d time.Duration
	switch id {
	case CLOCK_REALTIME:
		now := time.Now()
		storeTimespec(ts, now.Unix(), int64(now.Nanosecond()))
		return 0
	case CLOCK_MONOTONIC, CLOCK_PROCESS_CPUTIME_ID, CLOCK_THREAD_CPUTIME_ID:
		d = time.Since(start)
	default:
		SetErrno(EINVAL)
		return -1
	}
	storeTimespec(ts, int64(d/time.Second), int64(d%time.Second))
	return 0
}

// ClockGetres is clock_getres: a nanosecond, for each clock.
func ClockGetres(id int32, ts Ptr) int32 {
	if id < CLOCK_REALTIME || id > CLOCK_THREAD_CPUTIME_ID {
		SetErrno(EINVAL)
		return -1
	}
	if Addr(ts) != 0 {
		storeTimespec(ts, 0, 1)
	}
	return 0
}

// Nanosleep is nanosleep, which is not interrupted, so it does not store
// what is left at rem.
func Nanosleep(req, rem Ptr) int32 {
	sec, nsec := int64(Load64(req)), loadLong(Add(req, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		SetErrno(EINVAL)
		return -1
	}
	time.Sleep(time.Duration(sec)*time.Second + time.Duration(nsec))
	return 0
}

// utime.h, sys/time.h's utimes

// Utime is utime: times is a struct utimbuf {time_t actime; time_t
// modtime;}, or null for now.
func Utime(path, times Ptr) int32 {
	a, m := time.Now(), time.Now()
	if Addr(times) != 0 {
		a = time.Unix(int64(Load64(times)), 0)
		m = time.Unix(int64(Load64(Add(times, 8))), 0)
	}
	return errRet(os.Chtimes(GoString(path), a, m))
}

// Utimes is utimes: tv is two struct timevals, the access and
// modification times, or null for now.
func Utimes(path, tv Ptr) int32 {
	a, m := time.Now(), time.Now()
	if Addr(tv) != 0 {
		var ts [2]time.Time
		for i := range ts {
			sec, usec := int64(Load64(Add(tv, 16*i))), int64(Load64(Add(tv, 16*i+8)))
			if usec < 0 || usec >= 1e6 {
				SetErrno(EINVAL)
				return -1
			}
			ts[i] = time.Unix(sec, usec*1000)
		}
		a, m = ts[0], ts[1]
	}
	return errRet(os.Chtimes(GoString(path), a, m))
}

// pwd.h

// passwd is C's struct passwd.
type passwd struct {
	name, passwd unsafe.Pointer
	uid, gid     uint32
	gecos, dir   unsafe.Pointer
	shell        unsafe.Pointer
}

var (
	pwMu  sync.Mutex
	pwBuf Ptr
)

// pwEntry is a user: the fields of a line of /etc/passwd.
type pwEntry [7]string

// lookupUser finds the user of the name, or of uid if the name is "",
// in /etc/passwd, or else the program's own user as the environment
// tells of it.
func lookupUser(name string, uid uint32) (pwEntry, bool) {
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			e := strings.Split(s.Text(), ":")
			if len(e) != 7 || strings.HasPrefix(e[0], "#") {
				continue
			}
			if name != "" && e[0] == name {
				return pwEntry(e), true
			}
			if name == "" {
				if u, err := strconv.ParseUint(e[2], 10, 32); err == nil && uint32(u) == uid {
					return pwEntry(e), true
				}
			}
		}
	}
	me := os.Getenv("USER")
	if me == "" {
		me = os.Getenv("USERNAME")
	}
	if me == "" || (name != "" && name != me) || (name == "" && uid != uint32(os.Getuid())) {
		return pwEntry{}, false
	}
	home, _ := os.UserHomeDir()
	return pwEntry{me, "x", strconv.FormatUint(uint64(uint32(os.Getuid())), 10),
		strconv.FormatUint(uint64(uint32(os.Getgid())), 10), "", home, os.Getenv("SHELL")}, true
}

// pwResult returns the static struct passwd of e, or null (errno
// unchanged: no such user).
func pwResult(e pwEntry, ok bool) Ptr {
	if !ok {
		return Ptr{}
	}
	pwMu.Lock()
	defer pwMu.Unlock()
	if pwBuf.obj == nil {
		pwBuf = New[passwd]()
	}
	uid, _ := strconv.ParseUint(e[2], 10, 32)
	gid, _ := strconv.ParseUint(e[3], 10, 32)
	var pw passwd
	StorePtr(pwBuf, CString(e[0]))
	StorePtr(Add(pwBuf, int(unsafe.Offsetof(pw.passwd))), CString(e[1]))
	Store32(Add(pwBuf, int(unsafe.Offsetof(pw.uid))), uint32(uid))
	Store32(Add(pwBuf, int(unsafe.Offsetof(pw.gid))), uint32(gid))
	StorePtr(Add(pwBuf, int(unsafe.Offsetof(pw.gecos))), CString(e[4]))
	StorePtr(Add(pwBuf, int(unsafe.Offsetof(pw.dir))), CString(e[5]))
	StorePtr(Add(pwBuf, int(unsafe.Offsetof(pw.shell))), CString(e[6]))
	return pwBuf
}

// Getpwuid is getpwuid.
func Getpwuid(uid uint32) Ptr { return pwResult(lookupUser("", uid)) }

// Getpwnam is getpwnam.
func Getpwnam(name Ptr) Ptr {
	n := GoString(name)
	if n == "" {
		return Ptr{}
	}
	return pwResult(lookupUser(n, 0))
}

// sys/resource.h

const (
	RUSAGE_SELF     = 0
	RUSAGE_CHILDREN = -1
	RUSAGE_THREAD   = 1
)

// Getrusage is getrusage: the system's, where it has it (RUSAGE_THREAD is
// the process's); elsewhere, the time since the program started as its
// user time. usage is a struct rusage: two struct timevals and 14 longs.
func Getrusage(who int32, usage Ptr) int32 {
	if who != RUSAGE_SELF && who != RUSAGE_CHILDREN && who != RUSAGE_THREAD {
		SetErrno(EINVAL)
		return -1
	}
	ru, err := rusage(who)
	if err != nil {
		setErrnoOf(err)
		return -1
	}
	for i, v := range ru {
		if i < 4 {
			Store64(Add(usage, 8*i), uint64(v)) // time_t and suseconds_t
		} else {
			storeLong(Add(usage, 32+int(longSize)*(i-4)), v)
		}
	}
	return 0
}

// sys/mman.h

const (
	PROT_NONE     = 0
	PROT_READ     = 1
	PROT_WRITE    = 2
	PROT_EXEC     = 4
	MAP_SHARED    = 0x01
	MAP_PRIVATE   = 0x02
	MAP_FIXED     = 0x10
	MAP_ANONYMOUS = 0x20
)

// mapFailed is MAP_FAILED, (void *)-1.
var mapFailed = FromInt(^uintptr(0))

// Mmap is mmap, of memory C allocates: an anonymous mapping is zeroed
// memory, and a private mapping of a file a copy of what it holds (zeros
// past its end). A shared mapping of a file fails (ENODEV), as does
// MAP_FIXED (EINVAL); prot is not enforced.
func Mmap(addr Ptr, length uint64, prot, flags, fd int32, off int64) Ptr {
	page := int64(os.Getpagesize())
	share := flags & (MAP_SHARED | MAP_PRIVATE)
	if length == 0 || share == 0 || share == MAP_SHARED|MAP_PRIVATE || flags&MAP_FIXED != 0 || off < 0 || off%page != 0 {
		SetErrno(EINVAL)
		return mapFailed
	}
	if flags&MAP_ANONYMOUS != 0 {
		return mallocAligned(uintptr(page), uintptr(length))
	}
	f := fdOS(fd)
	if f == nil {
		return mapFailed
	}
	if share == MAP_SHARED {
		return mapShared(f, length, off, page)
	}
	p := mallocAligned(uintptr(page), uintptr(length))
	if _, err := f.ReadAt(bytesAt(p, uintptr(length)), off); err != nil && err != io.EOF {
		setErrnoOf(err)
		return mapFailed
	}
	return p
}

// Munmap is munmap: the collector frees the memory when it is no longer
// reachable.
func Munmap(addr Ptr, length uint64) int32 {
	if length == 0 {
		SetErrno(EINVAL)
		return -1
	}
	return 0
}

// Msync is msync: no mapping is shared with a file.
func Msync(addr Ptr, length uint64, flags int32) int32 { return 0 }

// Mprotect is mprotect, which changes nothing.
func Mprotect(addr Ptr, length uint64, prot int32) int32 { return 0 }

// Madvise is madvise, which takes no advice.
func Madvise(addr Ptr, length uint64, advice int32) int32 { return 0 }

// Shared file mappings: within the process, every mapping of the same
// region of the same file is the same memory, read from the file when
// first mapped. What is written to it does not reach the file, and other
// processes do not share it: enough for a file used only to share memory
// in a process, as SQLite's WAL index is when one process uses it.
var (
	sharedMu   sync.Mutex
	sharedMaps = map[sharedKey]Ptr{}
)

type sharedKey struct {
	file     string
	off, len int64
}

func mapShared(f *os.File, length uint64, off, page int64) Ptr {
	name := f.Name()
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	k := sharedKey{name, off, int64(length)}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if p, ok := sharedMaps[k]; ok {
		return p
	}
	p := mallocAligned(uintptr(page), uintptr(length))
	if _, err := f.ReadAt(bytesAt(p, uintptr(length)), off); err != nil && err != io.EOF {
		setErrnoOf(err)
		return mapFailed
	}
	sharedMaps[k] = p
	return p
}
