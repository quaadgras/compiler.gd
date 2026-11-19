// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// The rest of POSIX's files and system interfaces, as musl's headers
// declare them in POSIX mode: the *at functions (fcntl.h, sys/stat.h,
// unistd.h, stdio.h), dirent.h's, unistd.h's descriptors, limits, ids
// and terminals, stdio.h's large offsets and stream locks, sys/
// resource.h's priorities and limits, sys/mman.h's locks and shared
// memory objects, the user and group databases' entries (pwd.h, grp.h),
// utmpx.h, netdb.h's databases, net/if.h's if_nameindex, termios.h's
// window size and stdlib.h's pseudo-terminals. The constants are
// Linux's.

// initPxFiles runs at the end of headers_util.go's init, as it extends
// headers that headers_sock.go, headers_term.go and headers_util.go
// define.
func initPxFiles() {
	extendHeader("fcntl.h", `#define AT_FDCWD (-100)
#define AT_SYMLINK_NOFOLLOW 0x100
#define AT_REMOVEDIR 0x200
#define AT_SYMLINK_FOLLOW 0x400
#define AT_EACCESS 0x200
#define AT_EMPTY_PATH 0x1000
#define POSIX_FADV_NORMAL 0
#define POSIX_FADV_RANDOM 1
#define POSIX_FADV_SEQUENTIAL 2
#define POSIX_FADV_WILLNEED 3
#define POSIX_FADV_DONTNEED 4
#define POSIX_FADV_NOREUSE 5
int openat(int fd, const char *path, int oflag, ...);
int posix_fadvise(int fd, off_t offset, off_t len, int advice);
int posix_fallocate(int fd, off_t offset, off_t len);
`)
	extendHeader("sys/stat.h", `#define UTIME_NOW ((1l << 30) - 1l)
#define UTIME_OMIT ((1l << 30) - 2l)
int fstatat(int fd, const char *restrict path, struct stat *restrict buf, int flag);
int mkdirat(int fd, const char *path, mode_t mode);
int fchmodat(int fd, const char *path, mode_t mode, int flag);
int futimens(int fd, const struct timespec times[2]);
int utimensat(int fd, const char *path, const struct timespec times[2], int flag);
int mkfifo(const char *path, mode_t mode);
int mkfifoat(int fd, const char *path, mode_t mode);
int mknod(const char *path, mode_t mode, dev_t dev);
int mknodat(int fd, const char *path, mode_t mode, dev_t dev);
`)
	extendHeader("unistd.h", `#define _PC_LINK_MAX 0
#define _PC_MAX_CANON 1
#define _PC_MAX_INPUT 2
#define _PC_NAME_MAX 3
#define _PC_PATH_MAX 4
#define _PC_PIPE_BUF 5
#define _PC_CHOWN_RESTRICTED 6
#define _PC_NO_TRUNC 7
#define _PC_VDISABLE 8
#define _PC_SYNC_IO 9
#define _PC_ASYNC_IO 10
#define _PC_PRIO_IO 11
#define _PC_SOCK_MAXBUF 12
#define _PC_FILESIZEBITS 13
#define _PC_REC_INCR_XFER_SIZE 14
#define _PC_REC_MAX_XFER_SIZE 15
#define _PC_REC_MIN_XFER_SIZE 16
#define _PC_REC_XFER_ALIGN 17
#define _PC_ALLOC_SIZE_MIN 18
#define _PC_SYMLINK_MAX 19
#define _PC_2_SYMLINKS 20
#define _CS_PATH 0
#define _CS_POSIX_V6_WIDTH_RESTRICTED_ENVS 1
#define _CS_GNU_LIBC_VERSION 2
#define _CS_GNU_LIBPTHREAD_VERSION 3
#define _CS_POSIX_V5_WIDTH_RESTRICTED_ENVS 4
#define _CS_POSIX_V7_WIDTH_RESTRICTED_ENVS 5
#define _CS_POSIX_V6_ILP32_OFF32_CFLAGS 1116
#define _CS_POSIX_V6_ILP32_OFF32_LDFLAGS 1117
#define _CS_POSIX_V6_ILP32_OFF32_LIBS 1118
#define _CS_POSIX_V6_ILP32_OFF32_LINTFLAGS 1119
#define _CS_POSIX_V6_ILP32_OFFBIG_CFLAGS 1120
#define _CS_POSIX_V6_ILP32_OFFBIG_LDFLAGS 1121
#define _CS_POSIX_V6_ILP32_OFFBIG_LIBS 1122
#define _CS_POSIX_V6_ILP32_OFFBIG_LINTFLAGS 1123
#define _CS_POSIX_V6_LP64_OFF64_CFLAGS 1124
#define _CS_POSIX_V6_LP64_OFF64_LDFLAGS 1125
#define _CS_POSIX_V6_LP64_OFF64_LIBS 1126
#define _CS_POSIX_V6_LP64_OFF64_LINTFLAGS 1127
#define _CS_POSIX_V6_LPBIG_OFFBIG_CFLAGS 1128
#define _CS_POSIX_V6_LPBIG_OFFBIG_LDFLAGS 1129
#define _CS_POSIX_V6_LPBIG_OFFBIG_LIBS 1130
#define _CS_POSIX_V6_LPBIG_OFFBIG_LINTFLAGS 1131
#define _CS_POSIX_V7_ILP32_OFF32_CFLAGS 1132
#define _CS_POSIX_V7_ILP32_OFF32_LDFLAGS 1133
#define _CS_POSIX_V7_ILP32_OFF32_LIBS 1134
#define _CS_POSIX_V7_ILP32_OFF32_LINTFLAGS 1135
#define _CS_POSIX_V7_ILP32_OFFBIG_CFLAGS 1136
#define _CS_POSIX_V7_ILP32_OFFBIG_LDFLAGS 1137
#define _CS_POSIX_V7_ILP32_OFFBIG_LIBS 1138
#define _CS_POSIX_V7_ILP32_OFFBIG_LINTFLAGS 1139
#define _CS_POSIX_V7_LP64_OFF64_CFLAGS 1140
#define _CS_POSIX_V7_LP64_OFF64_LDFLAGS 1141
#define _CS_POSIX_V7_LP64_OFF64_LIBS 1142
#define _CS_POSIX_V7_LP64_OFF64_LINTFLAGS 1143
#define _CS_POSIX_V7_LPBIG_OFFBIG_CFLAGS 1144
#define _CS_POSIX_V7_LPBIG_OFFBIG_LDFLAGS 1145
#define _CS_POSIX_V7_LPBIG_OFFBIG_LIBS 1146
#define _CS_POSIX_V7_LPBIG_OFFBIG_LINTFLAGS 1147
#define _CS_V6_ENV 1148
#define _CS_V7_ENV 1149
#define F_ULOCK 0
#define F_LOCK 1
#define F_TLOCK 2
#define F_TEST 3
#define POSIX_CLOSE_RESTART 0
int unlinkat(int fd, const char *path, int flag);
int linkat(int fd1, const char *path1, int fd2, const char *path2, int flag);
int symlinkat(const char *path1, int fd, const char *path2);
ssize_t readlinkat(int fd, const char *restrict path, char *restrict buf, size_t bufsize);
int faccessat(int fd, const char *path, int amode, int flag);
int fchownat(int fd, const char *path, uid_t owner, gid_t group, int flag);
int fchdir(int fd);
int dup3(int fd, int fd2, int flags);
int pipe2(int fds[2], int flags);
int fexecve(int fd, char *const argv[], char *const envp[]);
long pathconf(const char *path, int name);
long fpathconf(int fd, int name);
size_t confstr(int name, char *buf, size_t len);
void sync(void);
int lockf(int fd, int cmd, off_t len);
int nice(int incr);
int getgroups(int gidsetsize, gid_t grouplist[]);
int setuid(uid_t uid);
int setgid(gid_t gid);
int seteuid(uid_t uid);
int setegid(gid_t gid);
int setreuid(uid_t ruid, uid_t euid);
int setregid(gid_t rgid, gid_t egid);
char *getlogin(void);
int getlogin_r(char *name, size_t namesize);
char *ttyname(int fd);
int ttyname_r(int fd, char *name, size_t namesize);
long gethostid(void);
int posix_close(int fd, int flag);
pid_t setpgrp(void);
pid_t _Fork(void);
`)
	extendHeader("stdio.h", `#include <sys/types.h>
#define L_ctermid 20
int fseeko(FILE *stream, off_t offset, int whence);
off_t ftello(FILE *stream);
void flockfile(FILE *file);
int ftrylockfile(FILE *file);
void funlockfile(FILE *file);
int getc_unlocked(FILE *stream);
int getchar_unlocked(void);
int putc_unlocked(int c, FILE *stream);
int putchar_unlocked(int c);
int renameat(int oldfd, const char *old, int newfd, const char *new);
char *ctermid(char *s);
`)
	extendHeader("dirent.h", `#define d_fileno d_ino
typedef unsigned short reclen_t;
struct posix_dent {
	ino_t d_ino;
	off_t d_off;
	reclen_t d_reclen;
	unsigned char d_type;
	char d_name[];
};
int dirfd(DIR *dirp);
DIR *fdopendir(int fd);
int readdir_r(DIR *restrict dirp, struct dirent *restrict entry, struct dirent **restrict result);
long telldir(DIR *dirp);
void seekdir(DIR *dirp, long loc);
int alphasort(const struct dirent **d1, const struct dirent **d2);
int scandir(const char *dir, struct dirent ***namelist, int (*sel)(const struct dirent *), int (*compar)(const struct dirent **, const struct dirent **));
ssize_t posix_getdents(int fd, void *buf, size_t nbyte, int flags);
`)
	extendHeader("sys/resource.h", `#include <sys/types.h>
#ifdef __ILP32__
typedef unsigned long long rlim_t;
#define RLIM_INFINITY (~0ULL)
#else
typedef unsigned long rlim_t;
#define RLIM_INFINITY (~0UL)
#endif
#define RLIM_SAVED_CUR RLIM_INFINITY
#define RLIM_SAVED_MAX RLIM_INFINITY
struct rlimit {
	rlim_t rlim_cur;
	rlim_t rlim_max;
};
#define PRIO_PROCESS 0
#define PRIO_PGRP 1
#define PRIO_USER 2
#define RLIMIT_CPU 0
#define RLIMIT_FSIZE 1
#define RLIMIT_DATA 2
#define RLIMIT_STACK 3
#define RLIMIT_CORE 4
#define RLIMIT_RSS 5
#define RLIMIT_NPROC 6
#define RLIMIT_NOFILE 7
#define RLIMIT_MEMLOCK 8
#define RLIMIT_AS 9
#define RLIMIT_LOCKS 10
#define RLIMIT_SIGPENDING 11
#define RLIMIT_MSGQUEUE 12
#define RLIMIT_NICE 13
#define RLIMIT_RTPRIO 14
#define RLIMIT_RTTIME 15
#define RLIMIT_NLIMITS 16
#define RLIM_NLIMITS RLIMIT_NLIMITS
int getpriority(int which, id_t who);
int setpriority(int which, id_t who, int prio);
int getrlimit(int resource, struct rlimit *rlp);
int setrlimit(int resource, const struct rlimit *rlp);
`)
	extendHeader("sys/mman.h", `#define MCL_CURRENT 1
#define MCL_FUTURE 2
#define MCL_ONFAULT 4
#define POSIX_MADV_NORMAL 0
#define POSIX_MADV_RANDOM 1
#define POSIX_MADV_SEQUENTIAL 2
#define POSIX_MADV_WILLNEED 3
#define POSIX_MADV_DONTNEED 4
int mlock(const void *addr, size_t len);
int munlock(const void *addr, size_t len);
int mlockall(int flags);
int munlockall(void);
int posix_madvise(void *addr, size_t len, int advice);
int shm_open(const char *name, int oflag, mode_t mode);
int shm_unlink(const char *name);
`)
	extendHeader("pwd.h", `struct passwd *getpwent(void);
void setpwent(void);
void endpwent(void);
int getpwuid_r(uid_t uid, struct passwd *pwd, char *buf, size_t buflen, struct passwd **result);
int getpwnam_r(const char *name, struct passwd *pwd, char *buf, size_t buflen, struct passwd **result);
`)
	extendHeader("grp.h", `struct group *getgrent(void);
void setgrent(void);
void endgrent(void);
int getgrgid_r(gid_t gid, struct group *grp, char *buf, size_t buflen, struct group **result);
int getgrnam_r(const char *name, struct group *grp, char *buf, size_t buflen, struct group **result);
`)
	extendHeader("netdb.h", `struct netent {
	char *n_name;
	char **n_aliases;
	int n_addrtype;
	uint32_t n_net;
};
struct hostent *gethostent(void);
void sethostent(int stayopen);
void endhostent(void);
struct netent *getnetent(void);
struct netent *getnetbyname(const char *name);
struct netent *getnetbyaddr(uint32_t net, int type);
void setnetent(int stayopen);
void endnetent(void);
struct protoent *getprotoent(void);
void setprotoent(int stayopen);
void endprotoent(void);
struct servent *getservent(void);
struct servent *getservbyport(int port, const char *proto);
void setservent(int stayopen);
void endservent(void);
`)
	extendHeader("net/if.h", `struct if_nameindex {
	unsigned int if_index;
	char *if_name;
};
struct if_nameindex *if_nameindex(void);
void if_freenameindex(struct if_nameindex *ptr);
`)
	// struct winsize is termios.h's (POSIX 2024's tcgetwinsize) and
	// sys/ioctl.h's.
	const winsize = `struct winsize {
	unsigned short ws_row;
	unsigned short ws_col;
	unsigned short ws_xpixel;
	unsigned short ws_ypixel;
};
`
	const guarded = "#ifndef __CC_STRUCT_WINSIZE\n#define __CC_STRUCT_WINSIZE\n" + winsize + "#endif\n"
	if !strings.Contains(sysHeaders["sys/ioctl.h"], winsize) {
		panic("cmd/cc: sys/ioctl.h has no struct winsize")
	}
	sysHeaders["sys/ioctl.h"] = strings.Replace(sysHeaders["sys/ioctl.h"], winsize, guarded, 1)
	extendHeader("termios.h", guarded+`int tcgetwinsize(int fd, struct winsize *ws);
int tcsetwinsize(int fd, const struct winsize *ws);
`)
	extendHeader("stdlib.h", `int posix_openpt(int oflag);
int grantpt(int fd);
int unlockpt(int fd);
char *ptsname(int fd);
int ptsname_r(int fd, char *buf, size_t buflen);
`)
	sysHeaders["utmpx.h"] = `#ifndef __CC_UTMPX_H
#define __CC_UTMPX_H
#include <sys/types.h>
#include <sys/time.h>
#include <stdint.h>
#define __UT_LINESIZE 32
#define __UT_NAMESIZE 32
#define __UT_HOSTSIZE 256
#define EMPTY 0
#define RUN_LVL 1
#define BOOT_TIME 2
#define NEW_TIME 3
#define OLD_TIME 4
#define INIT_PROCESS 5
#define LOGIN_PROCESS 6
#define USER_PROCESS 7
#define DEAD_PROCESS 8
#define ACCOUNTING 9
struct __exit_status {
	short e_termination;
	short e_exit;
};
struct utmpx {
	short ut_type;
	pid_t ut_pid;
	char ut_line[__UT_LINESIZE];
	char ut_id[4];
	char ut_user[__UT_NAMESIZE];
	char ut_host[__UT_HOSTSIZE];
	struct __exit_status ut_exit;
	int32_t ut_session;
	struct {
		int32_t tv_sec;
		int32_t tv_usec;
	} ut_tv;
	int32_t ut_addr_v6[4];
	char __glibc_reserved[20];
};
#define ut_name ut_user
#define ut_xtime ut_tv.tv_sec
#define ut_addr ut_addr_v6[0]
void setutxent(void);
struct utmpx *getutxent(void);
struct utmpx *getutxid(const struct utmpx *ut);
struct utmpx *getutxline(const struct utmpx *ut);
struct utmpx *pututxline(const struct utmpx *ut);
void endutxent(void);
#endif
`
	for k, v := range pxFilesLibc {
		libc[k] = v
	}
}

var pxFilesLibc = map[string]string{
	// fcntl.h
	"openat": "fcntl.Openat", "posix_fadvise": "fcntl.PosixFadvise", "posix_fallocate": "fcntl.PosixFallocate",
	// sys/stat.h
	"fstatat": "sys/stat.Fstatat", "mkdirat": "sys/stat.Mkdirat", "fchmodat": "sys/stat.Fchmodat",
	"futimens": "sys/stat.Futimens", "utimensat": "sys/stat.Utimensat", "mkfifo": "sys/stat.Mkfifo",
	"mkfifoat": "sys/stat.Mkfifoat", "mknod": "sys/stat.Mknod", "mknodat": "sys/stat.Mknodat",
	// unistd.h (lockf is on fcntl.h's record locks)
	"unlinkat": "unistd.Unlinkat", "linkat": "unistd.Linkat", "symlinkat": "unistd.Symlinkat",
	"readlinkat": "unistd.Readlinkat", "faccessat": "unistd.Faccessat", "fchownat": "unistd.Fchownat",
	"fchdir": "unistd.Fchdir", "dup3": "unistd.Dup3", "pipe2": "unistd.Pipe2", "fexecve": "unistd.Fexecve",
	"pathconf": "unistd.Pathconf", "fpathconf": "unistd.Fpathconf", "confstr": "unistd.Confstr",
	"sync": "unistd.Sync", "lockf": "fcntl.Lockf", "nice": "unistd.Nice", "getgroups": "unistd.Getgroups",
	"setuid": "unistd.Setuid", "setgid": "unistd.Setgid", "seteuid": "unistd.Seteuid",
	"setegid": "unistd.Setegid", "setreuid": "unistd.Setreuid", "setregid": "unistd.Setregid",
	"getlogin": "unistd.Getlogin", "getlogin_r": "unistd.GetloginR", "ttyname": "unistd.Ttyname",
	"ttyname_r": "unistd.TtynameR", "gethostid": "unistd.Gethostid", "posix_close": "unistd.PosixClose",
	"setpgrp": "unistd.Setpgrp", "_Fork": "unistd.Fork",
	// stdio.h: the streams' functions take no lock themselves, so the
	// _unlocked ones are theirs
	"fseeko": "stdio.Fseek", "ftello": "stdio.Ftell", "flockfile": "stdio.Flockfile",
	"ftrylockfile": "stdio.Ftrylockfile", "funlockfile": "stdio.Funlockfile",
	"getc_unlocked": "stdio.Fgetc", "getchar_unlocked": "stdio.Getchar", "putc_unlocked": "stdio.Fputc",
	"putchar_unlocked": "stdio.Putchar", "renameat": "stdio.Renameat", "ctermid": "stdio.Ctermid",
	// dirent.h
	"dirfd": "dirent.Dirfd", "fdopendir": "dirent.Fdopendir", "readdir_r": "dirent.ReaddirR",
	"telldir": "dirent.Telldir", "seekdir": "dirent.Seekdir", "alphasort": "dirent.Alphasort",
	"scandir": "dirent.Scandir", "posix_getdents": "dirent.PosixGetdents",
	// sys/resource.h
	"getpriority": "sys/resource.Getpriority", "setpriority": "sys/resource.Setpriority",
	"getrlimit": "sys/resource.Getrlimit", "setrlimit": "sys/resource.Setrlimit",
	// sys/mman.h
	"mlock": "sys/mman.Mlock", "munlock": "sys/mman.Munlock", "mlockall": "sys/mman.Mlockall",
	"munlockall": "sys/mman.Munlockall", "posix_madvise": "sys/mman.PosixMadvise",
	"shm_open": "sys/mman.ShmOpen", "shm_unlink": "sys/mman.ShmUnlink",
	// pwd.h, grp.h
	"getpwent": "pwd.Getpwent", "setpwent": "pwd.Setpwent", "endpwent": "pwd.Endpwent",
	"getpwuid_r": "pwd.GetpwuidR", "getpwnam_r": "pwd.GetpwnamR",
	"getgrent": "grp.Getgrent", "setgrent": "grp.Setgrent", "endgrent": "grp.Endgrent",
	"getgrgid_r": "grp.GetgrgidR", "getgrnam_r": "grp.GetgrnamR",
	// utmpx.h
	"setutxent": "utmpx.Setutxent", "getutxent": "utmpx.Getutxent", "getutxid": "utmpx.Getutxid",
	"getutxline": "utmpx.Getutxline", "pututxline": "utmpx.Pututxline", "endutxent": "utmpx.Endutxent",
	// netdb.h
	"gethostent": "netdb.Gethostent", "sethostent": "netdb.Sethostent", "endhostent": "netdb.Endhostent",
	"getnetent": "netdb.Getnetent", "getnetbyname": "netdb.Getnetbyname", "getnetbyaddr": "netdb.Getnetbyaddr",
	"setnetent": "netdb.Setnetent", "endnetent": "netdb.Endnetent",
	"getprotoent": "netdb.Getprotoent", "setprotoent": "netdb.Setprotoent", "endprotoent": "netdb.Endprotoent",
	"getservent": "netdb.Getservent", "getservbyport": "netdb.Getservbyport",
	"setservent": "netdb.Setservent", "endservent": "netdb.Endservent",
	// net/if.h
	"if_nameindex": "net/if.IfNameindex", "if_freenameindex": "net/if.IfFreenameindex",
	// termios.h
	"tcgetwinsize": "termios.Tcgetwinsize", "tcsetwinsize": "termios.Tcsetwinsize",
	// stdlib.h
	"posix_openpt": "stdlib.PosixOpenpt", "grantpt": "stdlib.Grantpt", "unlockpt": "stdlib.Unlockpt",
	"ptsname": "stdlib.Ptsname", "ptsname_r": "stdlib.PtsnameR",
}
