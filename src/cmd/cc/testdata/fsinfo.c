// File systems' and the system's information, wordexp and the small
// headers of glibc and musl: the output must match gcc's (with glibc or
// musl). It prints only what both agree on, and runs in an empty
// directory, where it makes the files it walks and globs.
#define _GNU_SOURCE
#include <features.h>
#include <ar.h>
#include <arpa/inet.h>
#include <arpa/nameser.h>
#include <cpio.h>
#include <errno.h>
#include <fcntl.h>
#include <fmtmsg.h>
#include <ftw.h>
#include <ifaddrs.h>
#include <libintl.h>
#include <limits.h>
#include <locale.h>
#include <monetary.h>
#include <net/if.h>
#include <netinet/in.h>
#include <netinet/ip.h>
#include <netinet/udp.h>
#include <nl_types.h>
#include <paths.h>
#include <resolv.h>
#include <stdio.h>
#include <stdio_ext.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/statvfs.h>
#include <sys/sysinfo.h>
#include <sys/sysmacros.h>
#include <sys/times.h>
#include <sys/vfs.h>
#include <sysexits.h>
#include <tar.h>
#include <ulimit.h>
#include <unistd.h>
#include <values.h>
#include <wordexp.h>

static void touch(const char *path, const char *text) {
	FILE *f = fopen(path, "w");
	fputs(text, f);
	fclose(f);
}

static void testStatvfs(void) {
	struct statvfs v;
	struct statfs s;
	printf("statvfs: %d", statvfs(".", &v));
	printf(" bsize %d frsize %d namemax %d", v.f_bsize > 0, v.f_frsize > 0, v.f_namemax > 0);
	printf(" blocks %d bfree %d\n", v.f_blocks > 0 && v.f_blocks >= v.f_bfree, v.f_bfree >= v.f_bavail);
	int fd = open(".", O_RDONLY);
	struct statvfs fv;
	int r = fstatvfs(fd, &fv);
	printf("fstatvfs: %d same %d\n", r, fv.f_blocks == v.f_blocks && fv.f_bsize == v.f_bsize);
	printf("statfs: %d", statfs(".", &s));
	printf(" bsize %d blocks %d namelen %d\n", s.f_bsize > 0, s.f_blocks == v.f_blocks, s.f_namelen == v.f_namemax);
	struct statfs fs;
	r = fstatfs(fd, &fs);
	printf("fstatfs: %d same %d\n", r, fs.f_type == s.f_type && fs.f_blocks == s.f_blocks);
	close(fd);
	errno = 0;
	r = statvfs("no/such/dir", &v);
	printf("statvfs missing: %d %d\n", r, errno == ENOENT);
	errno = 0;
	r = fstatvfs(-1, &v);
	printf("fstatvfs bad: %d %d\n", r, errno == EBADF);
	printf("sizes: %zu %zu\n", sizeof(struct statvfs), sizeof(struct statfs));
}

static const char *ftwFlag(int f) {
	switch (f) {
	case FTW_F:
		return "F";
	case FTW_D:
		return "D";
	case FTW_DNR:
		return "DNR";
	case FTW_NS:
		return "NS";
	case FTW_SL:
		return "SL";
	case FTW_DP:
		return "DP";
	case FTW_SLN:
		return "SLN";
	}
	return "?";
}

static char seen[32][128];
static int nseen;
static int dpAfterChildren;

static int collect(const char *path, const struct stat *sb, int flag, struct FTW *ftw) {
	char size[32] = "";
	if (flag == FTW_F)
		snprintf(size, sizeof size, " size %lld", (long long)sb->st_size);
	if (flag == FTW_D || flag == FTW_DP)
		snprintf(size, sizeof size, " dir %d", S_ISDIR(sb->st_mode));
	snprintf(seen[nseen++], sizeof seen[0], "%s %s level %d base %d%s", path, ftwFlag(flag), ftw->level, ftw->base, size);
	if (flag == FTW_DP) {
		// Every entry under path must have come before.
		size_t n = strlen(path);
		for (int i = 0; i < nseen - 1; i++)
			if (strncmp(seen[i], path, n) == 0 && seen[i][n] == ' ')
				dpAfterChildren = 0;
	}
	return 0;
}

static int cmp(const void *a, const void *b) { return strcmp(a, b); }

static void walk(const char *name, int flags) {
	nseen = 0;
	dpAfterChildren = 1;
	int r = nftw("tree", collect, 4, flags);
	qsort(seen, nseen, sizeof seen[0], cmp);
	printf("nftw %s: %d\n", name, r);
	for (int i = 0; i < nseen; i++)
		printf("  %s\n", seen[i]);
	if (flags & FTW_DEPTH)
		printf("  dp after children: %d\n", dpAfterChildren);
}

static int stopAt(const char *path, const struct stat *sb, int flag, struct FTW *ftw) {
	return strcmp(path, "tree/b/c") == 0 ? 7 : 0;
}

static int ftwCollect(const char *path, const struct stat *sb, int flag) {
	snprintf(seen[nseen++], sizeof seen[0], "%s %s", path, ftwFlag(flag));
	return 0;
}

static void testFtw(void) {
	mkdir("tree", 0755);
	mkdir("tree/b", 0755);
	touch("tree/a", "hello");
	touch("tree/b/c", "hi");
	walk("plain", 0);
	walk("depth", FTW_DEPTH);
	symlink("a", "tree/l");
	symlink("nowhere", "tree/dangling");
	walk("phys", FTW_PHYS);
	walk("follow", 0);
	walk("phys depth", FTW_PHYS | FTW_DEPTH);
	printf("nftw stop: %d\n", nftw("tree", stopAt, 4, FTW_PHYS));
	errno = 0;
	int r = nftw("no/such", stopAt, 4, 0);
	printf("nftw missing: %d %d\n", r, errno == ENOENT);

	mkdir("t2", 0755);
	mkdir("t2/x", 0755);
	touch("t2/x/y", "");
	nseen = 0;
	printf("ftw: %d\n", ftw("t2", ftwCollect, 4));
	qsort(seen, nseen, sizeof seen[0], cmp);
	for (int i = 0; i < nseen; i++)
		printf("  %s\n", seen[i]);
}

static void expand(const char *words, int flags) {
	wordexp_t we;
	int r = wordexp(words, &we, flags);
	printf("wordexp [%s]: %d", words, r);
	if (r == 0) {
		printf(" %zu:", we.we_wordc);
		for (size_t i = 0; i < we.we_wordc; i++)
			printf(" <%s>", we.we_wordv[i]);
		wordfree(&we);
	}
	printf("\n");
}

static void testWordexp(void) {
	setenv("HOME", "/home/test", 1);
	setenv("FSV", "one two", 1);
	setenv("FSE", "", 1);
	setenv("FSP", "dir/sub/file.c", 1);
	setenv("FSN", "21", 1);
	unsetenv("FSU");
	unsetenv("IFS");
	touch("g1.txt", "");
	touch("g2.txt", "");
	touch(".g3.txt", "");
	expand("a b  c", 0);
	expand("$FSV", 0);
	expand("\"$FSV\"", 0);
	expand("${FSV}x", 0);
	expand("x$FSE y \"$FSE\"", 0);
	expand("'$FSV' \"a b\" c\\ d", 0);
	expand("~ ~/x a~", 0);
	expand("${FSU:-def} ${FSV:+alt} ${FSE:-empty} ${FSE-unset} ${#FSV}", 0);
	expand("${FSP%.c} ${FSP#*/} ${FSP##*/} ${FSP%%/*} ${FSP%/*}", 0);
	expand("$((1+2*3)) $((FSN*2)) $(((7-1)/2)) $((-5%3)) $((1<<4|1))", 0);
	expand("g*.txt", 0);
	expand("*.none 'g*' g\\*", 0);
	expand(".g*", 0);
	expand("$(echo hi there) \"$(echo a  b)\" `echo x`", 0);
	expand("\"abc", 0);
	expand("'abc", 0);
	expand("$(echo hi)", WRDE_NOCMD);
	expand("`echo hi`", WRDE_NOCMD);
	expand("${FSV", 0);
	expand("$FSU", 0);
	expand("x${FSU}y", 0);

	wordexp_t we;
	we.we_offs = 2;
	int r = wordexp("one two", &we, WRDE_DOOFFS);
	printf("dooffs: %d %zu %d %d %s %s %d\n", r, we.we_wordc, we.we_wordv[0] == NULL, we.we_wordv[1] == NULL,
	       we.we_wordv[2], we.we_wordv[3], we.we_wordv[4] == NULL);
	r = wordexp("three", &we, WRDE_DOOFFS | WRDE_APPEND);
	printf("append: %d %zu %s %s %s %d\n", r, we.we_wordc, we.we_wordv[2], we.we_wordv[3], we.we_wordv[4],
	       we.we_wordv[5] == NULL);
	wordfree(&we);
}

static void testTimes(void) {
	struct tms t;
	volatile double x = 0;
	for (int i = 0; i < 1000000; i++)
		x += i;
	clock_t r = times(&t);
	printf("times: %d %d %d size %zu\n", r != (clock_t)-1, t.tms_utime >= 0 && t.tms_stime >= 0,
	       t.tms_cutime >= 0 && t.tms_cstime >= 0, sizeof(struct tms));
}

static void testSysinfo(void) {
	struct sysinfo si;
	printf("sysinfo: %d", sysinfo(&si));
	printf(" uptime %d ram %d free %d unit %d procs %d\n", si.uptime > 0, si.totalram > 0,
	       si.freeram > 0 && si.freeram <= si.totalram, si.mem_unit >= 1, si.procs > 0);
	int n = get_nprocs(), conf = get_nprocs_conf();
	printf("nprocs: %d %d\n", n > 0, conf >= n);
	long phys = get_phys_pages(), av = get_avphys_pages();
	printf("pages: %d %d\n", phys > 0, av > 0 && av <= phys);
}

static void testSysmacros(void) {
	dev_t d = makedev(8, 1);
	printf("makedev: %llu %u %u\n", (unsigned long long)d, major(d), minor(d));
	d = makedev(0x12345, 0x6789ab);
	printf("makedev big: %llx %x %x\n", (unsigned long long)d, major(d), minor(d));
	struct stat st;
	stat(".", &st);
	printf("stat dev: %d\n", makedev(major(st.st_dev), minor(st.st_dev)) == st.st_dev);
}

static void testIfaddrs(void) {
	struct ifaddrs *ifa;
	if (getifaddrs(&ifa) != 0) {
		printf("getifaddrs failed\n");
		return;
	}
	int lo4 = 0, packets = 0;
	for (struct ifaddrs *p = ifa; p; p = p->ifa_next) {
		if (strcmp(p->ifa_name, "lo") != 0 || p->ifa_addr == NULL)
			continue;
		if (p->ifa_addr->sa_family == AF_PACKET)
			packets++;
		if (p->ifa_addr->sa_family == AF_INET) {
			struct sockaddr_in *a = (struct sockaddr_in *)p->ifa_addr;
			struct sockaddr_in *m = (struct sockaddr_in *)p->ifa_netmask;
			char as[INET_ADDRSTRLEN], ms[INET_ADDRSTRLEN];
			inet_ntop(AF_INET, &a->sin_addr, as, sizeof as);
			inet_ntop(AF_INET, &m->sin_addr, ms, sizeof ms);
			if (strcmp(as, "127.0.0.1") == 0) {
				lo4++;
				printf("lo: %s/%s up %d loopback %d\n", as, ms, (p->ifa_flags & IFF_UP) != 0,
				       (p->ifa_flags & IFF_LOOPBACK) != 0);
			}
		}
	}
	printf("lo ipv4 %d packet %d\n", lo4, packets);
	freeifaddrs(ifa);
}

static void testStdioExt(void) {
	FILE *f = fopen("ext.txt", "w");
	printf("ext: readable %d writable %d", __freadable(f) != 0, __fwritable(f) != 0);
	fputs("abc", f);
	printf(" pending %zu writing %d", __fpending(f), __fwriting(f) != 0);
	fflush(f);
	printf(" flushed %zu", __fpending(f));
	fputs("xyz", f);
	__fpurge(f);
	printf(" purged %zu lbf %d\n", __fpending(f), __flbf(f));
	fclose(f);
	f = fopen("ext.txt", "r");
	char buf[16] = "";
	fgets(buf, sizeof buf, f);
	printf("ext: reading %d writable %d content %s\n", __freading(f) != 0, __fwritable(f) != 0, buf);
	fclose(f);
	_flushlbf();
}

static void testIntl(void) {
	const char *m = "hello";
	printf("gettext: %d %s %s\n", gettext(m) == m, dgettext("dom", "msg"), dcgettext("dom", "cmsg", LC_MESSAGES));
	printf("ngettext: %s %s %s %s\n", ngettext("file", "files", 1), ngettext("file", "files", 2),
	       dngettext("d", "dir", "dirs", 0), dcngettext("d", "x", "xs", 1, LC_MESSAGES));
	printf("textdomain: %s", textdomain(NULL));
	printf(" %s", textdomain("myapp"));
	printf(" %s\n", textdomain(NULL));
	printf("bindtextdomain: %s\n", bindtextdomain("myapp", "/opt/locale"));
	printf("bindtextdomain again: %s\n", bindtextdomain("myapp", NULL));
	printf("codeset: %s\n", bind_textdomain_codeset("myapp", "UTF-8"));
}

static void testMonetary(void) {
	char buf[100];
	memset(buf, 0, sizeof buf); // musl's strfmon does not end the string
	ssize_t n = strfmon(buf, sizeof buf, "[%n] [%i] [%10n] [%.3n] [%.0n] [%n] 100%%", 1234.56, 1234.56, 1234.56, 2.5,
			    2.5, -7.25);
	printf("strfmon: %zd %s\n", n, buf);
	errno = 0;
	n = strfmon(buf, 3, "%n", 1234.56);
	printf("strfmon small: %zd %d\n", n, errno == E2BIG);
}

static void testCatalogs(void) {
	errno = 0;
	nl_catd c = catopen("no-such-catalog-here", 0);
	printf("catopen: %d %d\n", c == (nl_catd)-1, errno == ENOENT);
}

static void testFmtmsg(void) {
	unsetenv("MSGVERB");
	fflush(stderr);
	int saved = dup(2);
	int fd = open("fmtmsg.txt", O_WRONLY | O_CREAT | O_TRUNC, 0644);
	dup2(fd, 2);
	close(fd);
	int r = fmtmsg(MM_PRINT | MM_SOFT | MM_UTIL, "cmd:sub", MM_ERROR, "something failed", MM_NULLACT, MM_NULLTAG);
	int r2 = fmtmsg(MM_PRINT, MM_NULLLBL, MM_INFO, "just text", MM_NULLACT, MM_NULLTAG);
	fflush(stderr);
	dup2(saved, 2);
	close(saved);
	printf("fmtmsg: %d %d\n", r, r2);
	FILE *f = fopen("fmtmsg.txt", "r");
	char line[128];
	while (fgets(line, sizeof line, f))
		printf("  %s", line);
	fclose(f);
}

static void testConstants(void) {
	printf("ulimit: %d\n", ulimit(UL_GETFSIZE) > 0);
	printf("sysexits: %d %d %d %d\n", EX_OK, EX_USAGE, EX_SOFTWARE, EX_CONFIG);
	printf("paths: %s %s %s\n", _PATH_DEVNULL, _PATH_BSHELL, _PATH_TTY);
	printf("values: %d %d %d %d %d\n", MAXINT == INT_MAX, MAXLONG == LONG_MAX, BITSPERBYTE, (int)INTBITS, HIBITS);
	printf("tar: %s %d %s %c %c %o %o\n", TMAGIC, TMAGLEN, TVERSION, REGTYPE, DIRTYPE, TUREAD, TSVTX);
	printf("cpio: %s %o %o %o\n", MAGIC, C_ISDIR, C_ISLNK, C_IRUSR);
	struct ar_hdr h;
	printf("ar: %d %d %zu %zu\n", memcmp(ARMAG, "!<arch>\n", SARMAG) == 0, strcmp(ARFMAG, "`\n") == 0, sizeof h,
	       sizeof h.ar_size);
	struct udphdr u;
	memset(&u, 0, sizeof u);
	u.uh_dport = htons(53);
	printf("udp: %zu %d\n", sizeof u, ntohs(u.dest));
	struct ip ip;
	memset(&ip, 0, sizeof ip);
	ip.ip_v = 4;
	ip.ip_hl = 5;
	printf("ip: %zu %zu %#x %#x %#x %#x %d %d\n", sizeof ip, sizeof(struct iphdr), *(unsigned char *)&ip, IPTOS_LOWDELAY,
	       IP_DF, IPTOS_PREC_CRITIC_ECP, MAXTTL, IPVERSION);
	printf("nameser: %d %d %d %d %d %zu\n", NS_PACKETSZ, NS_MAXDNAME, T_AAAA, C_IN, ns_t_mx, sizeof(HEADER));
	unsigned char msg[4] = {1, 2, 3, 4}, *cp = msg;
	unsigned v16;
	NS_GET16(v16, cp);
	printf("get16: %#x %d\n", v16, (int)(cp - msg));
	printf("resolv: %d\n", res_init());
	printf("if: %#x %#x %#x %#x\n", IFF_UP, IFF_LOOPBACK, IFF_RUNNING, IFF_MULTICAST);
}

int main(void) {
	testStatvfs();
	testFtw();
	testWordexp();
	testTimes();
	testSysinfo();
	testSysmacros();
	testIfaddrs();
	testStdioExt();
	testIntl();
	testMonetary();
	testCatalogs();
	testFmtmsg();
	testConstants();
	return 0;
}
