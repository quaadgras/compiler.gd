// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The headers of the common extensions of glibc and musl that are not
// C's or the POSIX ones headers.go has: getopt_long, err.h, libgen.h,
// endian.h, fnmatch.h, glob.h, search.h, iconv.h and others.

func init() {
	sysHeaders["getopt.h"] = `#ifndef __CC_GETOPT_H
#define __CC_GETOPT_H
#include <unistd.h>
struct option {
	const char *name;
	int has_arg;
	int *flag;
	int val;
};
#define no_argument 0
#define required_argument 1
#define optional_argument 2
int getopt_long(int argc, char *const argv[], const char *optstring, const struct option *longopts, int *longindex);
int getopt_long_only(int argc, char *const argv[], const char *optstring, const struct option *longopts, int *longindex);
#endif
`
	sysHeaders["err.h"] = `#ifndef __CC_ERR_H
#define __CC_ERR_H
#include <stdarg.h>
void warn(const char *fmt, ...);
void vwarn(const char *fmt, va_list ap);
void warnx(const char *fmt, ...);
void vwarnx(const char *fmt, va_list ap);
[[noreturn]] void err(int eval, const char *fmt, ...);
[[noreturn]] void verr(int eval, const char *fmt, va_list ap);
[[noreturn]] void errx(int eval, const char *fmt, ...);
[[noreturn]] void verrx(int eval, const char *fmt, va_list ap);
#endif
`
	sysHeaders["libgen.h"] = `#ifndef __CC_LIBGEN_H
#define __CC_LIBGEN_H
char *basename(char *path);
char *dirname(char *path);
#endif
`
	sysHeaders["endian.h"] = `#ifndef __CC_ENDIAN_H
#define __CC_ENDIAN_H
#include <stdint.h>
#define __LITTLE_ENDIAN 1234
#define __BIG_ENDIAN 4321
#define __PDP_ENDIAN 3412
#define __BYTE_ORDER __LITTLE_ENDIAN
#define LITTLE_ENDIAN __LITTLE_ENDIAN
#define BIG_ENDIAN __BIG_ENDIAN
#define PDP_ENDIAN __PDP_ENDIAN
#define BYTE_ORDER __BYTE_ORDER
#define htobe16(x) __builtin_bswap16(x)
#define htole16(x) ((uint16_t)(x))
#define be16toh(x) __builtin_bswap16(x)
#define le16toh(x) ((uint16_t)(x))
#define htobe32(x) __builtin_bswap32(x)
#define htole32(x) ((uint32_t)(x))
#define be32toh(x) __builtin_bswap32(x)
#define le32toh(x) ((uint32_t)(x))
#define htobe64(x) __builtin_bswap64(x)
#define htole64(x) ((uint64_t)(x))
#define be64toh(x) __builtin_bswap64(x)
#define le64toh(x) ((uint64_t)(x))
#endif
`
	sysHeaders["byteswap.h"] = `#ifndef __CC_BYTESWAP_H
#define __CC_BYTESWAP_H
#include <stdint.h>
#define bswap_16(x) __builtin_bswap16(x)
#define bswap_32(x) __builtin_bswap32(x)
#define bswap_64(x) __builtin_bswap64(x)
#endif
`
	sysHeaders["sys/param.h"] = `#ifndef __CC_SYS_PARAM_H
#define __CC_SYS_PARAM_H
#include <limits.h>
#include <endian.h>
#include <sys/types.h>
#ifndef PATH_MAX
#define PATH_MAX 4096
#endif
#define MAXPATHLEN PATH_MAX
#define MAXHOSTNAMELEN 64
#define MAXSYMLINKS 20
#define NOFILE 256
#define NGROUPS 32
#define NBBY 8
#define DEV_BSIZE 512
#define setbit(a, i) (((unsigned char *)(a))[(i) / NBBY] |= 1 << ((i) % NBBY))
#define clrbit(a, i) (((unsigned char *)(a))[(i) / NBBY] &= ~(1 << ((i) % NBBY)))
#define isset(a, i) (((const unsigned char *)(a))[(i) / NBBY] & (1 << ((i) % NBBY)))
#define isclr(a, i) ((((const unsigned char *)(a))[(i) / NBBY] & (1 << ((i) % NBBY))) == 0)
#define howmany(n, d) (((n) + ((d) - 1)) / (d))
#define roundup(n, d) (howmany(n, d) * (d))
#define powerof2(n) !(((n) - 1) & (n))
#define MIN(a, b) (((a) < (b)) ? (a) : (b))
#define MAX(a, b) (((a) > (b)) ? (a) : (b))
#endif
`
	sysHeaders["fnmatch.h"] = `#ifndef __CC_FNMATCH_H
#define __CC_FNMATCH_H
#define FNM_PATHNAME 0x1
#define FNM_NOESCAPE 0x2
#define FNM_PERIOD 0x4
#define FNM_LEADING_DIR 0x8
#define FNM_CASEFOLD 0x10
#define FNM_FILE_NAME FNM_PATHNAME
#define FNM_NOMATCH 1
#define FNM_NOSYS (-1)
int fnmatch(const char *pattern, const char *string, int flags);
#endif
`
	sysHeaders["glob.h"] = `#ifndef __CC_GLOB_H
#define __CC_GLOB_H
#include <stddef.h>
typedef struct {
	size_t gl_pathc;
	char **gl_pathv;
	size_t gl_offs;
	int gl_flags;
	void *__unused[5];
} glob_t;
#define GLOB_ERR 0x01
#define GLOB_MARK 0x02
#define GLOB_NOSORT 0x04
#define GLOB_DOOFFS 0x08
#define GLOB_NOCHECK 0x10
#define GLOB_APPEND 0x20
#define GLOB_NOESCAPE 0x40
#define GLOB_PERIOD 0x80
#define GLOB_NOSPACE 1
#define GLOB_ABORTED 2
#define GLOB_NOMATCH 3
#define GLOB_NOSYS 4
int glob(const char *restrict pattern, int flags, int (*errfunc)(const char *epath, int eerrno), glob_t *restrict pglob);
void globfree(glob_t *pglob);
#endif
`
	sysHeaders["search.h"] = `#ifndef __CC_SEARCH_H
#define __CC_SEARCH_H
#include <stddef.h>
typedef struct entry {
	char *key;
	void *data;
} ENTRY;
typedef enum { FIND, ENTER } ACTION;
typedef enum { preorder, postorder, endorder, leaf } VISIT;
int hcreate(size_t nel);
void hdestroy(void);
ENTRY *hsearch(ENTRY item, ACTION action);
void *tsearch(const void *key, void **rootp, int (*compar)(const void *, const void *));
void *tfind(const void *key, void *const *rootp, int (*compar)(const void *, const void *));
void *tdelete(const void *restrict key, void **restrict rootp, int (*compar)(const void *, const void *));
void twalk(const void *root, void (*action)(const void *nodep, VISIT which, int depth));
void tdestroy(void *root, void (*free_node)(void *nodep));
void *lfind(const void *key, const void *base, size_t *nelp, size_t width, int (*compar)(const void *, const void *));
void *lsearch(const void *key, void *base, size_t *nelp, size_t width, int (*compar)(const void *, const void *));
struct qelem {
	struct qelem *q_forw;
	struct qelem *q_back;
	char q_data[1];
};
void insque(void *element, void *pred);
void remque(void *element);
#endif
`
	sysHeaders["malloc.h"] = `#ifndef __CC_MALLOC_H
#define __CC_MALLOC_H
#include <stdlib.h>
size_t malloc_usable_size(void *ptr);
void *memalign(size_t alignment, size_t size);
void *valloc(size_t size);
void *pvalloc(size_t size);
#endif
`
	sysHeaders["sys/random.h"] = `#ifndef __CC_SYS_RANDOM_H
#define __CC_SYS_RANDOM_H
#include <sys/types.h>
#define GRND_NONBLOCK 0x01
#define GRND_RANDOM 0x02
#define GRND_INSECURE 0x04
ssize_t getrandom(void *buf, size_t buflen, unsigned int flags);
int getentropy(void *buffer, size_t length);
#endif
`
	sysHeaders["sys/uio.h"] = `#ifndef __CC_SYS_UIO_H
#define __CC_SYS_UIO_H
#include <sys/types.h>
#ifndef __CC_IOVEC
#define __CC_IOVEC
struct iovec { void *iov_base; size_t iov_len; };
#endif
#define UIO_MAXIOV 1024
#define IOV_MAX 1024
ssize_t readv(int fd, const struct iovec *iov, int iovcnt);
ssize_t writev(int fd, const struct iovec *iov, int iovcnt);
#endif
`
	sysHeaders["sys/utsname.h"] = `#ifndef __CC_SYS_UTSNAME_H
#define __CC_SYS_UTSNAME_H
struct utsname {
	char sysname[65];
	char nodename[65];
	char release[65];
	char version[65];
	char machine[65];
	char domainname[65];
};
int uname(struct utsname *name);
#endif
`
	sysHeaders["sys/file.h"] = `#ifndef __CC_SYS_FILE_H
#define __CC_SYS_FILE_H
#include <fcntl.h>
#define LOCK_SH 1
#define LOCK_EX 2
#define LOCK_NB 4
#define LOCK_UN 8
int flock(int fd, int operation);
#endif
`
	sysHeaders["grp.h"] = `#ifndef __CC_GRP_H
#define __CC_GRP_H
#include <sys/types.h>
struct group {
	char *gr_name;
	char *gr_passwd;
	gid_t gr_gid;
	char **gr_mem;
};
struct group *getgrnam(const char *name);
struct group *getgrgid(gid_t gid);
#endif
`
	sysHeaders["syslog.h"] = `#ifndef __CC_SYSLOG_H
#define __CC_SYSLOG_H
#include <stdarg.h>
#define LOG_EMERG 0
#define LOG_ALERT 1
#define LOG_CRIT 2
#define LOG_ERR 3
#define LOG_WARNING 4
#define LOG_NOTICE 5
#define LOG_INFO 6
#define LOG_DEBUG 7
#define LOG_PRIMASK 7
#define LOG_PRI(p) ((p) & LOG_PRIMASK)
#define LOG_MAKEPRI(f, p) ((f) | (p))
#define LOG_KERN (0 << 3)
#define LOG_USER (1 << 3)
#define LOG_MAIL (2 << 3)
#define LOG_DAEMON (3 << 3)
#define LOG_AUTH (4 << 3)
#define LOG_SYSLOG (5 << 3)
#define LOG_LPR (6 << 3)
#define LOG_NEWS (7 << 3)
#define LOG_UUCP (8 << 3)
#define LOG_CRON (9 << 3)
#define LOG_AUTHPRIV (10 << 3)
#define LOG_FTP (11 << 3)
#define LOG_LOCAL0 (16 << 3)
#define LOG_LOCAL1 (17 << 3)
#define LOG_LOCAL2 (18 << 3)
#define LOG_LOCAL3 (19 << 3)
#define LOG_LOCAL4 (20 << 3)
#define LOG_LOCAL5 (21 << 3)
#define LOG_LOCAL6 (22 << 3)
#define LOG_LOCAL7 (23 << 3)
#define LOG_FACMASK 0x3f8
#define LOG_FAC(p) (((p) & LOG_FACMASK) >> 3)
#define LOG_MASK(p) (1 << (p))
#define LOG_UPTO(p) ((1 << ((p) + 1)) - 1)
#define LOG_PID 0x01
#define LOG_CONS 0x02
#define LOG_ODELAY 0x04
#define LOG_NDELAY 0x08
#define LOG_NOWAIT 0x10
#define LOG_PERROR 0x20
void openlog(const char *ident, int option, int facility);
void syslog(int priority, const char *format, ...);
void vsyslog(int priority, const char *format, va_list ap);
void closelog(void);
int setlogmask(int mask);
#endif
`
	sysHeaders["langinfo.h"] = `#ifndef __CC_LANGINFO_H
#define __CC_LANGINFO_H
#include <locale.h>
typedef int nl_item;
#define CODESET 14
#define RADIXCHAR 0x10000
#define THOUSEP 0x10001
#define ABDAY_1 0x20000
#define ABDAY_2 0x20001
#define ABDAY_3 0x20002
#define ABDAY_4 0x20003
#define ABDAY_5 0x20004
#define ABDAY_6 0x20005
#define ABDAY_7 0x20006
#define DAY_1 0x20007
#define DAY_2 0x20008
#define DAY_3 0x20009
#define DAY_4 0x2000A
#define DAY_5 0x2000B
#define DAY_6 0x2000C
#define DAY_7 0x2000D
#define ABMON_1 0x2000E
#define ABMON_2 0x2000F
#define ABMON_3 0x20010
#define ABMON_4 0x20011
#define ABMON_5 0x20012
#define ABMON_6 0x20013
#define ABMON_7 0x20014
#define ABMON_8 0x20015
#define ABMON_9 0x20016
#define ABMON_10 0x20017
#define ABMON_11 0x20018
#define ABMON_12 0x20019
#define MON_1 0x2001A
#define MON_2 0x2001B
#define MON_3 0x2001C
#define MON_4 0x2001D
#define MON_5 0x2001E
#define MON_6 0x2001F
#define MON_7 0x20020
#define MON_8 0x20021
#define MON_9 0x20022
#define MON_10 0x20023
#define MON_11 0x20024
#define MON_12 0x20025
#define AM_STR 0x20026
#define PM_STR 0x20027
#define D_T_FMT 0x20028
#define D_FMT 0x20029
#define T_FMT 0x2002A
#define T_FMT_AMPM 0x2002B
#define ERA 0x2002C
#define ERA_D_FMT 0x2002E
#define ALT_DIGITS 0x2002F
#define ERA_D_T_FMT 0x20030
#define ERA_T_FMT 0x20031
#define CRNCYSTR 0x4000F
#define YESEXPR 0x50000
#define NOEXPR 0x50001
char *nl_langinfo(nl_item item);
#endif
`
	sysHeaders["iconv.h"] = `#ifndef __CC_ICONV_H
#define __CC_ICONV_H
#include <stddef.h>
typedef void *iconv_t;
iconv_t iconv_open(const char *tocode, const char *fromcode);
size_t iconv(iconv_t cd, char **restrict inbuf, size_t *restrict inbytesleft, char **restrict outbuf, size_t *restrict outbytesleft);
int iconv_close(iconv_t cd);
#endif
`
	for k, v := range map[string]string{
		// getopt.h
		"getopt_long": "GetoptLong", "getopt_long_only": "GetoptLongOnly",
		// err.h
		"warn": "Warn", "vwarn": "Vwarn", "warnx": "Warnx", "vwarnx": "Vwarnx",
		"err": "Err", "verr": "Verr", "errx": "Errx", "verrx": "Verrx",
		// libgen.h
		"basename": "Basename", "dirname": "Dirname",
		// fnmatch.h, glob.h
		"fnmatch": "Fnmatch", "glob": "Glob", "globfree": "Globfree",
		// search.h
		"hcreate": "Hcreate", "hdestroy": "Hdestroy", "hsearch": "Hsearch",
		"tsearch": "Tsearch", "tfind": "Tfind", "tdelete": "Tdelete", "twalk": "Twalk",
		"tdestroy": "Tdestroy", "lfind": "Lfind", "lsearch": "Lsearch",
		"insque": "Insque", "remque": "Remque",
		// malloc.h
		"malloc_usable_size": "MallocUsableSize", "memalign": "Memalign",
		"valloc": "Valloc", "pvalloc": "Pvalloc",
		// sys/random.h
		"getrandom": "Getrandom", "getentropy": "Getentropy",
		// sys/uio.h, sys/utsname.h, sys/file.h
		"readv": "Readv", "writev": "Writev", "uname": "Uname", "flock": "Flock",
		// grp.h
		"getgrnam": "Getgrnam", "getgrgid": "Getgrgid",
		// syslog.h
		"openlog": "Openlog", "syslog": "Syslog", "vsyslog": "Vsyslog",
		"closelog": "Closelog", "setlogmask": "Setlogmask",
		// langinfo.h, iconv.h
		"nl_langinfo": "NlLanginfo",
		"iconv_open":  "IconvOpen", "iconv": "Iconv", "iconv_close": "IconvClose",
		// stdio.h
		"getline": "Getline", "getdelim": "Getdelim",
		"asprintf": "Asprintf", "vasprintf": "Vasprintf",
		"dprintf": "Dprintf", "vdprintf": "Vdprintf",
		"fmemopen": "Fmemopen", "open_memstream": "OpenMemstream",
		// string.h
		"strsep": "Strsep", "strsignal": "Strsignal",
		// stdlib.h
		"reallocarray": "Reallocarray", "qsort_r": "QsortR", "mkdtemp": "Mkdtemp",
	} {
		libc[k] = v
	}
}
