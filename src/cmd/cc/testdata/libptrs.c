// What the library hands a program pointers to, used as a program that
// wraps the library uses it: copied with memcpy, kept in memory of the
// program's own, converted through void *, resized and freed. Each line
// is a name and 1; TestLibraryPointers fails on anything else.

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <time.h>
#include <locale.h>
#include <pwd.h>
#include <grp.h>
#include <netdb.h>
#include <glob.h>
#include <dirent.h>
#include <unistd.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <ifaddrs.h>
#include <wordexp.h>
#include <regex.h>
#include <search.h>
#include <ftw.h>
#include <getopt.h>
#include <sys/utsname.h>
#include <langinfo.h>
#include <iconv.h>
#include <spawn.h>
#include <sys/uio.h>

extern char **environ;

/* Everything the library hands out is looked at through a copy that
   memcpy made, and through void *: as a program that wraps it does. */
static void *through(void *p) { void *volatile v = p; return v; }

static int cmp(const void *a, const void *b) { return strcmp(a, b); }
static int nftwfn(const char *p, const struct stat *st, int f, struct FTW *w) { return p && st && w ? 0 : 1; }
static int sel(const struct dirent *d) { return d->d_name[0] != '.'; }

int main(int argc, char **argv) {
	// environ, argv
	int n = 0;
	for (char **e = through(environ); *e; e++) n += strlen(*e) > 0;
	printf("environ: %d\n", n > 0);
	char **av = malloc((argc + 1) * sizeof *av);
	memcpy(av, argv, (argc + 1) * sizeof *av);
	printf("argv: %d\n", strlen(av[0]) > 0 && av[argc] == 0);

	// time
	time_t t = 86400 * 365;
	struct tm *tm = gmtime(&t), tmc;
	memcpy(&tmc, tm, sizeof tmc);
	printf("tm: %d\n", tmc.tm_year == 71);
	struct tm *lt = through(localtime(&t));
	printf("localtime: %d\n", lt->tm_year >= 70);
	printf("tzname: %d\n", strlen(tzname[0]) > 0);

	// locale
	struct lconv *lc = localeconv(), lcc = *lc;
	printf("lconv: %d\n", strcmp(lcc.decimal_point, ".") == 0);

	// passwd, group
	struct passwd *pw = getpwuid(getuid());
	if (pw) {
		struct passwd pwc; memcpy(&pwc, pw, sizeof pwc);
		printf("passwd: %d\n", strlen(pwc.pw_name) > 0 && pwc.pw_dir != 0);
		struct passwd st, *res; char buf[4096];
		printf("getpwnam_r: %d\n", getpwnam_r(pwc.pw_name, &st, buf, sizeof buf, &res) == 0 && res == &st && st.pw_name >= buf && st.pw_name < buf + sizeof buf);
	}
	struct group *gr = getgrgid(getgid());
	if (gr) {
		char **mem = through(gr->gr_mem);
		int k = 0; while (mem[k]) k++;
		printf("group: %d\n", strlen(gr->gr_name) > 0 && k >= 0);
	}

	// netdb
	struct addrinfo hints = {0}, *ai;
	hints.ai_family = AF_INET; hints.ai_socktype = SOCK_STREAM; hints.ai_flags = AI_NUMERICHOST | AI_CANONNAME;
	int e = getaddrinfo("127.0.0.1", "80", &hints, &ai);
	if (e == 0) {
		struct addrinfo c; memcpy(&c, ai, sizeof c);
		struct sockaddr_in *sin = (struct sockaddr_in *)through(c.ai_addr);
		printf("addrinfo: %d\n", strcmp(inet_ntoa(sin->sin_addr), "127.0.0.1") == 0 && ntohs(sin->sin_port) == 80);
		freeaddrinfo(ai);
	} else printf("getaddrinfo: %d\n", strlen(gai_strerror(e)) > 0);
	struct hostent *he = gethostbyname("127.0.0.1");
	if (he) {
		char **al = through(he->h_addr_list);
		struct in_addr ia; memcpy(&ia, al[0], sizeof ia);
		printf("hostent: %d\n", strlen(he->h_name) > 0 && strcmp(inet_ntoa(ia), "127.0.0.1") == 0);
	}
	struct servent *se = getservbyname("http", "tcp");
	if (se) printf("servent: %d\n", strcmp(se->s_name, "http") == 0 && ntohs(se->s_port) == 80);
	struct protoent *pe = getprotobyname("tcp");
	if (pe) printf("protoent: %d\n", strcmp(pe->p_name, "tcp") == 0 && pe->p_proto == 6);
	struct ifaddrs *ifa;
	if (getifaddrs(&ifa) == 0) {
		int k = 0;
		for (struct ifaddrs *i = through(ifa); i; i = i->ifa_next) k += i->ifa_name != 0 && (i->ifa_addr == 0 || i->ifa_addr->sa_family >= 0);
		printf("ifaddrs: %d\n", k > 0);
		freeifaddrs(ifa);
	}

	// glob, wordexp, scandir
	glob_t g = {0};
	if (glob("/etc/pass*", 0, 0, &g) == 0) {
		char **pv = through(g.gl_pathv);
		glob_t gc; memcpy(&gc, &g, sizeof gc);
		printf("glob: %d\n", gc.gl_pathc > 0 && strncmp(pv[0], "/etc/pass", 9) == 0);
		globfree(&gc);
	}
	wordexp_t we;
	if (wordexp("a 'b c' d", &we, 0) == 0) {
		char **wv = through(we.we_wordv);
		printf("wordexp: %d\n", we.we_wordc == 3 && strcmp(wv[1], "b c") == 0);
		wordfree(&we);
	}
	struct dirent **nl;
	int nn = scandir("/etc", &nl, sel, alphasort);
	if (nn > 0) {
		struct dirent **c = malloc(nn * sizeof *c);
		memcpy(c, nl, nn * sizeof *c);
		printf("scandir: %d\n", strlen(c[0]->d_name) > 0);
		for (int i = 0; i < nn; i++) free(c[i]);
		free(nl); free(c);
	}
	DIR *d = opendir("/etc");
	struct dirent *de = readdir(d), dec;
	memcpy(&dec, de, sizeof dec);
	printf("readdir: %d\n", strlen(dec.d_name) > 0);
	closedir(d);

	// regex
	regex_t re; regmatch_t m[2];
	regcomp(&re, "b(c+)", REG_EXTENDED);
	regex_t rec; memcpy(&rec, &re, sizeof rec);
	int rx = regexec(&rec, "abccd", 2, m, 0);
	printf("regex: %d\n", rx == 0 && m[1].rm_eo == 4);
	regfree(&rec);

	// search.h
	void *root = 0;
	char *keys[] = {"b", "a", "c"};
	for (int i = 0; i < 3; i++) tsearch(keys[i], &root, cmp);
	char **found = tfind("a", &root, cmp);
	printf("tsearch: %d\n", found && strcmp(*found, "a") == 0);
	hcreate(8);
	ENTRY en = {"k", "v"}, *ep = hsearch(en, ENTER);
	ENTRY ec; memcpy(&ec, ep, sizeof ec);
	printf("hsearch: %d\n", strcmp(ec.data, "v") == 0);
	hdestroy();

	// ftw, fts
	printf("nftw: %d\n", nftw("/etc/passwd", nftwfn, 4, 0) == 0);

	// misc strings of the library's
	struct utsname un; uname(&un);
	printf("uname: %d\n", strlen(un.sysname) > 0);
	char *se2 = strerror(2), *cp = malloc(strlen(se2) + 1);
	strcpy(cp, se2);
	printf("strerror: %d\n", strlen(cp) > 0);
	printf("langinfo: %d\n", strlen(nl_langinfo(CODESET)) > 0);
	char *tok, *save, str[] = "x,y,z";
	tok = strtok_r(str, ",", &save); tok = strtok_r(0, ",", &save);
	printf("strtok_r: %d\n", strcmp(tok, "y") == 0 && strcmp(save, "z") == 0);
	char *end; long v = strtol("123abc", &end, 10);
	char **pe2 = through(&end);
	printf("strtol: %d\n", v == 123 && strcmp(*pe2, "abc") == 0);
	FILE *fp = fopen("/etc/passwd", "r");
	char *line = 0; size_t cap = 0;
	ssize_t got = getline(&line, &cap, fp);
	line = realloc(line, got + 10);
	strcat(line, "!");
	printf("getline: %d\n", got > 0 && cap >= (size_t)got);
	free(line); fclose(fp);
	char *as; asprintf(&as, "%d-%s", 7, "x"); as = realloc(as, 32); strcat(as, "y");
	printf("asprintf: %d\n", strcmp(as, "7-xy") == 0);
	char *mem; size_t msz; FILE *ms = open_memstream(&mem, &msz);
	fprintf(ms, "hello %d", 5); fclose(ms);
	mem = realloc(mem, 32); strcat(mem, "!");
	printf("memstream: %d\n", strcmp(mem, "hello 5!") == 0 && msz == 7);
	char *rp = realpath("/etc/../etc/passwd", 0);
	printf("realpath: %d\n", strcmp(rp, "/etc/passwd") == 0); free(rp);
	char *cwd = getcwd(0, 0); printf("getcwd: %d\n", cwd[0] == '/'); free(cwd);
	char *dup = strndup("abcdef", 3); dup = realloc(dup, 8); strcat(dup, "x"); printf("strndup: %d\n", strcmp(dup, "abcx") == 0);
	struct iovec iov[2] = {{"iov:", 4}, {" 1\n", 3}};
	struct iovec *ic = malloc(sizeof iov); memcpy(ic, iov, sizeof iov);
	fflush(stdout); writev(1, ic, 2);
	puts("done");
	return 0;
}
