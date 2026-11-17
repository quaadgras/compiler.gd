// C's library: the output must match gcc's (with glibc or musl).
#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <locale.h>
#include <math.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <uchar.h>
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
	return 0;
}
