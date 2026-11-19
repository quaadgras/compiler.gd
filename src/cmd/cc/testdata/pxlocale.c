// Locales and the _l functions, wide characters' widths and POSIX's
// string, wide string, random number, crypt, time, Bessel and inet
// functions. It prints only what glibc and musl agree on.

#include <arpa/inet.h>
#include <crypt.h>
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <langinfo.h>
#include <locale.h>
#include <math.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>
#include <unistd.h>
#include <wchar.h>
#include <wctype.h>

// Only _GNU_SOURCE declares these in musl.
double strtod_l(const char *restrict s, char **restrict end, locale_t loc);

static locale_t cloc, uloc;

static void *thread(void *arg) {
	printf("thread starts global: %d\n", uselocale(0) == LC_GLOBAL_LOCALE);
	uselocale(uloc);
	printf("thread uses its own: %d\n", uselocale(0) == uloc);
	return 0;
}

static void locales(void) {
	printf("== locale\n");
	cloc = newlocale(LC_ALL_MASK, "C", 0);
	uloc = newlocale(LC_ALL_MASK, "C.UTF-8", 0);
	locale_t p = newlocale(LC_CTYPE_MASK | LC_NUMERIC_MASK, "POSIX", 0);
	printf("newlocale: %d %d %d\n", cloc != 0, uloc != 0, p != 0);
	locale_t d = duplocale(uloc);
	printf("duplocale: %d\n", d != 0);
	freelocale(d);
	freelocale(p);
	printf("global at first: %d\n", uselocale(0) == LC_GLOBAL_LOCALE);
	locale_t old = uselocale(cloc);
	printf("uselocale returns the global: %d, now cloc: %d\n", old == LC_GLOBAL_LOCALE, uselocale(0) == cloc);
	pthread_t t;
	pthread_create(&t, 0, thread, 0);
	pthread_join(t, 0);
	printf("main still cloc: %d\n", uselocale(0) == cloc);
	uselocale(LC_GLOBAL_LOCALE);
	printf("back to global: %d\n", uselocale(0) == LC_GLOBAL_LOCALE);
	// musl's C.UTF-8 is LC_CTYPE's only: setlocale(LC_ALL) differs.
	printf("setlocale C.UTF-8: %d\n", setlocale(LC_ALL, "C.UTF-8") != 0);
	printf("query: %s\n", setlocale(LC_CTYPE, 0));
	printf("setlocale C: %s\n", setlocale(LC_ALL, "C"));
	printf("nl_langinfo_l CODESET: %s\n", nl_langinfo_l(CODESET, uloc));
	printf("nl_langinfo_l DAY_1: %s\n", nl_langinfo_l(DAY_1, cloc));

	printf("== _l\n");
	int cs[] = {'a', 'Z', '5', ' ', '\t', '!', 'f', 'g', 0x7f, '\n'};
	for (int i = 0; i < 10; i++) {
		int c = cs[i];
		printf("%#x: %d%d%d%d%d%d%d%d%d%d%d%d %c%c %d%d\n", c,
			!!isalnum_l(c, cloc), !!isalpha_l(c, cloc), !!isblank_l(c, cloc), !!iscntrl_l(c, cloc),
			!!isdigit_l(c, cloc), !!isgraph_l(c, cloc), !!islower_l(c, cloc), !!isprint_l(c, cloc),
			!!ispunct_l(c, cloc), !!isspace_l(c, cloc), !!isupper_l(c, cloc), !!isxdigit_l(c, cloc),
			isprint(c) ? tolower_l(c, cloc) : '.', isprint(c) ? toupper_l(c, uloc) : '.',
			!!isascii(c), toascii(c | 0x80));
	}
	printf("isascii(0x80) %d toascii(0xe9) %#x\n", !!isascii(0x80), toascii(0xe9));
	wint_t ws[] = {'a', 'Z', '7', 0xe9, 0xc9, 0x3b1, 0x4e00, 0x5d0, ' ', 0x416};
	for (int i = 0; i < 10; i++) {
		wint_t c = ws[i];
		printf("%#x: %d%d%d%d%d%d%d%d%d%d %#x %#x %d %#x %#x\n", c,
			!!iswalnum_l(c, uloc), !!iswalpha_l(c, uloc), !!iswblank_l(c, uloc), !!iswcntrl_l(c, uloc),
			!!iswdigit_l(c, uloc), !!iswgraph_l(c, uloc), !!iswlower_l(c, uloc), !!iswprint_l(c, uloc),
			!!iswupper_l(c, uloc), !!iswxdigit_l(c, uloc), towlower_l(c, uloc), towupper_l(c, uloc),
			!!iswctype_l(c, wctype_l("alpha", uloc), uloc),
			towctrans_l(c, wctrans_l("toupper", uloc), uloc), towctrans_l(c, wctrans_l("tolower", uloc), uloc));
	}
	printf("iswspace_l/iswpunct_l: %d %d\n", !!iswspace_l(' ', uloc), !!iswpunct_l('!', uloc));
	printf("strcoll_l: %d %d %d\n", strcoll_l("abc", "abd", cloc) < 0, strcoll_l("b", "a", cloc) > 0,
		strcoll_l("same", "same", uloc));
	char xf[16];
	size_t xn = strxfrm_l(xf, "hello", sizeof xf, cloc);
	printf("strxfrm_l: %zu %d\n", xn, strcmp(xf, "hello") == 0);
	printf("strerror_l: %d\n", strcmp(strerror_l(EINVAL, cloc), strerror(EINVAL)) == 0);
	printf("strcasecmp_l: %d %d %d\n", strcasecmp_l("HeLLo", "hello", cloc),
		strncasecmp_l("ABCx", "abcy", 3, cloc), strcasecmp_l("a", "B", cloc) < 0);
	printf("wcscasecmp_l: %d %d\n", wcscasecmp_l(L"HÉllo", L"héLLO", uloc), wcsncasecmp_l(L"ABx", L"aby", 2, uloc));
	printf("wcscoll_l: %d %d\n", wcscoll_l(L"a", L"b", uloc) < 0, wcscoll_l(L"x", L"x", uloc));
	wchar_t wxf[8];
	printf("wcsxfrm_l: %zu\n", wcsxfrm_l(wxf, L"abc", 8, uloc));
	struct tm tm = {.tm_sec = 5, .tm_min = 4, .tm_hour = 13, .tm_mday = 9, .tm_mon = 6, .tm_year = 99, .tm_wday = 5, .tm_yday = 189};
	char tb[64];
	size_t tn = strftime_l(tb, sizeof tb, "%A %d %B %Y %H:%M:%S %j", &tm, cloc);
	printf("strftime_l: %zu %s\n", tn, tb);
	char *end;
	double dv = strtod_l("3.25e2xyz", &end, cloc);
	printf("strtod_l: %g %s\n", dv, end);
}

static void widths(void) {
	printf("== wchar\n");
	// glibc's C locale is ASCII's.
	setlocale(LC_CTYPE, "C.UTF-8");
	wchar_t cs[] = {0, 'A', '\n', 0x7f, 0x85, 0xe9, 0x300, 0x36f, 0x1160, 0x200b, 0x4e00, 0xac00, 0xff21,
		0x3042, 0x1f600, 0x20000, 0x2329, 0xfffe, 0x20ac, 0x3b1};
	for (int i = 0; i < sizeof cs / sizeof cs[0]; i++)
		printf("wcwidth(%#x) = %d\n", cs[i], wcwidth(cs[i]));
	printf("wcswidth: %d %d %d %d\n", wcswidth(L"abc一", 10), wcswidth(L"áb", 10),
		wcswidth(L"ab\ncd", 10), wcswidth(L"ab\ncd", 2));
	wchar_t *dup = wcsdup(L"dup me");
	printf("wcsdup: %ls\n", dup);
	free(dup);
	wchar_t wb[16];
	wchar_t *e = wcpcpy(wb, L"hello");
	printf("wcpcpy: %d %ls\n", (int)(e - wb), wb);
	wmemset(wb, L'z', 16);
	e = wcpncpy(wb, L"abc", 6);
	printf("wcpncpy: %d %d %d\n", (int)(e - wb), wb[3] == 0 && wb[5] == 0, wb[6] == L'z');
	e = wcpncpy(wb, L"abcdef", 3);
	printf("wcpncpy short: %d %d\n", (int)(e - wb), wb[3] == 0);
	printf("wcsnlen: %zu %zu\n", wcsnlen(L"hello", 3), wcsnlen(L"hi", 10));
	printf("wcscasecmp: %d %d %d\n", wcscasecmp(L"HeLLo", L"hello"), wcscasecmp(L"a", L"B") < 0,
		wcsncasecmp(L"Hello", L"HELP", 3));
	const char *src = "h\xc3\xa9llo";
	const char *sp = src;
	wchar_t wd[16];
	mbstate_t st = {0};
	size_t n = mbsnrtowcs(wd, &sp, 3, 16, &st);
	printf("mbsnrtowcs: %zu %d\n", n, (int)(sp - src));
	n = mbsnrtowcs(wd, &sp, 10, 16, &st);
	printf("mbsnrtowcs: %zu %d %ls\n", n, sp == 0, wd);
	sp = src;
	n = mbsnrtowcs(0, &sp, 10, 0, &st);
	printf("mbsnrtowcs count: %zu\n", n);
	const wchar_t *wp = L"héllo";
	char mb[16];
	memset(mb, 'x', sizeof mb);
	n = wcsnrtombs(mb, &wp, 3, sizeof mb, &st);
	printf("wcsnrtombs: %zu %d %.4s\n", n, (int)(wp - L"héllo"), mb);
	wp = L"héllo";
	n = wcsnrtombs(mb, &wp, 10, sizeof mb, &st);
	printf("wcsnrtombs: %zu %d %s\n", n, wp == 0, mb);
	setlocale(LC_CTYPE, "C");
	printf("wcswcs: %ls %d\n", wcswcs(L"haystack", L"st"), wcswcs(L"abc", L"x") == 0);
	wchar_t *buf;
	size_t sz;
	FILE *f = open_wmemstream(&buf, &sz);
	// musl's wide memory streams lose what is not ASCII.
	fwprintf(f, L"x=%d %ls", 42, L"yz");
	fflush(f);
	printf("open_wmemstream: %zu %ls %d\n", sz, buf, buf[sz] == 0);
	fputwc(L'!', f);
	fclose(f);
	printf("open_wmemstream: %zu %d\n", sz, buf[sz - 1] == L'!');
	free(buf);
}

static void strs(void) {
	printf("== string\n");
	const char *h = "abcabcabd";
	printf("memmem: %d %d %d %d\n", (int)((char *)memmem(h, 9, "abd", 3) - h), memmem(h, 9, "abe", 3) == 0,
		(char *)memmem(h, 9, "", 0) == h, memmem(h, 2, "abc", 3) == 0);
	char buf[100];
	int r = strerror_r(EINVAL, buf, sizeof buf);
	printf("strerror_r: %d %d\n", r, strcmp(buf, strerror(EINVAL)) == 0);
	r = strerror_r(EINVAL, buf, 5);
	printf("strerror_r short: %d %d %d\n", r == ERANGE, (int)strlen(buf), strncmp(buf, strerror(EINVAL), 4) == 0);
}

static void rands(void) {
	printf("== stdlib\n");
	srand48(1234);
	for (int i = 0; i < 3; i++)
		printf("drand48 %.12f\n", drand48());
	for (int i = 0; i < 3; i++)
		printf("lrand48 %ld\n", lrand48());
	for (int i = 0; i < 3; i++)
		printf("mrand48 %ld\n", mrand48());
	unsigned short x[3] = {1, 2, 3};
	double e = erand48(x);
	long nr = nrand48(x), jr = jrand48(x);
	printf("erand48 %.12f nrand48 %ld jrand48 %ld\n", e, nr, jr);
	printf("xsubi %u %u %u\n", x[0], x[1], x[2]);
	unsigned short s[3] = {0x1234, 0x5678, 0x9abc};
	unsigned short *old = seed48(s);
	printf("seed48 old %u %u %u\n", old[0], old[1], old[2]);
	printf("after seed48 %ld\n", lrand48());
	// musl's srand48 and seed48 do not restore lcong48's a and c.
	unsigned short p[7] = {0x1234, 0x5678, 0x9abc, 0xe66d, 0xdeec, 0x5, 0xb};
	lcong48(p);
	long l1 = lrand48(), l2 = lrand48();
	printf("lcong48 %ld %ld\n", l1, l2);
	p[3] = 3;
	lcong48(p);
	l1 = lrand48(), l2 = lrand48();
	printf("lcong48 %ld %ld %.12f\n", l1, l2, drand48());

	// musl's random has a table and seeding of its own: only TYPE_0's,
	// rand's LCG, is glibc's.
	static char st8[8];
	char *def = initstate(7, st8, sizeof st8);
	for (int i = 0; i < 3; i++)
		printf("random TYPE_0 %ld\n", random());
	char *back = setstate(def);
	printf("setstate returns the state %d\n", back == st8);

	printf("l64a: [%s]", l64a(0));
	printf(" [%s]", l64a(1));
	printf(" [%s]\n", l64a(12345678));
	printf("a64l: %ld %ld %ld\n", a64l("."), a64l("/"), a64l("Ezk.y."));
	printf("a64l(l64a): %ld\n", a64l(l64a(0x12345678)));

	char opts[] = "ro,rw=yes,size=10,unknown,ro";
	char *const tokens[] = {"ro", "rw", "size", 0};
	char *o = opts, *v;
	int k;
	while (*o) {
		k = getsubopt(&o, tokens, &v);
		// glibc's value of an unknown suboption is the suboption, musl's null.
		printf("getsubopt %d %s\n", k, k >= 0 && v ? v : "-");
	}

	char t1[] = "pxlXXXXXX";
	int fd = mkostemp(t1, O_CLOEXEC);
	printf("mkostemp: %d %d %d\n", fd >= 0, strncmp(t1, "pxl", 3) == 0 && strcmp(t1 + 3, "XXXXXX") != 0,
		(fcntl(fd, F_GETFD) & FD_CLOEXEC) != 0);
	close(fd);
	unlink(t1);
	char t2[] = "pxlXXXXXX.txt";
	fd = mkstemps(t2, 4);
	printf("mkstemps: %d %d\n", fd >= 0, strcmp(t2 + 9, ".txt") == 0 && strncmp(t2 + 3, "XXXXXX", 6) != 0);
	close(fd);
	unlink(t2);
	char t3[] = "pxlXXXXXXab";
	fd = mkostemps(t3, 2, 0);
	printf("mkostemps: %d %d\n", fd >= 0, strcmp(t3 + 9, "ab") == 0);
	close(fd);
	unlink(t3);
	char t4[] = "pxlXXXXX.c";
	errno = 0;
	fd = mkstemps(t4, 2);
	printf("mkstemps bad: %d %d\n", fd, errno == EINVAL);
}

static void crypts(void) {
	printf("== crypt\n");
	const char *salts[] = {
		"$1$saltsalt$", "$1$", "$1$abcdefghijk$",
		"$5$saltstring", "$5$rounds=10000$saltstringsaltstring", "$5$rounds=1000$roundstoolow",
		"$6$saltstring", "$6$rounds=5000$toolongsaltstring", "$6$rounds=1400$anotherlongsaltstring",
		"ab", "./", "Xy",
	};
	for (int i = 0; i < sizeof salts / sizeof salts[0]; i++)
		printf("crypt(%s) = %s\n", salts[i], crypt(i % 3 == 2 ? "a much longer password of many words" : "password", salts[i]));
	printf("crypt empty key: %s\n", crypt("", "$6$x"));
	printf("crypt DES long key: %s\n", crypt("12345678ignored", "zz"));
	struct crypt_data cd;
	memset(&cd, 0, sizeof cd);
	printf("crypt_r: %s\n", crypt_r("hello", "$5$abc", &cd));

	char key[64], block[64];
	for (int i = 0; i < 64; i++)
		key[i] = (0x133457799BBCDFF1ull >> (63 - i)) & 1;
	for (int i = 0; i < 64; i++)
		block[i] = (0x0123456789ABCDEFull >> (63 - i)) & 1;
	setkey(key);
	encrypt(block, 0);
	unsigned long long c = 0;
	for (int i = 0; i < 64; i++)
		c = c << 1 | block[i];
	printf("encrypt: %016llx\n", c);
	encrypt(block, 1);
	c = 0;
	for (int i = 0; i < 64; i++)
		c = c << 1 | block[i];
	printf("decrypt: %016llx\n", c);
}

static void ptm(const char *what, struct tm *tm) {
	printf("%s: %d-%02d-%02d %02d:%02d:%02d\n", what, tm->tm_year + 1900, tm->tm_mon + 1, tm->tm_mday,
		tm->tm_hour, tm->tm_min, tm->tm_sec);
}

static void times(void) {
	printf("== time\n");
	struct {
		const char *s, *f;
	} cases[] = {
		{"2024-02-29 13:45:07", "%Y-%m-%d %H:%M:%S"},
		{"Thu, 29 Feb 2024 01:02:03 PM", "%a, %d %b %Y %I:%M:%S %p"},
		{"thursday february 1 99", "%A %B %e %y"},
		{"12/31/68 23:59", "%D %R"},
		{"20 24-1-2", "%C %y-%m-%d"},
		{"12:30:00 am", "%r"},
		{"100%", "%y%%"},
		{"2024-07-04T05:06:07", "%FT%T"},
		{"Sept 3", "%b %d"},
		{"xx", "%d"},
		{"32", "%d"},
		{"2024-01-01 tail", "%Y-%m-%d"},
	};
	for (int i = 0; i < sizeof cases / sizeof cases[0]; i++) {
		struct tm tm;
		memset(&tm, 0, sizeof tm);
		char *e = strptime(cases[i].s, cases[i].f, &tm);
		if (!e) {
			printf("strptime(%s, %s) = NULL\n", cases[i].s, cases[i].f);
			continue;
		}
		printf("strptime(%s, %s) +%d: ", cases[i].s, cases[i].f, (int)(e - cases[i].s));
		ptm("", &tm);
	}
	struct tm tm;
	memset(&tm, 0, sizeof tm);
	strptime("Wednesday", "%A", &tm);
	printf("strptime %%A wday %d\n", tm.tm_wday);
	strptime("060", "%j", &tm);
	printf("strptime %%j yday %d\n", tm.tm_yday);

	setenv("TZ", "UTC", 1);
	tzset();
	printf("tzset UTC: %s %ld %d\n", tzname[0], timezone, daylight);
	setenv("TZ", "America/New_York", 1);
	tzset();
	printf("tzset New York: %s %s %ld %d\n", tzname[0], tzname[1], timezone, daylight);
	time_t t0 = 0;
	struct tm lt;
	localtime_r(&t0, &lt);
	ptm("localtime New York", &lt);
	char ab[32];
	printf("ctime_r: %s", ctime_r(&t0, ab));
	setenv("TZ", "XYZ3", 1);
	tzset();
	printf("tzset XYZ3: %s %ld %d\n", tzname[0], timezone, daylight);
	localtime_r(&t0, &lt);
	ptm("localtime XYZ3", &lt);
	setenv("TZ", "EST5EDT,M3.2.0,M11.1.0", 1);
	tzset();
	printf("tzset EST5EDT: %s %s %ld %d\n", tzname[0], tzname[1], timezone, daylight);
	time_t summer = 1720000000;
	localtime_r(&summer, &lt);
	ptm("localtime EST5EDT summer", &lt);
	printf("isdst %d\n", lt.tm_isdst);
	setenv("TZ", "UTC", 1);
	tzset();
	struct tm g;
	gmtime_r(&summer, &g);
	printf("asctime_r: %s", asctime_r(&g, ab));
	printf("ctime_r UTC: %s", ctime_r(&summer, ab));
}

static void bessel(void) {
	printf("== math\n");
	double xs[] = {0.5, 1, 2.5, 10};
	for (int i = 0; i < 4; i++) {
		double x = xs[i];
		printf("%g: j0 %.6f j1 %.6f jn(3) %.6f y0 %.6f y1 %.6f yn(2) %.6f\n", x, j0(x), j1(x), jn(3, x),
			y0(x), y1(x), yn(2, x));
	}
}

static void inets(void) {
	printf("== inet\n");
	const char *ns[] = {"10.1.2.3", "0x7f.0.0.1", "127", "1.2.3.4.5", "", "256.1", "1.2.3."};
	for (int i = 0; i < sizeof ns / sizeof ns[0]; i++)
		printf("inet_network(%s) = %#x\n", ns[i], inet_network(ns[i]));
	// musl's inet_lnaof and inet_makeaddr are in the host's byte order:
	// internal/c23/arpa/inet's test has glibc's, as do inet_network's of
	// fewer than four parts (musl's are inet_addr's).
}

int main(void) {
	locales();
	widths();
	strs();
	rands();
	crypts();
	times();
	bessel();
	inets();
	return 0;
}
