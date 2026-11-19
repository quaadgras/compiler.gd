// ndbm.h, devctl.h, ppoll, sig2str and str2sig: TestPxMisc checks the
// output against what POSIX says (the host's musl has no ndbm.h,
// devctl.h or sig2str to compare with).

#include <devctl.h>
#include <errno.h>
#include <fcntl.h>
#include <ndbm.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <time.h>
#include <unistd.h>

static datum d(const char *s) { return (datum){(void *)s, strlen(s)}; }

static void dbm(void) {
	DBM *db = dbm_open("test.db", O_RDWR | O_CREAT | O_TRUNC, 0644);
	printf("open %d\n", db != NULL);
	int r1 = dbm_store(db, d("b"), d("bee"), DBM_INSERT);
	int r2 = dbm_store(db, d("a"), d("ay"), DBM_INSERT);
	int r3 = dbm_store(db, d("c"), d("sea"), DBM_INSERT);
	printf("store %d %d %d\n", r1, r2, r3);
	r1 = dbm_store(db, d("a"), d("x"), DBM_INSERT);
	r2 = dbm_store(db, d("c"), d("see"), DBM_REPLACE);
	printf("insert again %d, replace %d\n", r1, r2);
	datum v = dbm_fetch(db, d("a"));
	printf("fetch a %.*s\n", (int)v.dsize, (char *)v.dptr);
	v = dbm_fetch(db, d("zz"));
	printf("fetch zz %d\n", v.dptr == NULL);
	r1 = dbm_delete(db, d("b"));
	r2 = dbm_delete(db, d("b"));
	printf("delete b %d, again %d\n", r1, r2);
	dbm_close(db);
	db = dbm_open("test.db", O_RDONLY, 0);
	printf("reopened:");
	for (datum k = dbm_firstkey(db); k.dptr != NULL; k = dbm_nextkey(db)) {
		datum val = dbm_fetch(db, k);
		printf(" %.*s=%.*s", (int)k.dsize, (char *)k.dptr, (int)val.dsize, (char *)val.dptr);
	}
	r1 = dbm_store(db, d("q"), d("q"), DBM_REPLACE);
	printf("\nreadonly store %d error %d", r1, dbm_error(db) != 0);
	dbm_clearerr(db);
	printf(" cleared %d\n", dbm_error(db));
	dbm_close(db);
	printf("missing %d\n", dbm_open("none.db", O_RDONLY, 0) == NULL);
	unlink("test.db.pag");
	unlink("test.db.dir");
}

static void misc(void) {
	int p[2];
	pipe(p);
	struct pollfd pfd = {p[0], POLLIN, 0};
	struct timespec ts = {0, 20000000};
	int r = ppoll(&pfd, 1, &ts, NULL);
	printf("ppoll timeout %d\n", r);
	write(p[1], "abc", 3);
	r = ppoll(&pfd, 1, NULL, NULL);
	printf("ppoll ready %d %d\n", r, pfd.revents & POLLIN);
	int n = -1, info = -1;
	r = posix_devctl(p[0], FIONREAD, &n, sizeof n, &info);
	printf("devctl %d %d %d\n", r, n, info);
	r = posix_devctl(p[0], 0x5401, &n, sizeof n, NULL); // TCGETS on a pipe
	printf("devctl tcgets %d\n", r == ENOTTY);
	close(p[0]);
	close(p[1]);

	char s[SIG2STR_MAX];
	int sigs[] = {SIGHUP, SIGTERM, SIGCHLD, 34, 40, 60, 64};
	for (int i = 0; i < 7; i++) {
		sig2str(sigs[i], s);
		int back = 0;
		str2sig(s, &back);
		printf("%d %s %d\n", sigs[i], s, back);
	}
	int sig = 0;
	r = str2sig("9", &sig);
	printf("str2sig 9 %d %d", r, sig);
	r = str2sig("NOPE", &sig);
	printf(", bad %d", r);
	r = str2sig("SIGINT", &sig);
	printf(", SIGINT %d\n", r);
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	dbm();
	misc();
	return 0;
}
