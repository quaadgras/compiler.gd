// The common extensions of glibc and musl: the output must match gcc's
// (with glibc or musl). It prints only what both agree on.
#define _GNU_SOURCE
#include <byteswap.h>
#include <endian.h>
#include <err.h>
#include <errno.h>
#include <fcntl.h>
#include <fnmatch.h>
#include <getopt.h>
#include <glob.h>
#include <grp.h>
#include <iconv.h>
#include <langinfo.h>
#include <libgen.h>
#include <malloc.h>
#include <search.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/param.h>
#include <sys/random.h>
#include <sys/stat.h>
#include <sys/uio.h>
#include <sys/utsname.h>
#include <syslog.h>
#include <unistd.h>

static void testGetoptLong(void) {
	static int flag;
	static struct option opts[] = {
		{"verbose", no_argument, &flag, 7},
		{"name", required_argument, 0, 'n'},
		{"level", optional_argument, 0, 'l'},
		{"value", required_argument, 0, 'V'},
		{"version", no_argument, 0, 'v'},
		{0, 0, 0, 0},
	};
	char *argv[] = {"prog", "--verbose", "file1", "--name=bob", "-a", "--name", "alice", "--level", "--level=3",
		"--verb", "--vers", "-n", "x", "file2", "--", "--name", 0};
	int argc = sizeof argv / sizeof *argv - 1;
	int c, idx;
	optind = 1;
	opterr = 0;
	while ((c = getopt_long(argc, argv, "an:v", opts, &idx)) != -1) {
		switch (c) {
		case 0:
			printf("long %s flag %d\n", opts[idx].name, flag);
			break;
		case 'a':
			printf("short a\n");
			break;
		case 'n':
			printf("name %s\n", optarg);
			break;
		case 'l':
			printf("level %s\n", optarg ? optarg : "(none)");
			break;
		case 'v':
			printf("version idx %d\n", idx);
			break;
		default:
			printf("? %d\n", c);
		}
	}
	printf("rest:");
	for (int i = optind; i < argc; i++)
		printf(" %s", argv[i]);
	printf("\n");

	char *argv2[] = {"prog", "-name", "carol", "-a", "-level=2", "-v", "--nam=dave", "-x", 0};
	argc = sizeof argv2 / sizeof *argv2 - 1;
	optind = 1;
	while ((c = getopt_long_only(argc, argv2, "av", opts, &idx)) != -1) {
		if (c == 'n' || c == 'l')
			printf("only %c %s\n", c, optarg);
		else
			printf("only %c\n", c);
	}
	optind = 1;
}

static void testErr(void) {
	FILE *f = fopen("err.txt", "w+");
	int saved = dup(2);
	fflush(stderr);
	dup2(fileno(f), 2);
	errno = ENOENT;
	warn("warn %d", 1);
	warnx("warnx %s", "two");
	errno = EACCES;
	warn(NULL);
	openlog("util", LOG_PERROR, LOG_USER);
	syslog(LOG_INFO, "syslog %d", 3);
	setlogmask(LOG_UPTO(LOG_WARNING));
	syslog(LOG_INFO, "masked");
	syslog(LOG_ERR, "error %s", "four\n");
	closelog();
	fflush(stderr);
	dup2(saved, 2);
	close(saved);
	rewind(f);
	char line[256];
	while (fgets(line, sizeof line, f)) {
		// The program's name differs: print what follows it.
		char *p = strstr(line, ": ");
		printf("stderr:%s", p ? p + 1 : line);
	}
	fclose(f);
	remove("err.txt");
}

static void testLibgen(void) {
	const char *paths[] = {"/usr/lib", "/usr/lib/", "usr", "/", "a//b//", "", ".", "..", "/a", "///x///"};
	for (int i = 0; i < sizeof paths / sizeof *paths; i++) {
		char a[32], b[32];
		strcpy(a, paths[i]);
		strcpy(b, paths[i]);
		printf("'%s': basename '%s' dirname '%s'\n", paths[i], basename(a), dirname(b));
	}
	printf("null: '%s' '%s'\n", basename(NULL), dirname(NULL));
}

static void testBytes(void) {
	printf("bswap %04x %08x %016llx\n", bswap_16(0x1234), bswap_32(0x12345678u), (unsigned long long)bswap_64(0x0102030405060708ull));
	printf("be %04x %08x %016llx\n", htobe16(0x1234), htobe32(0x12345678u), (unsigned long long)htobe64(0x0102030405060708ull));
	printf("le %04x %08x %016llx\n", htole16(0x1234), le32toh(0x12345678u), (unsigned long long)le64toh(0x0102030405060708ull));
	printf("toh %04x %08x %016llx\n", be16toh(0x3412), be32toh(0x78563412u), (unsigned long long)be64toh(0x0807060504030201ull));
	printf("order %d\n", BYTE_ORDER == LITTLE_ENDIAN);
	printf("param MIN %d MAX %d howmany %d roundup %d powerof2 %d %d NBBY %d MAXPATHLEN %d\n",
		MIN(3, 4), MAX(3, 4), howmany(10, 4), roundup(10, 4), powerof2(64) != 0, powerof2(48) != 0, NBBY, MAXPATHLEN);
}

static void testFnmatch(void) {
	struct {
		const char *p, *s;
		int flags;
	} t[] = {
		{"*.c", "util.c", 0},
		{"*.c", "util.h", 0},
		{"u?il.*", "util.c", 0},
		{"[a-c]x", "bx", 0},
		{"[!a-c]x", "bx", 0},
		{"[^a-c]x", "dx", 0},
		{"[]]", "]", 0},
		{"[[:digit:]]*", "9lives", 0},
		{"[[:alpha:][:digit:]]", "_", 0},
		{"\\*", "*", 0},
		{"\\*", "\\*", FNM_NOESCAPE},
		{"*", "a/b", 0},
		{"*", "a/b", FNM_PATHNAME},
		{"a/*", "a/b", FNM_PATHNAME},
		{"a?b", "a/b", FNM_PATHNAME},
		{"*", ".hidden", 0},
		{"*", ".hidden", FNM_PERIOD},
		{".*", ".hidden", FNM_PERIOD},
		{"a/*", "a/.b", FNM_PATHNAME | FNM_PERIOD},
		{"a/*", "a/.b", FNM_PERIOD},
		{"*A*", "xay", FNM_CASEFOLD},
		{"[A-C]", "b", FNM_CASEFOLD},
		{"*a*a*a*b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 0},
		{"*b*", "abc", 0},
		{"", "", 0},
		{"a*", "", 0},
	};
	for (int i = 0; i < sizeof t / sizeof *t; i++)
		printf("fnmatch(%s, %.20s, %d) = %d\n", t[i].p, t[i].s, t[i].flags, fnmatch(t[i].p, t[i].s, t[i].flags));
}

static int globErrs;
static int globErr(const char *path, int e) {
	globErrs++;
	return 0;
}

static void printGlob(const char *what, int r, glob_t *g) {
	printf("%s: %d %zu:", what, r, r == 0 ? g->gl_pathc : 0);
	if (r == 0)
		for (size_t i = 0; i < g->gl_pathc; i++)
			printf(" %s", g->gl_pathv[g->gl_offs + i]);
	printf("\n");
}

static void testGlob(void) {
	mkdir("gdir", 0755);
	mkdir("gdir/sub", 0755);
	const char *files[] = {"gdir/b.txt", "gdir/a.txt", "gdir/c.dat", "gdir/.hid.txt", "gdir/sub/d.txt"};
	for (int i = 0; i < 5; i++)
		fclose(fopen(files[i], "w"));
	glob_t g;
	int r = glob("gdir/*.txt", 0, globErr, &g);
	printGlob("txt", r, &g);
	globfree(&g);
	r = glob("gdir/*", GLOB_MARK, NULL, &g);
	printGlob("mark", r, &g);
	globfree(&g);
	r = glob("gdir/*/*.txt", 0, NULL, &g);
	printGlob("sub", r, &g);
	r = glob("gdir/?.dat", GLOB_APPEND, NULL, &g);
	printGlob("append", r, &g);
	globfree(&g);
	r = glob("gdir/*.none", 0, NULL, &g);
	printf("nomatch %d\n", r == GLOB_NOMATCH);
	r = glob("gdir/*.none", GLOB_NOCHECK, NULL, &g);
	printGlob("nocheck", r, &g);
	globfree(&g);
	g.gl_offs = 2;
	r = glob("gdir/[ab].txt", GLOB_DOOFFS, NULL, &g);
	printGlob("dooffs", r, &g);
	printf("offs null %d %d\n", g.gl_pathv[0] == NULL, g.gl_pathv[1] == NULL);
	globfree(&g);
	r = glob("gdir/sub", 0, NULL, &g);
	printGlob("literal", r, &g);
	globfree(&g);
	printf("glob errors %d\n", globErrs);
	for (int i = 0; i < 5; i++)
		remove(files[i]);
	rmdir("gdir/sub");
	rmdir("gdir");
}

static int cmpInt(const void *a, const void *b) {
	int x = *(const int *)a, y = *(const int *)b;
	return (x > y) - (x < y);
}

static int cmpStr(const void *a, const void *b) { return strcmp(a, b); }

static void walk(const void *node, VISIT which, int depth) {
	if (which == postorder || which == leaf)
		printf(" %d", **(int *const *)node);
}

static int freed;
static void freeNode(void *p) { freed++; }

static void testSearch(void) {
	hcreate(16);
	char *keys[] = {"one", "two", "three", "four", "five", "six", "seven", "eight"};
	for (long i = 0; i < 8; i++) {
		ENTRY e = {keys[i], (void *)(i + 1)};
		ENTRY *r = hsearch(e, ENTER);
		if (!r)
			printf("hsearch ENTER %s failed\n", keys[i]);
	}
	ENTRY q = {"three", 0};
	ENTRY *r = hsearch(q, FIND);
	printf("hsearch three %ld\n", r ? (long)r->data : -1L);
	q.key = "nine";
	printf("hsearch nine %d\n", hsearch(q, FIND) == NULL);
	q.key = "one";
	q.data = (void *)99;
	r = hsearch(q, ENTER);
	printf("hsearch one again %ld\n", (long)r->data);
	hdestroy();

	void *root = NULL;
	int vals[] = {50, 20, 80, 10, 30, 70, 90, 60, 40, 20};
	for (int i = 0; i < 10; i++) {
		int **n = tsearch(&vals[i], &root, cmpInt);
		if (**n != vals[i])
			printf("tsearch %d wrong\n", vals[i]);
		if (i == 9)
			printf("duplicate is first %d\n", *n == &vals[1]);
	}
	printf("twalk:");
	twalk(root, walk);
	printf("\n");
	int k = 30;
	int **f = tfind(&k, &root, cmpInt);
	printf("tfind 30 %d\n", f ? **f : -1);
	k = 35;
	printf("tfind 35 %d\n", tfind(&k, &root, cmpInt) != NULL);
	k = 50;
	printf("tdelete 50 %d\n", tdelete(&k, &root, cmpInt) != NULL);
	k = 20;
	printf("tdelete 20 %d\n", tdelete(&k, &root, cmpInt) != NULL);
	k = 20;
	printf("tdelete 20 again %d\n", tdelete(&k, &root, cmpInt) != NULL);
	printf("twalk:");
	twalk(root, walk);
	printf("\n");
	tdestroy(root, freeNode);
	printf("tdestroy freed %d\n", freed);

	int arr[10] = {5, 3, 8};
	size_t n = 3;
	k = 8;
	int *p = lfind(&k, arr, &n, sizeof(int), cmpInt);
	printf("lfind 8 at %td\n", p ? p - arr : -1);
	k = 4;
	printf("lfind 4 %d\n", lfind(&k, arr, &n, sizeof(int), cmpInt) != NULL);
	p = lsearch(&k, arr, &n, sizeof(int), cmpInt);
	printf("lsearch 4 at %td n %zu\n", p - arr, n);
	p = lsearch(&k, arr, &n, sizeof(int), cmpInt);
	printf("lsearch 4 again at %td n %zu\n", p - arr, n);
	char words[4][8] = {"pear", "fig"};
	size_t nw = 2;
	char kiwi[8] = "kiwi";
	char *w = lsearch(kiwi, words, &nw, 8, cmpStr);
	printf("lsearch kiwi %s n %zu\n", w, nw);

	struct qelem a = {0}, b = {0}, c = {0};
	a.q_data[0] = 'a';
	b.q_data[0] = 'b';
	c.q_data[0] = 'c';
	insque(&a, NULL);
	insque(&c, &a);
	insque(&b, &a);
	printf("insque:");
	for (struct qelem *e = &a; e; e = e->q_forw)
		printf(" %c", e->q_data[0]);
	printf(" back %c\n", c.q_back->q_data[0]);
	remque(&b);
	printf("remque:");
	for (struct qelem *e = &a; e; e = e->q_forw)
		printf(" %c", e->q_data[0]);
	printf(" back %c\n", c.q_back->q_data[0]);
}

static void testMalloc(void) {
	void *p = malloc(100);
	printf("usable %d\n", malloc_usable_size(p) >= 100);
	free(p);
	p = memalign(64, 100);
	printf("memalign %d\n", ((uintptr_t)p & 63) == 0);
	free(p);
	unsigned char buf[64] = {0};
	printf("getrandom %zd\n", getrandom(buf, sizeof buf, 0));
	int nonzero = 0;
	for (int i = 0; i < 64; i++)
		nonzero += buf[i] != 0;
	printf("random bytes %d\n", nonzero > 32);
	printf("getentropy %d", getentropy(buf, 32));
	int r = getentropy(buf, 300);
	printf(" %d\n", r == -1 && errno == EIO);
}

static void testUio(void) {
	int fd = open("uio.txt", O_RDWR | O_CREAT | O_TRUNC, 0644);
	char a[] = "hello, ", b[] = "vectored ", c[] = "world\n";
	struct iovec iov[3] = {{a, strlen(a)}, {b, strlen(b)}, {c, strlen(c)}};
	printf("writev %zd\n", writev(fd, iov, 3));
	lseek(fd, 0, SEEK_SET);
	char x[5], y[30];
	struct iovec riov[2] = {{x, sizeof x}, {y, sizeof y}};
	ssize_t n = readv(fd, riov, 2);
	printf("readv %zd '%.5s' '%.*s'\n", n, x, (int)(n - 5), y);
	printf("readv eof %zd\n", readv(fd, riov, 2));
	printf("flock %d", flock(fd, LOCK_EX));
	printf(" %d", flock(fd, LOCK_SH | LOCK_NB));
	printf(" %d\n", flock(fd, LOCK_UN));
	close(fd);
	remove("uio.txt");
}

static void testUname(void) {
	struct utsname u;
	printf("uname %d\n", uname(&u));
	printf("sysname %s machine %s\n", u.sysname, u.machine);
	char host[256];
	gethostname(host, sizeof host);
	printf("nodename %d\n", strcmp(host, u.nodename) == 0);
	struct group *g = getgrgid(0);
	printf("getgrgid(0) %s %u\n", g ? g->gr_name : "(null)", g ? (unsigned)g->gr_gid : 0);
	g = getgrnam("root");
	printf("getgrnam(root) %u\n", g ? (unsigned)g->gr_gid : 99);
	printf("getgrnam(nosuch) %d\n", getgrnam("no-such-group-xyz") == NULL);
}

static void testLanginfo(void) {
	int items[] = {DAY_1, DAY_7, ABDAY_2, MON_1, MON_12, ABMON_5, D_FMT, T_FMT, T_FMT_AMPM, AM_STR, PM_STR, RADIXCHAR, YESEXPR, NOEXPR};
	for (int i = 0; i < sizeof items / sizeof *items; i++)
		printf("langinfo %#x '%s'\n", items[i], nl_langinfo(items[i]));
}

static void dump(const char *what, const char *b, size_t n) {
	printf("%s:", what);
	for (size_t i = 0; i < n; i++)
		printf(" %02x", (unsigned char)b[i]);
	printf("\n");
}

static void convert(const char *to, const char *from, const char *in, size_t n, size_t outsize) {
	iconv_t cd = iconv_open(to, from);
	if (cd == (iconv_t)-1) {
		printf("iconv_open %s %s failed %d\n", to, from, errno == EINVAL);
		return;
	}
	char out[64];
	char *ip = (char *)in, *op = out;
	size_t il = n, ol = outsize;
	errno = 0;
	size_t r = iconv(cd, &ip, &il, &op, &ol);
	char what[64];
	snprintf(what, sizeof what, "%s<-%s r=%d errno=%s in=%zu left=%zu", to, from, (int)r,
		errno == EILSEQ ? "EILSEQ" : errno == EINVAL ? "EINVAL" : errno == E2BIG ? "E2BIG" : errno ? "other" : "0",
		(size_t)(ip - in), il);
	dump(what, out, op - out);
	iconv_close(cd);
}

static void testIconv(void) {
	const char *s = "h\xc3\xa9\xe2\x82\xac\xf0\x9f\x98\x80"; // hé€😀
	size_t n = strlen(s);
	convert("UTF-16LE", "UTF-8", s, n, 64);
	convert("UTF-16BE", "UTF-8", s, n, 64);
	convert("UTF-32LE", "UTF-8", s, n, 64);
	convert("UCS-4", "UTF-8", s, n, 64);
	convert("WCHAR_T", "UTF-8", s, 3, 64);
	convert("ISO-8859-1", "UTF-8", s, 3, 64);
	convert("latin1", "utf8", s, 3, 64);
	convert("UTF-8", "ISO-8859-1", "caf\xe9", 4, 64);
	convert("UTF-8", "UTF-16LE", "h\0\xe9\0\x3d\xd8\x00\xde", 8, 64);
	convert("UTF-8", "UTF-16", "\xff\xfeh\0i\0", 6, 64);
	convert("UTF-8", "UTF-16", "\xfe\xff\0h\0i", 6, 64);
	convert("UTF-8", "UTF-16BE", "\0h\xd8\x3d", 4, 64);
	convert("UTF-8", "UTF-16LE", "h\0\x00\xdc", 4, 64);
	convert("UTF-8", "UTF-32BE", "\0\0\0h\0\x11\0\0", 8, 64);
	convert("UTF-8", "UTF-8", "ab\xc3", 3, 64);
	convert("UTF-8", "UTF-8", "ab\xff", 3, 64);
	convert("UTF-8", "UTF-8", "ab\xc0\xaf", 4, 64);
	convert("UTF-8", "ASCII", "ab\x80", 3, 64);
	convert("UTF-16LE", "UTF-8", s, n, 5);
	convert("UTF-8", "UTF-8", s, n, 4);
	convert("NO-SUCH-CODESET", "UTF-8", s, n, 64);
	iconv_t cd = iconv_open("UTF-8", "UTF-8");
	printf("iconv reset %d\n", (int)iconv(cd, NULL, NULL, NULL, NULL));
	iconv_close(cd);
}

static int cmpR(const void *a, const void *b, void *arg) {
	int x = *(const int *)a, y = *(const int *)b;
	return *(int *)arg * ((x > y) - (x < y));
}

static void testStdio(void) {
	FILE *f = fopen("lines.txt", "w+");
	fputs("first line\nsecond\n\nlast without newline", f);
	rewind(f);
	char *line = NULL;
	size_t cap = 0;
	ssize_t n;
	while ((n = getline(&line, &cap, f)) != -1)
		printf("getline %zd '%s' cap ok %d\n", n, line, cap > (size_t)n);
	printf("getline eof %d\n", feof(f) != 0);
	rewind(f);
	while ((n = getdelim(&line, &cap, ' ', f)) != -1)
		printf("getdelim %zd '%s'\n", n, line);
	free(line);
	fclose(f);
	remove("lines.txt");

	char *s;
	int r = asprintf(&s, "%d-%s-%.2f", 42, "x", 1.5);
	printf("asprintf %d '%s'\n", r, s);
	free(s);
	fflush(stdout);
	r = dprintf(1, "dprintf %d\n", 7);
	printf("dprintf returned %d\n", r);

	char buf[32] = "hello world";
	FILE *m = fmemopen(buf, strlen(buf), "r");
	char word[16];
	while (fscanf(m, "%15s", word) == 1)
		printf("fmemopen read '%s'\n", word);
	printf("fmemopen eof %d\n", feof(m) != 0);
	fclose(m);
	memset(buf, 'x', sizeof buf);
	m = fmemopen(buf, sizeof buf, "w");
	fprintf(m, "%d %s", 12, "ab");
	fflush(m);
	printf("fmemopen write '%s' tell %ld\n", buf, ftell(m));
	fseek(m, 1, SEEK_SET);
	fputc('9', m);
	fclose(m);
	printf("fmemopen after close '%s'\n", buf);
	m = fmemopen(NULL, 16, "w+");
	fputs("scratch", m);
	rewind(m);
	printf("fmemopen null '%s'\n", fgets(word, sizeof word, m));
	fclose(m);

	char *ms;
	size_t msize;
	m = open_memstream(&ms, &msize);
	fprintf(m, "memstream %d", 1);
	fflush(m);
	printf("open_memstream '%s' %zu\n", ms, msize);
	for (int i = 0; i < 1000; i++)
		fputc('a' + i % 26, m);
	fclose(m);
	printf("open_memstream %zu %.5s %s\n", msize, ms + 12, ms + msize - 3);
	free(ms);
}

static void testStrings(void) {
	char text[] = "a,b,,c";
	char *p = text, *tok;
	printf("strsep:");
	while ((tok = strsep(&p, ",")) != NULL)
		printf(" '%s'", tok);
	printf(" end %d\n", p == NULL);
	int sigs[] = {1, 2, 9, 11, 13, 15};
	for (int i = 0; i < 6; i++)
		printf("strsignal %d %s\n", sigs[i], strsignal(sigs[i]));
	char d[8];
	printf("strlcpy %zu '%s'", strlcpy(d, "truncated!", sizeof d), d);
	printf(" strlcat %zu '%s'\n", strlcat(d, "xyz", sizeof d), d);

	int *a = reallocarray(NULL, 4, sizeof(int));
	a[0] = 3, a[1] = 1, a[2] = 4, a[3] = 1;
	errno = 0;
	int *big = reallocarray(a, SIZE_MAX / 2, 4);
	printf("reallocarray overflow %d %d\n", big == NULL, errno == ENOMEM);
	int dir = -1;
	qsort_r(a, 4, sizeof(int), cmpR, &dir);
	printf("qsort_r %d %d %d %d\n", a[0], a[1], a[2], a[3]);
	free(a);
	char tmpl[] = "dirXXXXXX";
	char *dn = mkdtemp(tmpl);
	struct stat st;
	printf("mkdtemp %d %d %d\n", dn == tmpl, strncmp(tmpl, "dir", 3) == 0 && strlen(tmpl) == 9,
		stat(tmpl, &st) == 0 && S_ISDIR(st.st_mode) && (st.st_mode & 0777) == 0700);
	rmdir(tmpl);
	char bad[] = "dirXXXX";
	dn = mkdtemp(bad);
	printf("mkdtemp bad %d %d\n", dn == NULL, errno == EINVAL);
}

int main(void) {
	testGetoptLong();
	testErr();
	testLibgen();
	testBytes();
	testFnmatch();
	testGlob();
	testSearch();
	testMalloc();
	testUio();
	testUname();
	testLanginfo();
	testIconv();
	testStdio();
	testStrings();
	fflush(stdout);
	int devnull = open("/dev/null", O_WRONLY);
	dup2(devnull, 2);
	errx(0, "done");
}
