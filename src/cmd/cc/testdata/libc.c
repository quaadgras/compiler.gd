// C's library: the output must match gcc's (with glibc or musl).
#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <limits.h>
#include <locale.h>
#include <math.h>
#include <memory.h>
#include <pthread.h>
#include <pwd.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/types.h>
#include <time.h>
#include <uchar.h>
#include <unistd.h>
#include <utime.h>
#include <wchar.h>

static void testMath(void) {
	double xs[] = {0.5, 1.0, 2.5, -3.75, 10.0};
	for (int i = 0; i < 5; i++) {
		double x = xs[i];
		printf("%g: sin %.6g cos %.6g tan %.6g atan %.6g sinh %.6g cosh %.6g tanh %.6g\n",
			x, sin(x), cos(x), tan(x), atan(x), sinh(x), cosh(x), tanh(x));
		printf("  exp %.6g exp2 %.6g expm1 %.6g cbrt %.6g fabs %g floor %g ceil %g round %g trunc %g rint %g\n",
			exp(x), exp2(x), expm1(x), cbrt(x), fabs(x), floor(x), ceil(x), round(x), trunc(x), rint(x));
		if (x > 0)
			printf("  log %.6g log2 %.6g log10 %.6g log1p %.6g sqrt %.6g\n", log(x), log2(x), log10(x), log1p(x), sqrt(x));
		printf("  sinf %.6g expf %.6g floorf %g sqrtf %.6g lround %ld llrint %lld\n",
			sinf((float)x), expf((float)x), floorf((float)x), sqrtf(fabsf((float)x)), lround(x), llrint(x));
	}
	printf("asin %.6g acos %.6g atan2 %.6g %.6g\n", asin(0.5), acos(0.5), atan2(1, -1), atan2f(-1, -1));
	printf("pow %.6g %.6g %g hypot %.6g fmod %g %g remainder %g %g\n",
		pow(2, 10), pow(2.5, -1.5), powf(3, 4), hypot(3, 4), fmod(7.5, 2), fmod(-7.5, 2), remainder(7.5, 2), remainder(5, 2));
	printf("fmin %g fmax %g fdim %g %g fma %g copysign %g %g\n",
		fmin(1, 2), fmax(1, 2), fdim(5, 3), fdim(3, 5), fma(2, 3, 4), copysign(3, -1), copysignf(-2, 1));
	printf("round %g %g %g rint %g %g nearbyint %g trunc %g\n",
		round(2.5), round(-2.5), roundf(0.5f), rint(2.5), rint(3.5), nearbyint(-1.5), trunc(-2.7));
	int e;
	double m = frexp(48, &e);
	double ip;
	double fp = modf(-3.25, &ip);
	float ipf;
	float fpf = modff(7.5f, &ipf);
	printf("frexp %g %d ldexp %g scalbn %g modf %g %g modff %g %g ilogb %d logb %g\n",
		m, e, ldexp(0.75, 4), scalbn(1, 10), fp, ip, fpf, ipf, ilogb(1000), logb(1000));
	printf("nextafter %d %d erf %.6g erfc %.6g tgamma %g lgamma %.6g\n",
		nextafter(1.0, 2.0) > 1.0, nextafterf(1.0f, 0.0f) < 1.0f, erf(0.5), erfc(0.5), tgamma(5), lgamma(10));
	printf("long double %.6Lg %.6Lg\n", sinl(1.0L), sqrtl(2.0L));
	printf("M_PI %.10f M_E %.10f M_SQRT2 %.10f\n", M_PI, M_E, M_SQRT2);

	double inf = INFINITY, qnan = NAN, zero = 0.0, sub = 1e-310;
	float subf = 1e-40f;
	printf("inf %g %g %d\n", inf, -HUGE_VAL, HUGE_VALF == INFINITY);
	printf("isnan %d %d %d isinf %d %d %d isfinite %d %d %d isnormal %d %d\n",
		!!isnan(qnan), !!isnan(inf), !!isnan(1.0f), !!isinf(inf), !!isinf(-inf), !!isinf(qnan),
		!!isfinite(1.0), !!isfinite(inf), !!isfinite(qnan), !!isnormal(1.0), !!isnormal(sub));
	printf("fpclassify %d %d %d %d %d %d\n", fpclassify(qnan) == FP_NAN, fpclassify(inf) == FP_INFINITE,
		fpclassify(zero) == FP_ZERO, fpclassify(sub) == FP_SUBNORMAL, fpclassify(subf) == FP_SUBNORMAL,
		fpclassify(1.0f) == FP_NORMAL);
	printf("signbit %d %d %d %d\n", !!signbit(-0.0), !!signbit(0.0), !!signbit(-1.0f), !!signbit(-inf));
	printf("isgreater %d isless %d isunordered %d %d\n", isgreater(2.0, 1.0), isless(2.0, 1.0),
		isunordered(1.0, qnan), isunordered(1.0, 2.0));
	printf("nan %d %d fmax %g fmin %g\n", !!isnan(nan("")), !!isnan(nanf("1")), fmax(qnan, 1), fmin(2, qnan));

	errno = 0;
	double r = sqrt(-1);
	printf("sqrt(-1) %d %d\n", !!isnan(r), !(math_errhandling & MATH_ERRNO) || errno == EDOM);
	errno = 0;
	r = log(0);
	printf("log(0) %g %d\n", r, !(math_errhandling & MATH_ERRNO) || errno == ERANGE);
	errno = 0;
	r = exp(1000);
	printf("exp(1000) %g %d\n", r, !(math_errhandling & MATH_ERRNO) || errno == ERANGE);
}

static void testCtype(void) {
	const char *s = "aZ5 \t\n_!~\x7f";
	for (const char *p = s; *p; p++) {
		int c = (unsigned char)*p;
		printf("%3d: %d%d%d%d%d%d%d%d%d%d%d%d %c%c\n", c, !!isalnum(c), !!isalpha(c), !!isblank(c),
			!!iscntrl(c), !!isdigit(c), !!isgraph(c), !!islower(c), !!isprint(c), !!ispunct(c),
			!!isspace(c), !!isupper(c), !!isxdigit(c), isprint(tolower(c)) ? tolower(c) : '.',
			isprint(toupper(c)) ? toupper(c) : '.');
	}
	int n = 0;
	for (int c = 0; c < 256; c++)
		n += !!isalpha(c) + !!isdigit(c) * 2 + !!ispunct(c) * 4 + !!isspace(c) * 8 + !!isxdigit(c) * 16;
	printf("ctype sum %d EOF %d %d %d\n", n, !!isalpha(EOF), tolower(EOF), toupper('a'));
}

static void testInttypes(void) {
	int8_t a = -5;
	int16_t b = 1000;
	int32_t c = -100000;
	int64_t d = INT64_MIN;
	uint64_t u = UINT64_MAX;
	uintptr_t up = 0xdeadbeef;
	intmax_t m = -42;
	printf("%" PRId8 " %" PRIi16 " %" PRId32 " %" PRId64 " %" PRIu64 "\n", a, b, c, d, u);
	printf("%" PRIx32 " %" PRIX64 " %" PRIo16 " %" PRIxPTR " %" PRIdMAX " %" PRIdFAST16 "\n",
		(uint32_t)255, (uint64_t)0xabcdef, (uint16_t)8, up, m, (int_fast16_t)7);
	char *end;
	intmax_t v = strtoimax("  -123abc", &end, 10);
	uintmax_t w = strtoumax("0x1F", NULL, 0);
	printf("imaxabs %" PRIdMAX " strtoimax %" PRIdMAX " [%s] strtoumax %" PRIuMAX "\n", imaxabs(m), v, end, w);
}

static void testErrno(void) {
	printf("errno %d %d %d %d %d\n", EDOM, ERANGE, EILSEQ, EINVAL, ENOMEM);
	errno = 0;
	long l = strtol("99999999999999999999", NULL, 10);
	printf("strtol %ld %d\n", l, errno == ERANGE);
	errno = 0;
	unsigned long ul = strtoul("123", NULL, 10);
	printf("strtoul %lu %d\n", ul, errno);
	errno = EINVAL;
	int *ep = &errno;
	*ep += 1;
	printf("errno %d\n", errno);
	printf("strerror %d %d\n", strerror(ERANGE) != NULL && strerror(ERANGE)[0] != 0,
		strerror(EDOM) != strerror(ERANGE));
}

static void testUchar(void) {
	const char *s = "a\xc3\xa9\xe2\x82\xac\xf0\x9f\x98\x80";
	mbstate_t st = {0};
	char32_t c32;
	const char *p = s;
	size_t n;
	while ((n = mbrtoc32(&c32, p, 8, &st)) != 0 && n < (size_t)-3) {
		printf("c32 U+%04X (%zu)\n", (unsigned)c32, n);
		p += n;
	}
	char16_t c16;
	p = s;
	memset(&st, 0, sizeof st);
	for (;;) {
		n = mbrtoc16(&c16, p, 8, &st);
		if (n == 0)
			break;
		if (n == (size_t)-3) {
			printf("c16 %04X (stored)\n", c16);
			continue;
		}
		printf("c16 %04X (%zu)\n", c16, n);
		p += n;
	}
	// A character split across calls.
	memset(&st, 0, sizeof st);
	n = mbrtoc32(&c32, "\xe2\x82", 2, &st);
	printf("partial %d mbsinit %d\n", n == (size_t)-2, !!mbsinit(&st));
	n = mbrtoc32(&c32, "\xac", 1, &st);
	printf("rest %zu U+%04X mbsinit %d\n", n, (unsigned)c32, !!mbsinit(&st));
	errno = 0;
	n = mbrtoc32(&c32, "\xff", 1, &st);
	printf("invalid %d %d\n", n == (size_t)-1, errno == EILSEQ);

	char buf[16];
	memset(&st, 0, sizeof st);
	n = c32rtomb(buf, 0x20AC, &st);
	printf("c32rtomb %zu %02x %02x %02x\n", n, (unsigned char)buf[0], (unsigned char)buf[1], (unsigned char)buf[2]);
	char16_t pair[] = {0xD83D, 0xDE00};
	n = c16rtomb(buf, pair[0], &st);
	printf("c16rtomb high %zu\n", n);
	n = c16rtomb(buf, pair[1], &st);
	printf("c16rtomb low %zu %02x %02x %02x %02x\n", n, (unsigned char)buf[0], (unsigned char)buf[1],
		(unsigned char)buf[2], (unsigned char)buf[3]);
	errno = 0;
	n = c32rtomb(buf, 0xD800, &st);
	printf("c32rtomb surrogate %d %d\n", n == (size_t)-1, errno == EILSEQ);

	wchar_t wc;
	memset(&st, 0, sizeof st);
	n = mbrtowc(&wc, "\xc3\xa9", 2, &st);
	printf("mbrtowc %zu %d mbrlen %zu wcrtomb %zu\n", n, (int)wc, mbrlen("\xe2\x82\xac", 3, &st), wcrtomb(buf, L'x', &st));
	printf("btowc %u %d wctob %d %d\n", btowc('A'), btowc(200) == WEOF, wctob(L'z'), wctob(0x20AC));

	const wchar_t *ws = L"héllo";
	wchar_t wbuf[32];
	wcscpy(wbuf, ws);
	wcscat(wbuf, L" w");
	printf("wcslen %zu %zu wcscmp %d %d %d wcsncmp %d wcschr %td\n", wcslen(ws), wcslen(wbuf),
		wcscmp(ws, wbuf) < 0, wcscmp(wbuf, wbuf), wcscmp(L"b", L"a") > 0, wcsncmp(ws, wbuf, 5),
		wcschr(wbuf, L'w') - wbuf);

#ifdef __GD_CC__
	// C23's UTF-8 functions, which musl lacks: checked here, printed as
	// gcc's run prints them.
	char8_t c8;
	memset(&st, 0, sizeof st);
	int ok = mbrtoc8(&c8, "\xe2\x82\xac", 3, &st) == 3 && c8 == 0xe2 &&
		mbrtoc8(&c8, "", 0, &st) == (size_t)-3 && c8 == 0x82 &&
		mbrtoc8(&c8, "", 0, &st) == (size_t)-3 && c8 == 0xac && mbsinit(&st);
	ok = ok && c8rtomb(buf, 0xe2, &st) == 0 && c8rtomb(buf, 0x82, &st) == 0 &&
		c8rtomb(buf, 0xac, &st) == 3 && memcmp(buf, "\xe2\x82\xac", 3) == 0;
	ok = ok && c8rtomb(buf, 'x', &st) == 1 && buf[0] == 'x';
	printf("c8 %d\n", ok);
	char z[4] = "abc";
	memset_explicit(z, 0, 3);
	printf("memset_explicit %d\n", z[0] + z[1] + z[2]);
#else
	printf("c8 1\n");
	printf("memset_explicit 0\n");
#endif
}

static void testTime(void) {
	time_t t;
	time_t now = time(&t);
	clock_t c = clock();
	printf("time %d %d clock %d %g %d\n", now > 1700000000, now == t, c >= 0, difftime(10, 4),
		(int)CLOCKS_PER_SEC);
}

// testTm uses only UTC: the zone of the program is the environment's.
static void testTm(void) {
	time_t t = 1700000000;
	struct tm *tm = gmtime(&t);
	printf("gmtime %d %d %d %d %d %d %d %d %d\n", tm->tm_year, tm->tm_mon, tm->tm_mday, tm->tm_hour,
		tm->tm_min, tm->tm_sec, tm->tm_wday, tm->tm_yday, tm->tm_isdst);
	printf("asctime %s", asctime(tm));
	char buf[256];
	size_t k = strftime(buf, sizeof buf, "%a %A %b %B %C %d %D %e %F %g %G %h %H %I %j %m %M %n %p %r "
		"%R %S %t %T %u %U %V %w %W %x %X %y %Y %% %c %Ey %OH", tm);
	printf("strftime %zu [%s]\n", k, buf);
	printf("strftime short %zu\n", strftime(buf, 7, "%Y-%m", tm));
	struct tm tt = {0};
	tt.tm_year = 124;
	tt.tm_mon = 13;
	tt.tm_mday = 35;
	tt.tm_hour = 25;
	tt.tm_min = -5;
	time_t tg = timegm(&tt);
	printf("timegm %ld: %d %d %d %d %d %d %d\n", (long)tg, tt.tm_year, tt.tm_mon, tt.tm_mday,
		tt.tm_hour, tt.tm_min, tt.tm_wday, tt.tm_yday);
	time_t ts[] = {0, 946684800, 1104537600, 1230681600, 1293753600, 1609459200, -86400 * 365, 951782400};
	for (int i = 0; i < 8; i++) {
		struct tm b;
		gmtime_r(&ts[i], &b);
		strftime(buf, sizeof buf, "%G-W%V-%u %g %U %W %j %I%p", &b);
		printf("week %s\n", buf);
	}
	struct tm lt;
	localtime_r(&t, &lt);
	time_t back = mktime(&lt);
	printf("mktime %d %d\n", back == t, localtime(&t)->tm_min == lt.tm_min);
	printf("ctime %d\n", strlen(ctime(&t)) == 25);
	struct timespec sp;
	int base = timespec_get(&sp, TIME_UTC);
	printf("timespec_get %d %d\n", base == TIME_UTC,
		sp.tv_sec > 1700000000 && sp.tv_nsec >= 0 && sp.tv_nsec < 1000000000);
}

static volatile sig_atomic_t caught;
static void onSignal(int sig) { caught = sig; }

static void testSignal(void) {
	int dfl = signal(SIGINT, onSignal) == SIG_DFL;
	int r = raise(SIGINT);
	printf("signal %d raise %d caught %d\n", dfl, r, caught);
	int prev = signal(SIGTERM, SIG_IGN) == SIG_DFL;
	caught = 0;
	r = raise(SIGTERM);
	printf("ignored %d raise %d caught %d\n", prev, r, caught);
	prev = signal(SIGTERM, onSignal) == SIG_IGN;
	raise(SIGTERM);
	printf("handler %d caught %d\n", prev, caught);
	prev = signal(SIGINT, SIG_DFL) == onSignal;
	signal(SIGTERM, SIG_DFL);
	printf("restored %d %d\n", prev, SIGABRT + SIGFPE + SIGILL + SIGSEGV);
}

// testLocale runs after the tests that need a UTF-8 locale.
static void testLocale(void) {
	struct lconv *lc = localeconv();
	printf("lconv [%s] [%s] [%s] [%s] [%s] %d %d %d\n", lc->decimal_point, lc->thousands_sep,
		lc->grouping, lc->currency_symbol, lc->int_curr_symbol, lc->frac_digits == CHAR_MAX,
		lc->p_sign_posn == CHAR_MAX, lc->int_n_sign_posn == CHAR_MAX);
	printf("setlocale %s", setlocale(LC_NUMERIC, "C"));
	printf(" %s", setlocale(LC_NUMERIC, "POSIX"));
	printf(" %s\n", setlocale(LC_NUMERIC, NULL));
}

struct rec {
	const char *name;
	int key;
};

static int cmpInt(const void *a, const void *b) {
	int x = *(const int *)a, y = *(const int *)b;
	return (x > y) - (x < y);
}

static int cmpRec(const void *a, const void *b) {
	return strcmp(((const struct rec *)a)->name, ((const struct rec *)b)->name);
}

static void bye1(void) { printf("bye 1\n"); }
static void bye2(void) { printf("bye 2\n"); }

static void testStdlib(void) {
	int a[] = {5, -2, 9, 0, 3, 3, 100, -50, 7, 1, 8, 2};
	size_t n = sizeof a / sizeof a[0];
	qsort(a, n, sizeof a[0], cmpInt);
	for (size_t i = 0; i < n; i++)
		printf("%d ", a[i]);
	printf("\n");
	int key = 7, missing = 4;
	int *f = bsearch(&key, a, n, sizeof a[0], cmpInt);
	printf("bsearch %td %d\n", f - a, bsearch(&missing, a, n, sizeof a[0], cmpInt) == NULL);

	struct rec rs[] = {{"pear", 1}, {"apple", 2}, {"fig", 3}, {"kiwi", 4}, {"banana", 5}};
	qsort(rs, 5, sizeof rs[0], cmpRec);
	for (int i = 0; i < 5; i++)
		printf("%s=%d ", rs[i].name, rs[i].key);
	printf("\n");
	struct rec k = {"kiwi", 0};
	struct rec *found = bsearch(&k, rs, 5, sizeof rs[0], cmpRec);
	printf("found %s %d\n", found->name, found->key);

	char *end;
	float fl = strtof("3.25e2xyz", &end);
	long double ld = strtold("-0.125", NULL);
	double hx = strtod("0x1.8", NULL);
	printf("strtof %g [%s] strtold %Lg hex %g\n", fl, end, ld, hx);
	errno = 0;
	double big = strtod("1e999", NULL);
	printf("strtod %g %d\n", big, errno == ERANGE);

	atexit(bye1);
	atexit(bye2);
}

static void testDiv(void) {
	div_t d = div(-7, 2);
	ldiv_t ld = ldiv(7L, -2L);
	lldiv_t lld = lldiv(-9LL, -4LL);
	imaxdiv_t im = imaxdiv(100, 7);
	printf("div %d %d ldiv %ld %ld lldiv %lld %lld imaxdiv %jd %jd\n", d.quot, d.rem, ld.quot, ld.rem,
		lld.quot, lld.rem, im.quot, im.rem);
	fflush(stdout);
	int sh = system(NULL) != 0;
	int st = system("exit 3");
	int ok = system("true");
	printf("system %d %d %d\n", sh, st, ok);
}

static void testScanf(void) {
	int a = 0, n = 0, pos = 0;
	unsigned u = 0, x = 0;
	char s[20], c[3], set[20];
	double f = 0;
	float g = 0;
	long l = 0;
	short h = 0;
	signed char hh = 0;
	long long ll = 0;
	int r = sscanf("  42 0x1F 17 ff hello abc!def 3.5e2 -1.25", "%d %i %o %x %s %3c %[a-z!]%lf %f",
		&a, &n, &u, &x, s, c, set, &f, &g);
	printf("sscanf %d: %d %d %u %u %s %.3s %s %g %g\n", r, a, n, u, x, s, c, set, f, g);
	r = sscanf("12abc 99", "%2d%*[a-c]%n %ld", &a, &pos, &l);
	printf("sscanf %d: %d %d %ld\n", r, a, pos, l);
	printf("sscanf empty %d\n", sscanf("", "%d", &a));
	printf("sscanf blank %d\n", sscanf("   ", " %d", &a));
	printf("sscanf mismatch %d\n", sscanf("x", "%d", &a));
	printf("sscanf 100ergs %d\n", sscanf("100ergs", "%lf%s", &f, s));
	r = sscanf("-12 % 7 0777 -0x10 300", "%hd %% %i %i %i %hhd", &h, &a, &n, &x, &hh);
	printf("sscanf %d: %d %d %d %d\n", r, h, a, n, (int)x);
	r = sscanf("inf -nan 0x1p4 1e-2", "%lf %lf %lf %lf", &f, &f, &f, &f);
	printf("sscanf %d %g\n", r, f);
	printf("sscanf literal %d %d\n", sscanf("abc", "abd"), sscanf("abc5", "abc%d", &a));
	r = sscanf("9223372036854775807 x:y", "%lld %[^:]:%1s", &ll, set, s);
	printf("sscanf %d: %lld %s %s\n", r, ll, set, s);
	r = sscanf("123456", "%3d%2u%c", &a, &u, c);
	printf("sscanf %d: %d %u %c\n", r, a, u, c[0]);
	r = sscanf(".5 -.25e+1 0.", "%f %f %f", &g, &g, &g);
	printf("sscanf %d %g\n", r, g);

	FILE *fp = fopen("libc_scan.tmp", "w");
	fprintf(fp, "10 20\nword 2.5\n");
	fclose(fp);
	fp = fopen("libc_scan.tmp", "r");
	r = fscanf(fp, "%d %d %s %lf", &a, &n, s, &f);
	printf("fscanf %d: %d %d %s %g\n", r, a, n, s, f);
	r = fscanf(fp, "%d", &a);
	printf("fscanf eof %d %d\n", r, !!feof(fp));
	fclose(fp);
	fp = fopen("libc_scan.tmp", "r");
	r = fscanf(fp, "%d", &a);
	int next = fgetc(fp);
	printf("fscanf %d %d next %d\n", r, a, next);

	// A stream opened for reading only fails writes at once.
	int e = fputs("x", fp);
	printf("fputs read-only %d error %d\n", e, !!ferror(fp));
	clearerr(fp);
	size_t w = fwrite("ab", 1, 2, fp);
	int pc = fputc('a', fp);
	int pf = fprintf(fp, "x");
	printf("fwrite %zu fputc %d fprintf %d error %d\n", w, pc, pf < 0, !!ferror(fp));
	printf("fclose %d\n", fclose(fp));
	fp = fopen("libc_scan.tmp", "a");
	int gc = fgetc(fp);
	printf("fgetc write-only %d error %d\n", gc, !!ferror(fp));
	FILE *fp2 = freopen("libc_scan.tmp", "r", fp);
	int same = fp2 == fp;
	gc = fgetc(fp);
	printf("freopen %d %c\n", same, gc);
	fclose(fp);
	remove("libc_scan.tmp");
	char tn[L_tmpnam];
	int self = tmpnam(tn) == tn;
	printf("tmpnam %d %d %d %d\n", self, strlen(tn) < L_tmpnam, tmpnam(NULL) != NULL, TMP_MAX >= 25);
}

static void testString(void) {
	char s[] = "  the quick,brown  fox,,jumps ";
	for (char *t = strtok(s, " ,"); t; t = strtok(NULL, " ,"))
		printf("[%s]", t);
	printf("\n");
	char s2[] = "a=1;b=2";
	char *save;
	for (char *t = strtok_r(s2, ";", &save); t; t = strtok_r(NULL, ";", &save))
		printf("<%s>", t);
	printf("\n");
	printf("strpbrk %s %d\n", strpbrk("hello, world", " ,"), strpbrk("abc", "xyz") == NULL);
	printf("strnlen %zu %zu strcoll %d %d\n", strnlen("hello", 3), strnlen("hi", 10),
		strcoll("abc", "abd") < 0, strcoll("b", "a") > 0);
	printf("strerror %s|%s|%s|%s\n", strerror(ENOENT), strerror(EBADF), strerror(EACCES), strerror(ENOTDIR));
}

static void testStdio(void) {
	FILE *f = tmpfile();
	if (!f) {
		printf("tmpfile failed\n");
		return;
	}
	fprintf(f, "line one\nline %d\n", 2);
	fputs("third\n", f);
	printf("ftell %ld\n", ftell(f));
	rewind(f);
	char buf[64];
	while (fgets(buf, sizeof buf, f))
		printf("read: %s", buf);
	printf("feof %d ferror %d\n", !!feof(f), !!ferror(f));
	clearerr(f);
	printf("feof %d\n", !!feof(f));
	fseek(f, 5, SEEK_SET);
	int c = fgetc(f);
	printf("at 5: %c ftell %ld\n", c, ftell(f));
	ungetc('X', f);
	c = fgetc(f);
	printf("ungetc: %c ftell %ld\n", c, ftell(f));
	fseek(f, -6, SEEK_END);
	printf("from end: %c\n", fgetc(f));
	fseek(f, 0, SEEK_SET);
	fputs("LINE", f);
	fseek(f, 0, SEEK_SET);
	fgets(buf, sizeof buf, f);
	printf("overwritten: %s", buf);
	size_t k = fread(buf, 1, sizeof buf, f);
	printf("fread %zu feof %d\n", k, !!feof(f));
	printf("fclose %d\n", fclose(f));

	f = fopen("libc_test.tmp", "w");
	fwrite("12345", 1, 5, f);
	fclose(f);
	printf("rename %d\n", rename("libc_test.tmp", "libc_test2.tmp"));
	f = fopen("libc_test2.tmp", "a+");
	fputs("678", f);
	fseek(f, 0, SEEK_SET);
	k = fread(buf, 1, sizeof buf - 1, f);
	buf[k] = 0;
	printf("appended %s\n", buf);
	fclose(f);
	int r1 = remove("libc_test2.tmp");
	int r2 = remove("libc_test2.tmp");
	printf("remove %d %d\n", r1, r2);
	errno = 0;
	f = fopen("libc_test_missing/x", "r");
	printf("fopen missing %d %d\n", f == NULL, errno == ENOENT);
	printf("setvbuf %d\n", setvbuf(stdout, NULL, _IOFBF, BUFSIZ));
}

// POSIX: unistd.h, fcntl.h, sys/stat.h, dirent.h, strings.h, sys/time.h.

// show prints what a call returned, and errno if it failed.
static void show(const char *what, long r) {
	if (r < 0)
		printf("%s %ld errno %d\n", what, r, errno);
	else
		printf("%s %ld\n", what, r);
}

static int cmpStr(const void *a, const void *b) { return strcmp(*(char *const *)a, *(char *const *)b); }

static void testFd(void) {
	umask(022);
	show("mkdir", mkdir("posix_test.d", 0755));
	show("mkdir again", mkdir("posix_test.d", 0755));
	show("chdir", chdir("posix_test.d"));
	char cwd[4096];
	char *c = getcwd(cwd, sizeof cwd);
	size_t n = strlen(cwd);
	printf("getcwd %d %s\n", c == cwd, n > 12 ? cwd + n - 12 : cwd);
	c = getcwd(cwd + 100, 2);
	printf("getcwd small %d %d\n", c == NULL, errno == ERANGE);
	char *dyn = getcwd(NULL, 0);
	printf("getcwd null %d\n", dyn != NULL && strcmp(dyn, cwd) == 0);

	int fd = open("a.txt", O_WRONLY | O_CREAT | O_TRUNC, 0644);
	show("open", fd);
	show("write", write(fd, "hello, posix\n", 13));
	show("lseek", lseek(fd, 0, SEEK_CUR));
	int fl = fcntl(fd, F_GETFL);
	printf("F_GETFL %d %d\n", fl & O_ACCMODE, !!(fl & O_APPEND));
	show("F_GETFD", fcntl(fd, F_GETFD));
	show("F_SETFD", fcntl(fd, F_SETFD, FD_CLOEXEC));
	show("F_GETFD", fcntl(fd, F_GETFD));
	show("close", close(fd));
	show("close again", close(fd));
	show("write closed", write(fd, "x", 1));

	fd = open("a.txt", O_RDONLY);
	char buf[64];
	ssize_t r = read(fd, buf, 5);
	buf[r] = 0;
	printf("read %ld %s\n", (long)r, buf);
	show("lseek end", lseek(fd, -6, SEEK_END));
	r = read(fd, buf, sizeof buf - 1);
	buf[r] = 0;
	printf("read %ld %s", (long)r, buf);
	show("read eof", read(fd, buf, 10));
	show("write rdonly", write(fd, "x", 1));
	show("lseek bad", lseek(fd, 0, 7));
	int fd2 = dup(fd);
	show("dup", fd2);
	lseek(fd2, 7, SEEK_SET);
	show("shared offset", lseek(fd, 0, SEEK_CUR));
	show("dup2", dup2(fd, 9));
	show("F_DUPFD", fcntl(fd, F_DUPFD, 5));
	show("dup2 bad", dup2(42, 10));
	close(9);
	close(5);
	close(fd2);
	struct stat st;
	show("fstat", fstat(fd, &st));
	printf("  size %ld reg %d mode %o\n", (long)st.st_size, S_ISREG(st.st_mode), st.st_mode & 0777);
	close(fd);
	show("fstat bad", fstat(fd, &st));

	show("open missing", open("missing", O_RDONLY));
	show("open excl", open("a.txt", O_WRONLY | O_CREAT | O_EXCL, 0600));
	show("open dir", open("a.txt", O_RDONLY | O_DIRECTORY));
	fd = open("a.txt", O_WRONLY | O_APPEND);
	fl = fcntl(fd, F_GETFL);
	printf("append %d %d\n", fl & O_ACCMODE, !!(fl & O_APPEND));
	write(fd, "more\n", 5);
	close(fd);
	fd = creat("b.txt", 0600);
	write(fd, "b", 1);
	close(fd);

	show("stat", stat("a.txt", &st));
	printf("  size %ld reg %d dir %d mode %o\n", (long)st.st_size, S_ISREG(st.st_mode), S_ISDIR(st.st_mode), st.st_mode & 0777);
	printf("  nlink %ld mtime %d %d blksize %d uid %d\n", (long)st.st_nlink, st.st_mtime > 1000000000,
		st.st_mtim.tv_nsec >= 0 && st.st_mtim.tv_nsec < 1000000000, st.st_blksize > 0, st.st_uid == getuid());
	stat("b.txt", &st);
	printf("creat mode %o\n", st.st_mode & 0777);
	show("chmod", chmod("b.txt", 0640));
	stat("b.txt", &st);
	printf("chmod mode %o\n", st.st_mode & 0777);
	show("stat .", stat(".", &st));
	printf("  dir %d\n", S_ISDIR(st.st_mode));
	show("stat missing", stat("missing", &st));
	show("lstat", lstat("a.txt", &st));
	printf("  reg %d\n", S_ISREG(st.st_mode));
	printf("sizeof stat %d dirent %d\n", (int)sizeof(struct stat), (int)sizeof(struct dirent));
	mode_t m1 = umask(077);
	mode_t m2 = umask(022);
	printf("umask %o %o\n", m1, m2);

	show("access", access("a.txt", F_OK));
	show("access rw", access("a.txt", R_OK | W_OK));
	show("access missing", access("missing", F_OK));
	show("mkdir sub", mkdir("sub", 0700));
	FILE *f = fopen("sub/c.txt", "w");
	show("fileno", fileno(f));
	fputs("via FILE\n", f);
	fflush(f);
	write(fileno(f), "via fd\n", 7);
	fclose(f);
	fd = open("sub/c.txt", O_RDONLY);
	f = fdopen(fd, "r");
	printf("fdopen %d %d\n", f != NULL, fileno(f) == fd);
	while (fgets(buf, sizeof buf, f))
		printf("  %s", buf);
	fclose(f);
	show("closed by fclose", close(fd));

	DIR *d = opendir(".");
	char *names[16];
	int k = 0;
	struct dirent *e;
	while ((e = readdir(d)) != NULL && k < 16) {
		char tag = e->d_type == DT_DIR ? 'd' : e->d_type == DT_REG ? 'f' : '?';
		names[k] = malloc(strlen(e->d_name) + 3);
		sprintf(names[k], "%s:%c", e->d_name, tag);
		k++;
	}
	qsort(names, k, sizeof names[0], cmpStr);
	printf("readdir");
	for (int i = 0; i < k; i++)
		printf(" %s", names[i]);
	printf("\n");
	rewinddir(d);
	int again = 0;
	while ((e = readdir(d)) != NULL)
		again += e->d_ino != 0;
	printf("rewinddir %d\n", again);
	show("closedir", closedir(d));
	d = opendir("missing");
	printf("opendir missing %d %d\n", d == NULL, errno);
	d = opendir("a.txt");
	printf("opendir file %d %d\n", d == NULL, errno);

	show("rmdir nonempty", rmdir("sub"));
	show("unlink dir", unlink("sub"));
	show("rmdir file", rmdir("a.txt"));
	show("unlink", unlink("sub/c.txt"));
	show("unlink", unlink("a.txt"));
	show("unlink", unlink("b.txt"));
	show("unlink missing", unlink("b.txt"));
	show("rmdir", rmdir("sub"));

	char tmpl[] = "tmpXXXXXX";
	fd = mkstemp(tmpl);
	printf("mkstemp %d %d %d\n", fd >= 0, strncmp(tmpl, "tmp", 3) == 0, strcmp(tmpl + 3, "XXXXXX") != 0);
	fstat(fd, &st);
	printf("mkstemp mode %o\n", st.st_mode & 0777);
	close(fd);
	char *rp = realpath(tmpl, NULL);
	printf("realpath %d %d\n", rp[0] == '/', strncmp(rp, cwd, strlen(cwd)) == 0 && strcmp(rp + strlen(cwd) + 1, tmpl) == 0);
	char rbuf[4096];
	rp = realpath("../posix_test.d/./", rbuf);
	printf("realpath .. %d %d\n", rp == rbuf, strcmp(rbuf, cwd) == 0);
	rp = realpath("missing", rbuf);
	printf("realpath missing %d %d\n", rp == NULL, errno);
	unlink(tmpl);
	char bad[] = "tmpXXXX";
	show("mkstemp bad", mkstemp(bad));

	int p[2];
	show("pipe", pipe(p));
	printf("pipe fds %d %d\n", p[0], p[1]);
	write(p[1], "through a pipe", 14);
	close(p[1]);
	r = read(p[0], buf, sizeof buf);
	buf[r] = 0;
	printf("pipe read %ld %s\n", (long)r, buf);
	show("pipe eof", read(p[0], buf, 1));
	show("lseek pipe", lseek(p[0], 0, SEEK_CUR));
	close(p[0]);

	fd = open(".", O_RDONLY);
	show("isatty file", isatty(fd));
	printf("  errno %d\n", errno);
	close(fd);
	show("chdir ..", chdir(".."));
	show("rmdir", rmdir("posix_test.d"));
}

static void testUnistd(void) {
	printf("pid %d %d %d\n", getpid() > 0, getppid() > 0, getpid() != getppid());
	printf("uid %d %d\n", getuid() == geteuid(), getgid() == getegid());
	printf("sysconf %ld %d\n", sysconf(_SC_PAGESIZE), sysconf(_SC_NPROCESSORS_ONLN) > 0);
	char host[256];
	int r = gethostname(host, sizeof host);
	printf("gethostname %d %d\n", r, strlen(host) > 0);
	printf("sleep %u usleep %d\n", sleep(0), usleep(1000));
	printf("STDIN_FILENO %d %d %d\n", STDIN_FILENO, STDOUT_FILENO, STDERR_FILENO);

	char *argv1[] = {"prog", "-a", "-b", "val", "-cfoo", "-d", "--", "-x", "rest", NULL};
	int ch;
	opterr = 0;
	while ((ch = getopt(9, argv1, "ab:c:d")) != -1)
		printf("opt %c %s\n", ch, ch == 'b' || ch == 'c' ? optarg : "");
	printf("optind %d %s\n", optind, argv1[optind]);
	char *argv2[] = {"prog", "-ab", "-z", "-c", NULL};
	optind = 0; // glibc starts over (and reads optstring's + or - again) only at 0
	while ((ch = getopt(4, argv2, ":abc:")) != -1)
		printf("opt %c optopt %c\n", ch, ch == '?' || ch == ':' ? optopt : '-');
	char *argv3[] = {"prog", "-a", "file", "-b", NULL};
	optind = 0;
	while ((ch = getopt(4, argv3, "+ab")) != -1)
		printf("opt %c\n", ch);
	printf("optind %d %s\n", optind, argv3[optind]);
	optind = 1;
	opterr = 1;
}

static void testStrings(void) {
	printf("strcasecmp %d %d %d\n", strcasecmp("Hello", "hELLO"), strcasecmp("abc", "ABD") < 0, strcasecmp("b", "A") > 0);
	printf("strncasecmp %d %d\n", strncasecmp("HelloX", "helloY", 5), strncasecmp("a", "B", 0));
	char b[8] = "abcdefg";
	bzero(b + 2, 3);
	printf("bzero %d %d %c\n", b[2], b[4], b[5]);
	char s[16] = "0123456789";
	bcopy(s, s + 2, 5);
	printf("bcopy %s\n", s);
	printf("ffs %d %d %d %d %d\n", ffs(0), ffs(1), ffs(8), ffs(-2147483647 - 1), ffsl(1L << 40));
	const char *t = "hello world";
	printf("index %s rindex %s %d\n", index(t, 'o'), rindex(t, 'o'), index(t, 'z') == NULL);
	char d[16];
	char *end = stpcpy(d, "abc");
	printf("stpcpy %s %d\n", d, (int)(end - d));
	end = stpncpy(d, "xy", 5);
	printf("stpncpy %s %d %d\n", d, (int)(end - d), d[4]);
	size_t n = strlcpy(d, "truncate me", 5);
	printf("strlcpy %d %s\n", (int)n, d);
	n = strlcat(d, "+tail", 8);
	printf("strlcat %d %s\n", (int)n, d);
	printf("strlcat full %d\n", (int)strlcat(d, "zz", 3));
}

static void testPosixStdlib(void) {
	show("setenv", setenv("CC_POSIX_TEST", "one", 1));
	printf("  %s\n", getenv("CC_POSIX_TEST"));
	show("setenv keep", setenv("CC_POSIX_TEST", "two", 0));
	printf("  %s\n", getenv("CC_POSIX_TEST"));
	show("setenv over", setenv("CC_POSIX_TEST", "three", 1));
	printf("  %s\n", getenv("CC_POSIX_TEST"));
	show("setenv bad", setenv("A=B", "x", 1));
	show("unsetenv", unsetenv("CC_POSIX_TEST"));
	printf("  %d\n", getenv("CC_POSIX_TEST") == NULL);
	void *p = aligned_alloc(64, 128);
	printf("aligned_alloc %d\n", p != NULL && (unsigned long)p % 64 == 0);
	void *q = NULL;
	int r = posix_memalign(&q, 256, 10);
	printf("posix_memalign %d %d\n", r, q != NULL && (unsigned long)q % 256 == 0);
	printf("posix_memalign bad %d\n", posix_memalign(&q, 12, 10) == EINVAL);
	free(p);
	free(q);
	struct timeval tv;
	r = gettimeofday(&tv, NULL);
	printf("gettimeofday %d %d %d\n", r, tv.tv_sec > 1000000000, tv.tv_usec >= 0 && tv.tv_usec < 1000000);
	struct timeval a = {1, 900000}, b = {2, 200000}, t;
	timeradd(&a, &b, &t);
	printf("timeradd %ld %ld\n", (long)t.tv_sec, (long)t.tv_usec);
	timersub(&b, &a, &t);
	printf("timersub %ld %ld %d\n", (long)t.tv_sec, (long)t.tv_usec, timercmp(&a, &b, <));
}

static void testLocks(void) {
	int fd = open("lock.txt", O_RDWR | O_CREAT | O_TRUNC, 0644);
	struct flock fl = {.l_type = F_WRLCK, .l_whence = SEEK_SET, .l_start = 0, .l_len = 10};
	printf("sizeof flock %zu, F_SETLK %d\n", sizeof(struct flock), fcntl(fd, F_SETLK, &fl));
	struct flock q = {.l_type = F_WRLCK, .l_whence = SEEK_SET};
	int r = fcntl(fd, F_GETLK, &q);
	printf("F_GETLK %d unlocked %d\n", r, q.l_type == F_UNLCK);
	fl.l_type = F_RDLCK;
	printf("F_SETLKW read %d\n", fcntl(fd, F_SETLKW, &fl));
	fl.l_type = F_UNLCK;
	printf("unlock %d\n", fcntl(fd, F_SETLK, &fl));
	fl.l_type = 7;
	errno = 0;
	r = fcntl(fd, F_SETLK, &fl);
	printf("bad type %d %d\n", r, errno == EINVAL);
	fl.l_type = F_RDLCK;
	r = fcntl(99, F_SETLK, &fl);
	printf("bad fd %d %d\n", r, errno == EBADF);
	int wfd = open("lock.txt", O_WRONLY);
	errno = 0;
	r = fcntl(wfd, F_SETLK, &fl);
	printf("read lock of write-only %d %d\n", r, errno == EBADF);
	close(wfd);

	long w = pwrite(fd, "hello world", 11, 5);
	printf("pwrite %ld lseek %ld\n", w, (long)lseek(fd, 0, SEEK_CUR));
	char b[32] = {0};
	long n = pread(fd, b, sizeof b - 1, 5);
	printf("pread %ld %s", n, b);
	printf(" past %ld\n", (long)pread(fd, b, 4, 100));
	errno = 0;
	n = pread(fd, b, 4, -1);
	printf("pread negative %ld %d\n", n, errno == EINVAL);
	struct stat st;
	printf("ftruncate %d", ftruncate(fd, 8));
	fstat(fd, &st);
	printf(" size %ld fsync %d", (long)st.st_size, fsync(fd));
	printf(" fdatasync %d\n", fdatasync(fd));
	int avail = -1;
	lseek(fd, 3, SEEK_SET);
	r = ioctl(fd, FIONREAD, &avail);
	printf("FIONREAD %d %d\n", r, avail);
	struct winsize ws;
	errno = 0;
	r = ioctl(fd, TIOCGWINSZ, &ws);
	printf("TIOCGWINSZ of a file %d %d\n", r, errno == ENOTTY);
	int p[2];
	pipe(p);
	write(p[1], "abcde", 5);
	r = ioctl(p[0], FIONREAD, &avail);
	printf("FIONREAD of a pipe %d %d\n", r, avail);
	close(p[0]);
	close(p[1]);
	close(fd);
	printf("truncate %d", truncate("lock.txt", 2));
	stat("lock.txt", &st);
	printf(" size %ld", (long)st.st_size);
	errno = 0;
	r = truncate("no-such-file", 0);
	printf(" missing %d %d\n", r, errno == ENOENT);
}

static void testLinks(void) {
	char b[64];
	printf("symlink %d", symlink("lock.txt", "sym.txt"));
	long n = readlink("sym.txt", b, sizeof b);
	printf(" readlink %ld %.*s", n, (int)n, b);
	n = readlink("sym.txt", b, 4);
	printf(" short %ld %.*s", n, (int)n, b);
	errno = 0;
	n = readlink("lock.txt", b, sizeof b);
	printf(" not a link %ld %d\n", n, errno == EINVAL);
	struct stat st;
	printf("link %d", link("lock.txt", "hard.txt"));
	stat("lock.txt", &st);
	printf(" nlink %ld", (long)st.st_nlink);
	errno = 0;
	int r = link("lock.txt", "hard.txt");
	printf(" again %d %d\n", r, errno == EEXIST);
	int fd = open("lock.txt", O_RDONLY);
	printf("chown %d", chown("lock.txt", -1, -1));
	printf(" lchown %d", lchown("sym.txt", -1, -1));
	printf(" fchown %d\n", fchown(fd, -1, -1));
	close(fd);
	struct utimbuf ub = {1000000000, 1234567890};
	printf("utime %d", utime("lock.txt", &ub));
	stat("lock.txt", &st);
	printf(" %ld %ld", (long)st.st_atime, (long)st.st_mtime);
	printf(" now %d", utime("lock.txt", NULL));
	stat("lock.txt", &st);
	printf(" %d\n", st.st_mtime > 1600000000);
	struct timeval tv[2] = {{1, 0}, {2000000000, 500000}};
	printf("utimes %d", utimes("lock.txt", tv));
	stat("lock.txt", &st);
	printf(" %ld %ld %ld\n", (long)st.st_atime, (long)st.st_mtime, st.st_mtim.tv_nsec);
	unlink("sym.txt");
	unlink("hard.txt");
}

static void testMmap(void) {
	unsigned char *a = mmap(NULL, 10000, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	int zero = 1;
	for (int i = 0; i < 10000; i++)
		zero &= a[i] == 0;
	a[9999] = 7;
	printf("mmap anonymous %d %d %d", a != MAP_FAILED, zero, a[9999]);
	printf(" msync %d", msync(a, 10000, MS_SYNC));
	printf(" mprotect %d", mprotect(a, 10000, PROT_READ));
	printf(" munmap %d\n", munmap(a, 10000));
	int fd = open("map.txt", O_RDWR | O_CREAT | O_TRUNC, 0644);
	write(fd, "mapped file", 11);
	char *m = mmap(NULL, 4096, PROT_READ, MAP_PRIVATE, fd, 0);
	printf("mmap file %d %.11s %d", m != MAP_FAILED, m, m[11]);
	munmap(m, 4096);
	errno = 0;
	m = mmap(NULL, 0, PROT_READ, MAP_PRIVATE, fd, 0);
	printf(" empty %d %d", m == MAP_FAILED, errno == EINVAL);
	errno = 0;
	m = mmap(NULL, 4096, PROT_READ, MAP_PRIVATE, fd, 1);
	printf(" unaligned %d %d\n", m == MAP_FAILED, errno == EINVAL);
	close(fd);
}

static void testPopen(void) {
	char line[64];
	FILE *f = popen("echo hello; echo world", "r");
	while (fgets(line, sizeof line, f))
		printf("popen read: %s", line);
	printf("pclose %d\n", pclose(f));
	f = popen("exit 3", "r");
	printf("pclose exit 3: %d\n", pclose(f) >> 8);
	f = popen("cat > popen.txt", "w");
	fputs("through cat\n", f);
	printf("pclose w %d\n", pclose(f));
	f = fopen("popen.txt", "r");
	fgets(line, sizeof line, f);
	printf("popen wrote: %s", line);
	fclose(f);
	printf("bad mode %d\n", popen("true", "x") == NULL);
}

static void testClocks(void) {
	struct timespec ts = {0, 1000000}, rem = {9, 9};
	int r = nanosleep(&ts, &rem);
	printf("nanosleep %d", r);
	ts.tv_nsec = 1000000000;
	errno = 0;
	r = nanosleep(&ts, NULL);
	printf(" bad %d %d\n", r, errno == EINVAL);
	struct timespec a, b;
	clock_gettime(CLOCK_REALTIME, &a);
	time_t t = time(NULL);
	printf("CLOCK_REALTIME %d", a.tv_sec - t <= 1 && t - a.tv_sec <= 1 && a.tv_nsec >= 0 && a.tv_nsec < 1000000000);
	clock_gettime(CLOCK_MONOTONIC, &a);
	nanosleep(&(struct timespec){0, 2000000}, NULL);
	clock_gettime(CLOCK_MONOTONIC, &b);
	printf(" CLOCK_MONOTONIC %d", (b.tv_sec - a.tv_sec) * 1000000000L + (b.tv_nsec - a.tv_nsec) >= 2000000);
	errno = 0;
	r = clock_gettime(99, &a);
	printf(" bad %d %d", r, errno == EINVAL);
	printf(" getres %d\n", clock_getres(CLOCK_MONOTONIC, &a) == 0 && a.tv_sec == 0 && a.tv_nsec > 0);
	printf("getpagesize %d\n", getpagesize() == sysconf(_SC_PAGESIZE));
	struct rusage ru;
	r = getrusage(RUSAGE_SELF, &ru);
	printf("getrusage %d %d", r, ru.ru_utime.tv_usec >= 0 && ru.ru_utime.tv_usec < 1000000 && ru.ru_stime.tv_usec < 1000000);
	errno = 0;
	r = getrusage(5, &ru);
	printf(" bad %d %d\n", r, errno == EINVAL);
}

static void testPwd(void) {
	struct passwd *pw = getpwnam("root");
	printf("getpwnam root %d", pw != NULL);
	if (pw)
		printf(" %s %u %u %s", pw->pw_name, pw->pw_uid, pw->pw_gid, pw->pw_dir);
	pw = getpwuid(0);
	printf(" getpwuid 0 %s", pw ? pw->pw_name : "(none)");
	pw = getpwuid(getuid());
	printf(" self %d", pw == NULL || pw->pw_uid == getuid());
	printf(" missing %d", getpwnam("no-such-user-here") == NULL);
	printf(" %d\n", getpwuid(4000000000u) == NULL);
	char buf[8];
	memcpy(buf, "memory", 7);
	printf("memory.h %s\n", buf);
}

static int onceN, destroyed;
static void onceF(void) { onceN++; }
static pthread_key_t key;
static void keyDtor(void *p) { destroyed += *(int *)p; }
static int keyVal = 5;
static void *keyThread(void *arg) {
	int ok = pthread_getspecific(key) == NULL;
	pthread_setspecific(key, &keyVal);
	ok &= pthread_getspecific(key) == &keyVal;
	return ok ? arg : NULL;
}
static pthread_mutex_t rmu;
static int shared;
static void *rThread(void *arg) {
	for (int i = 0; i < 1000; i++) {
		pthread_mutex_lock(&rmu);
		pthread_mutex_lock(&rmu);
		shared++;
		pthread_mutex_unlock(&rmu);
		pthread_mutex_unlock(&rmu);
	}
	return NULL;
}

static void testPthread(void) {
	pthread_once_t once = PTHREAD_ONCE_INIT;
	pthread_once(&once, onceF);
	pthread_once(&once, onceF);
	printf("pthread_once %d\n", onceN);
	pthread_mutexattr_t attr;
	int type = -1;
	pthread_mutexattr_init(&attr);
	pthread_mutexattr_gettype(&attr, &type);
	printf("default type %d", type == PTHREAD_MUTEX_DEFAULT);
	printf(" bad %d", pthread_mutexattr_settype(&attr, 99) == EINVAL);
	printf(" settype %d", pthread_mutexattr_settype(&attr, PTHREAD_MUTEX_RECURSIVE));
	pthread_mutexattr_gettype(&attr, &type);
	printf(" recursive %d\n", type == PTHREAD_MUTEX_RECURSIVE);
	pthread_mutex_init(&rmu, &attr);
	pthread_mutexattr_destroy(&attr);
	printf("recursive lock %d", pthread_mutex_lock(&rmu));
	printf(" %d", pthread_mutex_lock(&rmu));
	printf(" trylock %d", pthread_mutex_trylock(&rmu));
	for (int i = 0; i < 3; i++)
		printf(" unlock %d", pthread_mutex_unlock(&rmu));
	printf(" trylock %d", pthread_mutex_trylock(&rmu));
	printf(" unlock %d\n", pthread_mutex_unlock(&rmu));
	pthread_t t1, t2;
	pthread_create(&t1, NULL, rThread, NULL);
	pthread_create(&t2, NULL, rThread, NULL);
	pthread_join(t1, NULL);
	pthread_join(t2, NULL);
	printf("recursive shared %d\n", shared);
	pthread_mutex_t emu;
	pthread_mutexattr_init(&attr);
	pthread_mutexattr_settype(&attr, PTHREAD_MUTEX_ERRORCHECK);
	pthread_mutex_init(&emu, &attr);
	printf("errorcheck %d", pthread_mutex_lock(&emu));
	printf(" relock %d", pthread_mutex_lock(&emu) == EDEADLK);
	printf(" trylock %d", pthread_mutex_trylock(&emu) == EBUSY);
	printf(" unlock %d", pthread_mutex_unlock(&emu));
	printf(" again %d\n", pthread_mutex_unlock(&emu) == EPERM);
	pthread_mutex_destroy(&emu);
	pthread_mutex_destroy(&rmu);
	printf("key_create %d", pthread_key_create(&key, keyDtor));
	void *ret;
	pthread_t t;
	pthread_create(&t, NULL, keyThread, &keyVal);
	pthread_join(t, &ret);
	printf(" thread %d destructor %d main %d", ret == &keyVal, destroyed, pthread_getspecific(key) == NULL);
	printf(" set %d", pthread_setspecific(key, &onceN));
	printf(" get %d", pthread_getspecific(key) == &onceN);
	printf(" delete %d\n", pthread_key_delete(key));
}

// environ follows setenv, putenv, unsetenv and clearenv.
extern char **environ;

static int countEnv(const char *prefix) {
	int n = 0;
	for (char **e = environ; *e; e++)
		if (strncmp(*e, prefix, strlen(prefix)) == 0)
			n++;
	return n;
}

static void testEnviron(void) {
	setenv("CCENV_A", "1", 1);
	printf("environ after setenv %d\n", countEnv("CCENV_A=1"));
	putenv("CCENV_B=two");
	printf("environ after putenv %d %s\n", countEnv("CCENV_"), getenv("CCENV_B"));
	unsetenv("CCENV_A");
	printf("environ after unsetenv %d\n", countEnv("CCENV_"));
	unsetenv("CCENV_B");
}

int main(void) {
#ifndef __GD_CC__
	setlocale(LC_ALL, "C.UTF-8");
#endif
	testMath();
	testCtype();
	testInttypes();
	testErrno();
	testUchar();
	testTime();
	testTm();
	testSignal();
	testDiv();
	testScanf();
	testStdlib();
	testString();
	testStdio();
	testLocale();
	testFd();
	testUnistd();
	testStrings();
	testPosixStdlib();
	testLocks();
	testLinks();
	testMmap();
	testPopen();
	testClocks();
	testPwd();
	testPthread();
	testEnviron();
	return 0;
}
