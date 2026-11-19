// regex.h: POSIX regular expressions, basic and extended, with the
// leftmost-longest match and its subexpressions, compared with the C
// library's. Only what glibc and musl agree on: not ERE back-references,
// [=a=] and [.a.] (musl has none), the subexpressions of a repetition's
// earlier iterations, an unclosed \{ (glibc's REG_EBRACE, musl's
// REG_BADBR) or {,n}. Then termios.h, as far as it goes without a
// terminal.

#include <errno.h>
#include <regex.h>
#include <stdio.h>
#include <string.h>
#include <sys/ttydefaults.h>
#include <termios.h>
#include <unistd.h>

#define E REG_EXTENDED
#define I REG_ICASE
#define N REG_NEWLINE

struct test {
	const char *pat;
	int cflags;
	const char *str;
	int eflags;
};

static const struct test tests[] = {
	// BRE.
	{"abc", 0, "xabcy", 0},
	{"a*", 0, "baaa", 0},
	{"a*", 0, "aaab", 0},
	{"a.c", 0, "abc a\nc", 0},
	{"a\\.c", 0, "abc a.c", 0},
	{"*a", 0, "x*a", 0},
	{"\\(*a\\)", 0, "b*a", 0},
	{"^*", 0, "*x", 0},
	{"a^b", 0, "a^b", 0},
	{"a$b", 0, "a$b", 0},
	{"^abc$", 0, "abc", 0},
	{"^abc$", 0, "abcd", 0},
	{"\\(ab\\)*c", 0, "ababc", 0},
	{"\\(a\\|b\\)*c", 0, "abbac", 0},
	{"a\\{2,3\\}", 0, "aaaa", 0},
	{"a\\{2\\}", 0, "aaaa", 0},
	{"a\\{2,\\}", 0, "aaaaa", 0},
	{"\\(ab*\\)\\{2\\}", 0, "abbab", 0},
	{"x\\{0\\}y", 0, "xy", 0},
	{"\\(a\\)\\1", 0, "aa", 0},
	{"\\(.\\)\\1", 0, "abccd", 0},
	{"\\(a*\\)b\\1", 0, "aabaa", 0},
	{"\\(a*\\)\\1", 0, "aaaa", 0},
	{"\\([a-c]*\\)x\\1", 0, "abcxabc", 0},
	{"\\(a\\)\\(b\\)\\(c\\)\\(d\\)\\(e\\)\\(f\\)\\(g\\)\\(h\\)\\(i\\)\\9\\1", 0, "abcdefghiia", 0},
	{"\\(\\(a\\)b\\)", 0, "ab", 0},
	// ERE.
	{"a|ab", E, "abc", 0},
	{"ab|a", E, "abc", 0},
	{"(a*)(b|abc)", E, "abc", 0},
	{"(ab|a)(bc|c)", E, "abc", 0},
	{"(wee|week)(knights|night)", E, "weeknights", 0},
	{"x(a|ab)(bc|c)?", E, "xabc", 0},
	{"(a|b)*", E, "abab", 0},
	{"(a+|b+)*c", E, "aabbbac", 0},
	{"(a*)+", E, "aa", 0},
	{"(a*)+", E, "b", 0},
	{"(a)|b", E, "b", 0},
	{"(a)|(b)", E, "b", 0},
	{"a+", E, "baaa", 0},
	{"a?b", E, "ab b", 0},
	{"a{2,3}", E, "aaaa", 0},
	{"(ab){2}", E, "abababab", 0},
	{"(a|b){3,}", E, "ababc", 0},
	{"^(a|b)$", E, "b", 0},
	{"a)", E, "xa)", 0},
	{"a^b", E, "a^b", 0},
	{"(^a)", E, "ab", 0},
	{"x|^a", E, "ba", 0},
	{"(foo|foobar)(bar)?", E, "foobar", 0},
	{"(a.*)(b.*)", E, "abab", 0},
	{"(a|ab)*c", E, "ababc", 0},
	{"([[:alpha:]]+)([[:digit:]]+)", E, "abc123", 0},
	{".*", E, "", 0},
	{"()", E, "x", 0},
	// Bracket expressions.
	{"[abc]+", E, "xbcay", 0},
	{"[^abc]+", E, "abxyzc", 0},
	{"[]a]+", E, "x]a]y", 0},
	{"[^]a]+", E, "]axy]", 0},
	{"[a-]+", E, "x-a-y", 0},
	{"[-a]+", E, "x-a-y", 0},
	{"[a-c-]+", E, "xb-cy", 0},
	{"[[:alpha:]]+", E, "12abC3", 0},
	{"[[:digit:]]+", E, "ab123c", 0},
	{"[[:alnum:]]+", E, "_ab12_", 0},
	{"[[:upper:]]+", E, "abCDe", 0},
	{"[[:lower:]]+", E, "ABcdE", 0},
	{"[[:space:]]+", E, "a \t\nb", 0},
	{"[[:blank:]]+", E, "a \t\nb", 0},
	{"[[:punct:]]+", E, "ab!?.c", 0},
	{"[[:xdigit:]]+", E, "xyzAf09g", 0},
	{"[[:cntrl:]]+", E, "a\x01\x02b", 0},
	{"[[:print:]]+", E, "\x01ab c\x02", 0},
	{"[[:graph:]]+", E, " ab c", 0},
	{"[^[:alpha:]]", 0, "ab1", 0},
	{"[[:alpha:][:digit:]]+", E, "-a1b2-", 0},
	{"[\\]+", E, "a\\\\b", 0},
	{"[.]", E, "ab.c", 0},
	{"[*]", 0, "ab*c", 0},
	// REG_ICASE.
	{"ABC", I, "xabc", 0},
	{"abc", I, "xABC", 0},
	{"[a-c]+", E | I, "xABCd", 0},
	{"[^a]+", E | I, "aAbB", 0},
	{"[[:upper:]]+", E | I, "abc", 0},
	// REG_NEWLINE.
	{"^b", N, "a\nb", 0},
	{"^b", 0, "a\nb", 0},
	{"a$", N, "a\nb", 0},
	{"a$", 0, "a\nb", 0},
	{"a.b", N, "a\nb", 0},
	{"a.b", 0, "a\nb", 0},
	{"a[^x]b", N, "a\nb", 0},
	{"a[^x]b", 0, "a\nb", 0},
	{"^b", N, "a\nb", REG_NOTBOL},
	// REG_NOTBOL, REG_NOTEOL.
	{"^a", 0, "a", REG_NOTBOL},
	{"a$", 0, "a", REG_NOTEOL},
	{"^$", 0, "", REG_NOTBOL},
	{"a", 0, "a", REG_NOTBOL | REG_NOTEOL},
	// No match.
	{"abc", 0, "ab", 0},
	{"a{3}", E, "aa", 0},
	{"(a*)*b", E, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 0},
	// Errors.
	{"a\\", 0, "", 0},
	{"\\(a", 0, "", 0},
	{"a\\)", 0, "", 0},
	{"(a", E, "", 0},
	{"[a", 0, "", 0},
	{"[[:alpha:]", 0, "", 0},
	{"[z-a]", 0, "", 0},
	{"[[:foo:]]", 0, "", 0},
	{"\\1", 0, "", 0},
	{"\\(a\\)\\2", 0, "", 0},
	{"a\\{2,1\\}", 0, "", 0},
	{"a{2,1}", E, "", 0},
	{"*a", E, "", 0},
	{"a|*b", E, "", 0},
	{"(+a)", E, "", 0},
	{"a**", E, "aaa", 0},
};

static void run(const struct test *t) {
	regex_t re;
	regmatch_t m[12];
	int rc = regcomp(&re, t->pat, t->cflags);
	printf("%-28s %2d ", t->pat, t->cflags);
	if (rc != 0) {
		printf("regcomp %d\n", rc);
		return;
	}
	for (int i = 0; i < 12; i++)
		m[i].rm_so = m[i].rm_eo = 7777;
	rc = regexec(&re, t->str, 12, m, t->eflags);
	if (rc != 0) {
		printf("nsub %zu regexec %d\n", re.re_nsub, rc);
	} else {
		printf("nsub %zu:", re.re_nsub);
		for (size_t i = 0; i <= re.re_nsub && i < 12; i++)
			printf(" (%d,%d)", (int)m[i].rm_so, (int)m[i].rm_eo);
		// Past re_nsub, -1.
		if (re.re_nsub + 1 < 12)
			printf(" [%d,%d]", (int)m[re.re_nsub + 1].rm_so, (int)m[re.re_nsub + 1].rm_eo);
		printf("\n");
	}
	regfree(&re);
}

static void misc(void) {
	regex_t re;
	regmatch_t m[3];
	char buf[64];

	// REG_NOSUB: regexec's answer only, pmatch untouched.
	regcomp(&re, "(a)(b)", E | REG_NOSUB);
	m[0].rm_so = 99;
	int r1 = regexec(&re, "xab", 3, m, 0), r2 = regexec(&re, "xa", 3, m, 0);
	printf("nosub: %d %d %d\n", r1, r2, (int)m[0].rm_so);
	regfree(&re);

	// nmatch 0 and pmatch NULL.
	regcomp(&re, "b+", E);
	printf("nmatch 0: %d\n", regexec(&re, "abbc", 0, NULL, 0));
	// Repeated searches along a string.
	const char *s = "one two  three";
	regcomp(&re, "[a-z]+", E);
	for (const char *p = s; regexec(&re, p, 1, m, p == s ? 0 : REG_NOTBOL) == 0; p += m[0].rm_eo)
		printf("word %.*s at %d\n", (int)(m[0].rm_eo - m[0].rm_so), p + m[0].rm_so, (int)(p - s + m[0].rm_so));
	regfree(&re);

	// regerror.
	size_t n = regerror(REG_NOMATCH, NULL, buf, sizeof buf);
	printf("regerror: %zu %s\n", n, buf);
	n = regerror(REG_EESCAPE, NULL, buf, sizeof buf);
	printf("regerror: %zu %s\n", n, buf);
	n = regerror(REG_ESUBREG, NULL, buf, sizeof buf);
	printf("regerror: %zu %s\n", n, buf);
	n = regerror(REG_NOMATCH, NULL, buf, 4);
	printf("regerror short: %zu \"%s\"\n", n, buf);
	printf("regerror 0: %zu\n", regerror(REG_NOMATCH, NULL, NULL, 0));

	printf("sizes: %zu %zu\n", sizeof(regmatch_t) >= 8, sizeof(regex_t) >= sizeof(size_t) + sizeof(void *));
}

static void term(void) {
	struct termios t;
	int p[2], r;

	// Each call before its errno is read: printf's arguments are
	// evaluated in no order.
	memset(&t, 0, sizeof t);
	pipe(p);
	r = tcgetattr(p[0], &t);
	printf("tcgetattr pipe: %d %d\n", r, errno == ENOTTY);
	r = tcsetattr(p[0], TCSANOW, &t);
	printf("tcsetattr pipe: %d %d\n", r, errno == ENOTTY);
	r = tcgetattr(99, &t);
	printf("tcgetattr bad: %d %d\n", r, errno == EBADF);
	r = tcflush(p[0], TCIFLUSH);
	printf("tcflush pipe: %d %d\n", r, errno == ENOTTY);
	r = tcdrain(p[1]);
	printf("tcdrain pipe: %d %d\n", r, errno == ENOTTY);
	r = tcflow(p[1], TCOON);
	printf("tcflow pipe: %d %d\n", r, errno == ENOTTY);
	close(p[0]);
	close(p[1]);

	memset(&t, 0, sizeof t);
	t.c_iflag = TTYDEF_IFLAG;
	t.c_oflag = TTYDEF_OFLAG;
	t.c_lflag = TTYDEF_LFLAG;
	t.c_cflag = TTYDEF_CFLAG;
	t.c_cc[VMIN] = 5;
	t.c_cc[VTIME] = 3;
	t.c_cc[VINTR] = CINTR;
	r = cfsetospeed(&t, TTYDEF_SPEED);
	printf("speed: %d %o\n", r, (unsigned)cfgetospeed(&t));
	cfmakeraw(&t);
	printf("raw: %o %o %o %o %d %d %d\n", t.c_iflag, t.c_oflag, t.c_cflag, t.c_lflag, t.c_cc[VMIN], t.c_cc[VTIME], t.c_cc[VINTR]);
	r = cfsetospeed(&t, B115200);
	printf("cfsetospeed: %d %o\n", r, (unsigned)cfgetospeed(&t));
	r = cfsetspeed(&t, B38400);
	printf("cfsetspeed: %d %o %o\n", r, (unsigned)cfgetospeed(&t), t.c_cflag & ~CBAUD);
	r = cfsetospeed(&t, 9600);
	printf("cfsetospeed bad: %d %d %o\n", r, errno == EINVAL, (unsigned)cfgetospeed(&t));
	printf("consts: %d %d %d %d %d %d %o %o %o %d\n", NCCS, VMIN, VTIME, VEOF, TCSAFLUSH, TCIOFLUSH, B4000000, CRTSCTS, IUTF8, CTRL('c'));
}

int main(void) {
	for (size_t i = 0; i < sizeof tests / sizeof tests[0]; i++)
		run(&tests[i]);
	misc();
	term();
	return 0;
}
