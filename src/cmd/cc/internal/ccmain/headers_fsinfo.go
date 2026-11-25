// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The headers of file systems' and the system's information (sys/statvfs.h,
// sys/statfs.h, ftw.h, sys/times.h, sys/sysinfo.h, ifaddrs.h), wordexp.h,
// and the small ones of glibc and musl: paths.h, sysexits.h, stdio_ext.h,
// libintl.h, monetary.h, nl_types.h, fmtmsg.h, ulimit.h, values.h, tar.h,
// cpio.h, ar.h, netinet/ip.h, netinet/udp.h, arpa/nameser.h and resolv.h.
// The structures are Linux's (glibc's and musl's agree on them).

func init() {
	for name, text := range fsinfoHeaders {
		sysHeaders[name] = text
	}
	for c, goName := range fsinfoLibc {
		libc[c] = goName
	}
}

var fsinfoLibc = map[string]string{
	// sys/statvfs.h, sys/statfs.h
	"statvfs": "sys/statvfs.Statvfs", "fstatvfs": "sys/statvfs.Fstatvfs",
	"statfs": "sys/statvfs.Statfs", "fstatfs": "sys/statvfs.Fstatfs",
	// ftw.h
	"ftw": "ftw.Ftw", "nftw": "ftw.Nftw",
	// wordexp.h
	"wordexp": "wordexp.Wordexp", "wordfree": "wordexp.Wordfree",
	// sys/times.h
	"times": "sys/times.Times",
	// sys/sysinfo.h
	"sysinfo": "sys/sysinfo.Sysinfo", "get_nprocs": "sys/sysinfo.GetNprocs",
	"get_nprocs_conf": "sys/sysinfo.GetNprocsConf", "get_phys_pages": "sys/sysinfo.GetPhysPages",
	"get_avphys_pages": "sys/sysinfo.GetAvphysPages",
	// ifaddrs.h
	"getifaddrs": "ifaddrs.Getifaddrs", "freeifaddrs": "ifaddrs.Freeifaddrs",
	// stdio_ext.h
	"__fbufsize": "stdio_ext.Fbufsize", "__fpending": "stdio_ext.Fpending", "__flbf": "stdio_ext.Flbf",
	"__freadable": "stdio_ext.Freadable", "__fwritable": "stdio_ext.Fwritable",
	"__freading": "stdio_ext.Freading", "__fwriting": "stdio_ext.Fwriting",
	"__fsetlocking": "stdio_ext.Fsetlocking", "_flushlbf": "stdio_ext.Flushlbf", "__fpurge": "stdio_ext.Fpurge",
	// libintl.h
	"gettext": "libintl.Gettext", "dgettext": "libintl.Dgettext", "dcgettext": "libintl.Dcgettext",
	"ngettext": "libintl.Ngettext", "dngettext": "libintl.Dngettext", "dcngettext": "libintl.Dcngettext",
	"textdomain": "libintl.Textdomain", "bindtextdomain": "libintl.Bindtextdomain",
	"bind_textdomain_codeset": "libintl.BindTextdomainCodeset",
	// monetary.h
	"strfmon": "monetary.Strfmon",
	// nl_types.h
	"catopen": "nl_types.Catopen", "catgets": "nl_types.Catgets", "catclose": "nl_types.Catclose",
	// fmtmsg.h, ulimit.h
	"fmtmsg": "fmtmsg.Fmtmsg", "ulimit": "ulimit.Ulimit",
	// resolv.h
	"res_init": "resolv.ResInit", "res_query": "resolv.ResQuery", "res_search": "resolv.ResSearch",
	"res_querydomain": "resolv.ResQuerydomain",
}

var fsinfoHeaders = map[string]string{
	"sys/statvfs.h": `#ifndef __CC_SYS_STATVFS_H
#define __CC_SYS_STATVFS_H
#include <sys/types.h>
#ifndef __CC_FSBLKCNT_T
#define __CC_FSBLKCNT_T
#ifdef __ILP32__
typedef unsigned long long fsblkcnt_t;
typedef unsigned long long fsfilcnt_t;
#else
typedef unsigned long fsblkcnt_t;
typedef unsigned long fsfilcnt_t;
#endif
#endif
struct statvfs {
	unsigned long f_bsize;
	unsigned long f_frsize;
	fsblkcnt_t f_blocks;
	fsblkcnt_t f_bfree;
	fsblkcnt_t f_bavail;
	fsfilcnt_t f_files;
	fsfilcnt_t f_ffree;
	fsfilcnt_t f_favail;
	unsigned long f_fsid;
#ifdef __ILP32__
	int __f_unused;
#endif
	unsigned long f_flag;
	unsigned long f_namemax;
	int __f_spare[6];
};
#define ST_RDONLY 1
#define ST_NOSUID 2
#define ST_NODEV 4
#define ST_NOEXEC 8
#define ST_SYNCHRONOUS 16
#define ST_MANDLOCK 64
#define ST_WRITE 128
#define ST_APPEND 256
#define ST_IMMUTABLE 512
#define ST_NOATIME 1024
#define ST_NODIRATIME 2048
#define ST_RELATIME 4096
int statvfs(const char *restrict path, struct statvfs *restrict buf);
int fstatvfs(int fd, struct statvfs *buf);
#endif
`,
	"sys/statfs.h": `#ifndef __CC_SYS_STATFS_H
#define __CC_SYS_STATFS_H
#include <sys/types.h>
#ifndef __CC_FSBLKCNT_T
#define __CC_FSBLKCNT_T
#ifdef __ILP32__
typedef unsigned long long fsblkcnt_t;
typedef unsigned long long fsfilcnt_t;
#else
typedef unsigned long fsblkcnt_t;
typedef unsigned long fsfilcnt_t;
#endif
#endif
typedef struct { int __val[2]; } fsid_t;
struct statfs {
	unsigned long f_type;
	unsigned long f_bsize;
	fsblkcnt_t f_blocks;
	fsblkcnt_t f_bfree;
	fsblkcnt_t f_bavail;
	fsfilcnt_t f_files;
	fsfilcnt_t f_ffree;
	fsid_t f_fsid;
	unsigned long f_namelen;
	unsigned long f_frsize;
	unsigned long f_flags;
	unsigned long f_spare[4];
};
int statfs(const char *path, struct statfs *buf);
int fstatfs(int fd, struct statfs *buf);
#endif
`,
	"sys/vfs.h": `#include <sys/statfs.h>
`,
	"ftw.h": `#ifndef __CC_FTW_H
#define __CC_FTW_H
#include <sys/stat.h>
#define FTW_F 0
#define FTW_D 1
#define FTW_DNR 2
#define FTW_NS 3
#define FTW_SL 4
#define FTW_DP 5
#define FTW_SLN 6
#define FTW_PHYS 1
#define FTW_MOUNT 2
#define FTW_CHDIR 4
#define FTW_DEPTH 8
#define FTW_ACTIONRETVAL 16
#define FTW_CONTINUE 0
#define FTW_STOP 1
#define FTW_SKIP_SUBTREE 2
#define FTW_SKIP_SIBLINGS 3
struct FTW {
	int base;
	int level;
};
int ftw(const char *path, int (*fn)(const char *, const struct stat *, int), int fd_limit);
int nftw(const char *path, int (*fn)(const char *, const struct stat *, int, struct FTW *), int fd_limit, int flags);
#endif
`,
	"wordexp.h": `#ifndef __CC_WORDEXP_H
#define __CC_WORDEXP_H
#include <stddef.h>
typedef struct {
	size_t we_wordc;
	char **we_wordv;
	size_t we_offs;
} wordexp_t;
#define WRDE_DOOFFS 1
#define WRDE_APPEND 2
#define WRDE_NOCMD 4
#define WRDE_REUSE 8
#define WRDE_SHOWERR 16
#define WRDE_UNDEF 32
#define WRDE_NOSYS (-1)
#define WRDE_NOSPACE 1
#define WRDE_BADCHAR 2
#define WRDE_BADVAL 3
#define WRDE_CMDSUB 4
#define WRDE_SYNTAX 5
int wordexp(const char *restrict words, wordexp_t *restrict we, int flags);
void wordfree(wordexp_t *we);
#endif
`,
	"sys/times.h": `#ifndef __CC_SYS_TIMES_H
#define __CC_SYS_TIMES_H
#include <sys/types.h>
struct tms {
	clock_t tms_utime;
	clock_t tms_stime;
	clock_t tms_cutime;
	clock_t tms_cstime;
};
clock_t times(struct tms *buf);
#endif
`,
	"sys/sysinfo.h": `#ifndef __CC_SYS_SYSINFO_H
#define __CC_SYS_SYSINFO_H
#define SI_LOAD_SHIFT 16
struct sysinfo {
	long uptime;
	unsigned long loads[3];
	unsigned long totalram;
	unsigned long freeram;
	unsigned long sharedram;
	unsigned long bufferram;
	unsigned long totalswap;
	unsigned long freeswap;
	unsigned short procs;
	unsigned short pad;
	unsigned long totalhigh;
	unsigned long freehigh;
	unsigned int mem_unit;
	char _f[20 - 2 * sizeof(long) - sizeof(int)];
};
int sysinfo(struct sysinfo *info);
int get_nprocs(void);
int get_nprocs_conf(void);
long get_phys_pages(void);
long get_avphys_pages(void);
#endif
`,
	"sys/sysmacros.h": `#ifndef __CC_SYS_SYSMACROS_H
#define __CC_SYS_SYSMACROS_H
#define major(x) ((unsigned int)((((unsigned long long)(x) >> 32) & 0xfffff000) | (((unsigned long long)(x) >> 8) & 0x00000fff)))
#define minor(x) ((unsigned int)((((unsigned long long)(x) >> 12) & 0xffffff00) | ((unsigned long long)(x) & 0x000000ff)))
#define makedev(x, y) ((((unsigned long long)(x) & 0xfffff000ULL) << 32) | (((unsigned long long)(x) & 0x00000fffULL) << 8) | (((unsigned long long)(y) & 0xffffff00ULL) << 12) | ((unsigned long long)(y) & 0x000000ffULL))
#endif
`,
	"ifaddrs.h": `#ifndef __CC_IFADDRS_H
#define __CC_IFADDRS_H
#include <sys/socket.h>
struct ifaddrs {
	struct ifaddrs *ifa_next;
	char *ifa_name;
	unsigned int ifa_flags;
	struct sockaddr *ifa_addr;
	struct sockaddr *ifa_netmask;
	union {
		struct sockaddr *ifu_broadaddr;
		struct sockaddr *ifu_dstaddr;
	} ifa_ifu;
	void *ifa_data;
};
#define ifa_broadaddr ifa_ifu.ifu_broadaddr
#define ifa_dstaddr ifa_ifu.ifu_dstaddr
int getifaddrs(struct ifaddrs **ifap);
void freeifaddrs(struct ifaddrs *ifa);
#endif
`,
	"paths.h": `#ifndef __CC_PATHS_H
#define __CC_PATHS_H
#define _PATH_DEFPATH "/usr/bin:/bin"
#define _PATH_STDPATH "/usr/bin:/bin:/usr/sbin:/sbin"
#define _PATH_BSHELL "/bin/sh"
#define _PATH_CONSOLE "/dev/console"
#define _PATH_CSHELL "/bin/csh"
#define _PATH_DEVDB "/var/run/dev.db"
#define _PATH_DEVNULL "/dev/null"
#define _PATH_DRUM "/dev/drum"
#define _PATH_GSHADOW "/etc/gshadow"
#define _PATH_KLOG "/proc/kmsg"
#define _PATH_KMEM "/dev/kmem"
#define _PATH_LASTLOG "/var/log/lastlog"
#define _PATH_MAILDIR "/var/mail"
#define _PATH_MAN "/usr/share/man"
#define _PATH_MEM "/dev/mem"
#define _PATH_MNTTAB "/etc/fstab"
#define _PATH_MOUNTED "/etc/mtab"
#define _PATH_NOLOGIN "/etc/nologin"
#define _PATH_PRESERVE "/var/lib"
#define _PATH_RWHODIR "/var/spool/rwho"
#define _PATH_SENDMAIL "/usr/sbin/sendmail"
#define _PATH_SHADOW "/etc/shadow"
#define _PATH_SHELLS "/etc/shells"
#define _PATH_TTY "/dev/tty"
#define _PATH_UNIX "/boot/vmlinux"
#define _PATH_UTMP "/var/run/utmp"
#define _PATH_VI "/usr/bin/vi"
#define _PATH_WTMP "/var/log/wtmp"
#define _PATH_DEV "/dev/"
#define _PATH_TMP "/tmp/"
#define _PATH_VARDB "/var/db/"
#define _PATH_VARRUN "/var/run/"
#define _PATH_VARTMP "/var/tmp/"
#endif
`,
	"sysexits.h": `#ifndef __CC_SYSEXITS_H
#define __CC_SYSEXITS_H
#define EX_OK 0
#define EX__BASE 64
#define EX_USAGE 64
#define EX_DATAERR 65
#define EX_NOINPUT 66
#define EX_NOUSER 67
#define EX_NOHOST 68
#define EX_UNAVAILABLE 69
#define EX_SOFTWARE 70
#define EX_OSERR 71
#define EX_OSFILE 72
#define EX_CANTCREAT 73
#define EX_IOERR 74
#define EX_TEMPFAIL 75
#define EX_PROTOCOL 76
#define EX_NOPERM 77
#define EX_CONFIG 78
#define EX__MAX 78
#endif
`,
	// features.h is glibc's and musl's header of feature-test macros:
	// here only its guard, as cmd/cc's headers declare everything
	// whatever macros the program defines.
	"features.h": `#ifndef _FEATURES_H
#define _FEATURES_H 1
#endif
`,
	"stdio_ext.h": `#ifndef __CC_STDIO_EXT_H
#define __CC_STDIO_EXT_H
#include <stdio.h>
#define FSETLOCKING_QUERY 0
#define FSETLOCKING_INTERNAL 1
#define FSETLOCKING_BYCALLER 2
size_t __fbufsize(FILE *stream);
size_t __fpending(FILE *stream);
int __flbf(FILE *stream);
int __freadable(FILE *stream);
int __fwritable(FILE *stream);
int __freading(FILE *stream);
int __fwriting(FILE *stream);
int __fsetlocking(FILE *stream, int type);
void _flushlbf(void);
void __fpurge(FILE *stream);
#endif
`,
	"libintl.h": `#ifndef __CC_LIBINTL_H
#define __CC_LIBINTL_H
#include <locale.h>
char *gettext(const char *msgid);
char *dgettext(const char *domainname, const char *msgid);
char *dcgettext(const char *domainname, const char *msgid, int category);
char *ngettext(const char *msgid1, const char *msgid2, unsigned long n);
char *dngettext(const char *domainname, const char *msgid1, const char *msgid2, unsigned long n);
char *dcngettext(const char *domainname, const char *msgid1, const char *msgid2, unsigned long n, int category);
char *textdomain(const char *domainname);
char *bindtextdomain(const char *domainname, const char *dirname);
char *bind_textdomain_codeset(const char *domainname, const char *codeset);
#endif
`,
	"monetary.h": `#ifndef __CC_MONETARY_H
#define __CC_MONETARY_H
#include <sys/types.h>
#include <locale.h>
ssize_t strfmon(char *restrict s, size_t maxsize, const char *restrict format, ...);
#endif
`,
	"nl_types.h": `#ifndef __CC_NL_TYPES_H
#define __CC_NL_TYPES_H
#define NL_SETD 1
#define NL_CAT_LOCALE 1
typedef int nl_item;
typedef void *nl_catd;
nl_catd catopen(const char *name, int oflag);
char *catgets(nl_catd catd, int set_id, int msg_id, const char *s);
int catclose(nl_catd catd);
#endif
`,
	"fmtmsg.h": `#ifndef __CC_FMTMSG_H
#define __CC_FMTMSG_H
#define MM_HARD 0x001
#define MM_SOFT 0x002
#define MM_FIRM 0x004
#define MM_APPL 0x008
#define MM_UTIL 0x010
#define MM_OPSYS 0x020
#define MM_RECOVER 0x040
#define MM_NRECOV 0x080
#define MM_PRINT 0x100
#define MM_CONSOLE 0x200
#define MM_NOSEV 0
#define MM_HALT 1
#define MM_ERROR 2
#define MM_WARNING 3
#define MM_INFO 4
#define MM_NULLLBL ((char *)0)
#define MM_NULLSEV 0
#define MM_NULLMC ((long)0)
#define MM_NULLTXT ((char *)0)
#define MM_NULLACT ((char *)0)
#define MM_NULLTAG ((char *)0)
#define MM_NOTOK (-1)
#define MM_OK 0
#define MM_NOMSG 1
#define MM_NOCON 4
int fmtmsg(long classification, const char *label, int severity, const char *text, const char *action, const char *tag);
#endif
`,
	"ulimit.h": `#ifndef __CC_ULIMIT_H
#define __CC_ULIMIT_H
#define UL_GETFSIZE 1
#define UL_SETFSIZE 2
long ulimit(int cmd, ...);
#endif
`,
	"values.h": `#ifndef __CC_VALUES_H
#define __CC_VALUES_H
#include <limits.h>
#include <float.h>
#define _TYPEBITS(type) (sizeof(type) * CHAR_BIT)
#define CHARBITS _TYPEBITS(char)
#define SHORTBITS _TYPEBITS(short int)
#define INTBITS _TYPEBITS(int)
#define LONGBITS _TYPEBITS(long int)
#define PTRBITS _TYPEBITS(char *)
#define DOUBLEBITS _TYPEBITS(double)
#define FLOATBITS _TYPEBITS(float)
#define MINSHORT SHRT_MIN
#define MININT INT_MIN
#define MINLONG LONG_MIN
#define MAXSHORT SHRT_MAX
#define MAXINT INT_MAX
#define MAXLONG LONG_MAX
#define HIBITS MINSHORT
#define HIBITL MINLONG
#define MAXDOUBLE DBL_MAX
#define MAXFLOAT FLT_MAX
#define MINDOUBLE DBL_MIN
#define MINFLOAT FLT_MIN
#define DMINEXP DBL_MIN_EXP
#define FMINEXP FLT_MIN_EXP
#define DMAXEXP DBL_MAX_EXP
#define FMAXEXP FLT_MAX_EXP
#define BITSPERBYTE CHAR_BIT
#define _IEEE 1
#define _DEXPLEN 11
#define _FEXPLEN 8
#define _HIDDENBIT 1
#define DSIGNIF (DOUBLEBITS - _DEXPLEN + _HIDDENBIT - 1)
#define FSIGNIF (FLOATBITS - _FEXPLEN + _HIDDENBIT - 1)
#endif
`,
	"tar.h": `#ifndef __CC_TAR_H
#define __CC_TAR_H
#define TMAGIC "ustar"
#define TMAGLEN 6
#define TVERSION "00"
#define TVERSLEN 2
#define REGTYPE '0'
#define AREGTYPE '\0'
#define LNKTYPE '1'
#define SYMTYPE '2'
#define CHRTYPE '3'
#define BLKTYPE '4'
#define DIRTYPE '5'
#define FIFOTYPE '6'
#define CONTTYPE '7'
#define TSUID 04000
#define TSGID 02000
#define TSVTX 01000
#define TUREAD 00400
#define TUWRITE 00200
#define TUEXEC 00100
#define TGREAD 00040
#define TGWRITE 00020
#define TGEXEC 00010
#define TOREAD 00004
#define TOWRITE 00002
#define TOEXEC 00001
#endif
`,
	"cpio.h": `#ifndef __CC_CPIO_H
#define __CC_CPIO_H
#define C_IRUSR 000400
#define C_IWUSR 000200
#define C_IXUSR 000100
#define C_IRGRP 000040
#define C_IWGRP 000020
#define C_IXGRP 000010
#define C_IROTH 000004
#define C_IWOTH 000002
#define C_IXOTH 000001
#define C_ISUID 004000
#define C_ISGID 002000
#define C_ISVTX 001000
#define C_ISBLK 060000
#define C_ISCHR 020000
#define C_ISDIR 040000
#define C_ISFIFO 010000
#define C_ISSOCK 0140000
#define C_ISLNK 0120000
#define C_ISCTG 0110000
#define C_ISREG 0100000
#define MAGIC "070707"
#endif
`,
	"ar.h": `#ifndef __CC_AR_H
#define __CC_AR_H
#define ARMAG "!<arch>\n"
#define SARMAG 8
#define ARFMAG "` + "`" + `\n"
struct ar_hdr {
	char ar_name[16];
	char ar_date[12];
	char ar_uid[6];
	char ar_gid[6];
	char ar_mode[8];
	char ar_size[10];
	char ar_fmag[2];
};
#endif
`,
	"netinet/udp.h": `#ifndef __CC_NETINET_UDP_H
#define __CC_NETINET_UDP_H
#include <stdint.h>
struct udphdr {
	union {
		struct {
			uint16_t uh_sport;
			uint16_t uh_dport;
			uint16_t uh_ulen;
			uint16_t uh_sum;
		};
		struct {
			uint16_t source;
			uint16_t dest;
			uint16_t len;
			uint16_t check;
		};
	};
};
#define UDP_CORK 1
#define UDP_ENCAP 100
#define UDP_NO_CHECK6_TX 101
#define UDP_NO_CHECK6_RX 102
#define UDP_SEGMENT 103
#define UDP_GRO 104
#define UDP_ENCAP_ESPINUDP_NON_IKE 1
#define UDP_ENCAP_ESPINUDP 2
#define UDP_ENCAP_L2TPINUDP 3
#define SOL_UDP 17
#endif
`,
	"netinet/ip.h": `#ifndef __CC_NETINET_IP_H
#define __CC_NETINET_IP_H
#include <stdint.h>
#include <netinet/in.h>
struct iphdr {
	unsigned int ihl:4;
	unsigned int version:4;
	uint8_t tos;
	uint16_t tot_len;
	uint16_t id;
	uint16_t frag_off;
	uint8_t ttl;
	uint8_t protocol;
	uint16_t check;
	uint32_t saddr;
	uint32_t daddr;
};
struct ip {
	unsigned int ip_hl:4;
	unsigned int ip_v:4;
	uint8_t ip_tos;
	uint16_t ip_len;
	uint16_t ip_id;
	uint16_t ip_off;
	uint8_t ip_ttl;
	uint8_t ip_p;
	uint16_t ip_sum;
	struct in_addr ip_src, ip_dst;
};
#define IP_RF 0x8000
#define IP_DF 0x4000
#define IP_MF 0x2000
#define IP_OFFMASK 0x1fff
#define IPVERSION 4
#define IP_MAXPACKET 65535
#define IPTOS_ECN_MASK 0x03
#define IPTOS_ECN(x) ((x) & IPTOS_ECN_MASK)
#define IPTOS_ECN_NOT_ECT 0x00
#define IPTOS_ECN_ECT1 0x01
#define IPTOS_ECN_ECT0 0x02
#define IPTOS_ECN_CE 0x03
#define IPTOS_DSCP_MASK 0xfc
#define IPTOS_DSCP(x) ((x) & IPTOS_DSCP_MASK)
#define IPTOS_DSCP_AF11 0x28
#define IPTOS_DSCP_AF12 0x30
#define IPTOS_DSCP_AF13 0x38
#define IPTOS_DSCP_AF21 0x48
#define IPTOS_DSCP_AF22 0x50
#define IPTOS_DSCP_AF23 0x58
#define IPTOS_DSCP_AF31 0x68
#define IPTOS_DSCP_AF32 0x70
#define IPTOS_DSCP_AF33 0x78
#define IPTOS_DSCP_AF41 0x88
#define IPTOS_DSCP_AF42 0x90
#define IPTOS_DSCP_AF43 0x98
#define IPTOS_DSCP_EF 0xb8
#define IPTOS_CLASS_MASK 0xe0
#define IPTOS_CLASS(x) ((x) & IPTOS_CLASS_MASK)
#define IPTOS_CLASS_CS0 0x00
#define IPTOS_CLASS_CS1 0x20
#define IPTOS_CLASS_CS2 0x40
#define IPTOS_CLASS_CS3 0x60
#define IPTOS_CLASS_CS4 0x80
#define IPTOS_CLASS_CS5 0xa0
#define IPTOS_CLASS_CS6 0xc0
#define IPTOS_CLASS_CS7 0xe0
#define IPTOS_CLASS_DEFAULT IPTOS_CLASS_CS0
#define IPTOS_TOS_MASK 0x1E
#define IPTOS_TOS(tos) ((tos) & IPTOS_TOS_MASK)
#define IPTOS_LOWDELAY 0x10
#define IPTOS_THROUGHPUT 0x08
#define IPTOS_RELIABILITY 0x04
#define IPTOS_LOWCOST 0x02
#define IPTOS_MINCOST IPTOS_LOWCOST
#define IPTOS_PREC_MASK IPTOS_CLASS_MASK
#define IPTOS_PREC(tos) IPTOS_CLASS(tos)
#define IPTOS_PREC_NETCONTROL IPTOS_CLASS_CS7
#define IPTOS_PREC_INTERNETCONTROL IPTOS_CLASS_CS6
#define IPTOS_PREC_CRITIC_ECP IPTOS_CLASS_CS5
#define IPTOS_PREC_FLASHOVERRIDE IPTOS_CLASS_CS4
#define IPTOS_PREC_FLASH IPTOS_CLASS_CS3
#define IPTOS_PREC_IMMEDIATE IPTOS_CLASS_CS2
#define IPTOS_PREC_PRIORITY IPTOS_CLASS_CS1
#define IPTOS_PREC_ROUTINE IPTOS_CLASS_CS0
#define IPOPT_COPY 0x80
#define IPOPT_CLASS_MASK 0x60
#define IPOPT_NUMBER_MASK 0x1f
#define IPOPT_COPIED(o) ((o) & IPOPT_COPY)
#define IPOPT_CLASS(o) ((o) & IPOPT_CLASS_MASK)
#define IPOPT_NUMBER(o) ((o) & IPOPT_NUMBER_MASK)
#define IPOPT_CONTROL 0x00
#define IPOPT_RESERVED1 0x20
#define IPOPT_DEBMEAS 0x40
#define IPOPT_MEASUREMENT IPOPT_DEBMEAS
#define IPOPT_RESERVED2 0x60
#define IPOPT_EOL 0
#define IPOPT_END IPOPT_EOL
#define IPOPT_NOP 1
#define IPOPT_NOOP IPOPT_NOP
#define IPOPT_RR 7
#define IPOPT_TS 68
#define IPOPT_TIMESTAMP IPOPT_TS
#define IPOPT_SECURITY 130
#define IPOPT_SEC IPOPT_SECURITY
#define IPOPT_LSRR 131
#define IPOPT_SATID 136
#define IPOPT_SID IPOPT_SATID
#define IPOPT_SSRR 137
#define IPOPT_RA 148
#define IPOPT_OPTVAL 0
#define IPOPT_OLEN 1
#define IPOPT_OFFSET 2
#define IPOPT_MINOFF 4
#define MAX_IPOPTLEN 40
#define IPOPT_TS_TSONLY 0
#define IPOPT_TS_TSANDADDR 1
#define IPOPT_TS_PRESPEC 3
#define MAXTTL 255
#define IPDEFTTL 64
#define IPFRAGTTL 60
#define IPTTLDEC 1
#define IP_MSS 576
#endif
`,
	"arpa/nameser.h": `#ifndef __CC_ARPA_NAMESER_H
#define __CC_ARPA_NAMESER_H
#include <stdint.h>
#define NS_PACKETSZ 512
#define NS_MAXDNAME 1025
#define NS_MAXMSG 65535
#define NS_MAXCDNAME 255
#define NS_MAXLABEL 63
#define NS_HFIXEDSZ 12
#define NS_QFIXEDSZ 4
#define NS_RRFIXEDSZ 10
#define NS_INT32SZ 4
#define NS_INT16SZ 2
#define NS_INT8SZ 1
#define NS_INADDRSZ 4
#define NS_IN6ADDRSZ 16
#define NS_CMPRSFLGS 0xc0
#define NS_DEFAULTPORT 53
typedef enum __ns_class {
	ns_c_invalid = 0,
	ns_c_in = 1,
	ns_c_2 = 2,
	ns_c_chaos = 3,
	ns_c_hs = 4,
	ns_c_none = 254,
	ns_c_any = 255,
	ns_c_max = 65536
} ns_class;
typedef enum __ns_type {
	ns_t_invalid = 0,
	ns_t_a = 1,
	ns_t_ns = 2,
	ns_t_md = 3,
	ns_t_mf = 4,
	ns_t_cname = 5,
	ns_t_soa = 6,
	ns_t_mb = 7,
	ns_t_mg = 8,
	ns_t_mr = 9,
	ns_t_null = 10,
	ns_t_wks = 11,
	ns_t_ptr = 12,
	ns_t_hinfo = 13,
	ns_t_minfo = 14,
	ns_t_mx = 15,
	ns_t_txt = 16,
	ns_t_rp = 17,
	ns_t_afsdb = 18,
	ns_t_aaaa = 28,
	ns_t_loc = 29,
	ns_t_srv = 33,
	ns_t_naptr = 35,
	ns_t_cert = 37,
	ns_t_dname = 39,
	ns_t_opt = 41,
	ns_t_ds = 43,
	ns_t_sshfp = 44,
	ns_t_rrsig = 46,
	ns_t_nsec = 47,
	ns_t_dnskey = 48,
	ns_t_tlsa = 52,
	ns_t_https = 65,
	ns_t_spf = 99,
	ns_t_tkey = 249,
	ns_t_tsig = 250,
	ns_t_ixfr = 251,
	ns_t_axfr = 252,
	ns_t_any = 255,
	ns_t_caa = 257,
	ns_t_max = 65536
} ns_type;
typedef enum __ns_opcode {
	ns_o_query = 0,
	ns_o_iquery = 1,
	ns_o_status = 2,
	ns_o_notify = 4,
	ns_o_update = 5,
	ns_o_max = 6
} ns_opcode;
typedef enum __ns_rcode {
	ns_r_noerror = 0,
	ns_r_formerr = 1,
	ns_r_servfail = 2,
	ns_r_nxdomain = 3,
	ns_r_notimpl = 4,
	ns_r_refused = 5,
	ns_r_yxdomain = 6,
	ns_r_yxrrset = 7,
	ns_r_nxrrset = 8,
	ns_r_notauth = 9,
	ns_r_notzone = 10,
	ns_r_max = 11
} ns_rcode;
typedef enum __ns_sect {
	ns_s_qd = 0,
	ns_s_zn = 0,
	ns_s_an = 1,
	ns_s_pr = 1,
	ns_s_ns = 2,
	ns_s_ud = 2,
	ns_s_ar = 3,
	ns_s_max = 4
} ns_sect;
#define NS_GET16(s, cp) do { const unsigned char *t_cp = (const unsigned char *)(cp); (s) = ((uint16_t)t_cp[0] << 8) | (uint16_t)t_cp[1]; (cp) += NS_INT16SZ; } while (0)
#define NS_GET32(l, cp) do { const unsigned char *t_cp = (const unsigned char *)(cp); (l) = ((uint32_t)t_cp[0] << 24) | ((uint32_t)t_cp[1] << 16) | ((uint32_t)t_cp[2] << 8) | (uint32_t)t_cp[3]; (cp) += NS_INT32SZ; } while (0)
#define NS_PUT16(s, cp) do { uint16_t t_s = (uint16_t)(s); unsigned char *t_cp = (unsigned char *)(cp); *t_cp++ = t_s >> 8; *t_cp = t_s; (cp) += NS_INT16SZ; } while (0)
#define NS_PUT32(l, cp) do { uint32_t t_l = (uint32_t)(l); unsigned char *t_cp = (unsigned char *)(cp); *t_cp++ = t_l >> 24; *t_cp++ = t_l >> 16; *t_cp++ = t_l >> 8; *t_cp = t_l; (cp) += NS_INT32SZ; } while (0)
#define PACKETSZ NS_PACKETSZ
#define MAXDNAME NS_MAXDNAME
#define MAXCDNAME NS_MAXCDNAME
#define MAXLABEL NS_MAXLABEL
#define HFIXEDSZ NS_HFIXEDSZ
#define QFIXEDSZ NS_QFIXEDSZ
#define RRFIXEDSZ NS_RRFIXEDSZ
#define INT32SZ NS_INT32SZ
#define INT16SZ NS_INT16SZ
#define INT8SZ NS_INT8SZ
#define INADDRSZ NS_INADDRSZ
#define IN6ADDRSZ NS_IN6ADDRSZ
#define INDIR_MASK NS_CMPRSFLGS
#define NAMESERVER_PORT NS_DEFAULTPORT
#define QUERY ns_o_query
#define IQUERY ns_o_iquery
#define STATUS ns_o_status
#define NS_NOTIFY_OP ns_o_notify
#define NS_UPDATE_OP ns_o_update
#define NOERROR ns_r_noerror
#define FORMERR ns_r_formerr
#define SERVFAIL ns_r_servfail
#define NXDOMAIN ns_r_nxdomain
#define NOTIMP ns_r_notimpl
#define REFUSED ns_r_refused
#define C_IN ns_c_in
#define C_CHAOS ns_c_chaos
#define C_HS ns_c_hs
#define C_NONE ns_c_none
#define C_ANY ns_c_any
#define T_A ns_t_a
#define T_NS ns_t_ns
#define T_CNAME ns_t_cname
#define T_SOA ns_t_soa
#define T_NULL ns_t_null
#define T_WKS ns_t_wks
#define T_PTR ns_t_ptr
#define T_HINFO ns_t_hinfo
#define T_MINFO ns_t_minfo
#define T_MX ns_t_mx
#define T_TXT ns_t_txt
#define T_AAAA ns_t_aaaa
#define T_SRV ns_t_srv
#define T_NAPTR ns_t_naptr
#define T_OPT ns_t_opt
#define T_AXFR ns_t_axfr
#define T_ANY ns_t_any
#define GETSHORT NS_GET16
#define GETLONG NS_GET32
#define PUTSHORT NS_PUT16
#define PUTLONG NS_PUT32
typedef struct {
	unsigned id :16;
	unsigned rd :1;
	unsigned tc :1;
	unsigned aa :1;
	unsigned opcode :4;
	unsigned qr :1;
	unsigned rcode :4;
	unsigned cd :1;
	unsigned ad :1;
	unsigned unused :1;
	unsigned ra :1;
	unsigned qdcount :16;
	unsigned ancount :16;
	unsigned nscount :16;
	unsigned arcount :16;
} HEADER;
#endif
`,
	"resolv.h": `#ifndef __CC_RESOLV_H
#define __CC_RESOLV_H
#include <netinet/in.h>
#include <arpa/nameser.h>
#include <netdb.h>
#define RES_INIT 0x00000001
#define RES_DEBUG 0x00000002
#define RES_USEVC 0x00000008
#define RES_IGNTC 0x00000020
#define RES_RECURSE 0x00000040
#define RES_DEFNAMES 0x00000080
#define RES_STAYOPEN 0x00000100
#define RES_DNSRCH 0x00000200
#define RES_DEFAULT (RES_RECURSE | RES_DEFNAMES | RES_DNSRCH)
int res_init(void);
int res_query(const char *dname, int __class, int type, unsigned char *answer, int anslen);
int res_search(const char *dname, int __class, int type, unsigned char *answer, int anslen);
int res_querydomain(const char *name, const char *domain, int __class, int type, unsigned char *answer, int anslen);
#endif
`,
}
