// The rest of POSIX's files and system interfaces: the *at functions with
// a directory descriptor, dirent.h's, dup3 and pipe2, fseeko and ftello,
// stream locks from two threads, pathconf and confstr, limits, shared
// memory objects, the user and group databases, netdb.h's databases,
// if_nameindex, pseudo-terminals and fexecve. The output must match
// gcc's (with glibc or musl): it prints only what both agree on, and runs
// in an empty directory, where it makes its files. Each call is made
// before the printf that shows it, as printf's arguments are evaluated in
// no order.
#define _GNU_SOURCE
#include <arpa/inet.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <net/if.h>
#include <netdb.h>
#include <poll.h>
#include <pthread.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <termios.h>
#include <unistd.h>
#include <utmpx.h>

extern char **environ;

// fails reports whether r is -1 with errno e.
static int fails(long r, int e) { return r == -1 && errno == e; }

static void atfuncs(void) {
	struct stat st;
	char buf[64];
	int r, r2;
	if (mkdir("d", 0755) != 0) {
		perror("mkdir");
		exit(1);
	}
	int dfd = open("d", O_RDONLY | O_DIRECTORY);
	int fd = openat(dfd, "f", O_CREAT | O_WRONLY | O_TRUNC, 0644);
	printf("openat %d\n", fd >= 0);
	write(fd, "hello\n", 6);
	close(fd);
	r = fstatat(dfd, "f", &st, 0);
	printf("fstatat %d size %ld reg %d\n", r, (long)st.st_size, S_ISREG(st.st_mode));
	r = mkdirat(dfd, "sub", 0755);
	fstatat(dfd, "sub", &st, 0);
	printf("mkdirat %d dir %d\n", r, S_ISDIR(st.st_mode));
	r = symlinkat("f", dfd, "lnk");
	fstatat(dfd, "lnk", &st, AT_SYMLINK_NOFOLLOW);
	printf("symlinkat %d nofollow lnk %d", r, S_ISLNK(st.st_mode));
	fstatat(dfd, "lnk", &st, 0);
	printf(" follow size %ld\n", (long)st.st_size);
	ssize_t n = readlinkat(dfd, "lnk", buf, sizeof buf);
	printf("readlinkat %zd %.*s\n", n, (int)n, buf);
	r = linkat(dfd, "f", dfd, "hard", 0);
	fstatat(dfd, "f", &st, 0);
	printf("linkat %d nlink %ld", r, (long)st.st_nlink);
	r = linkat(dfd, "lnk", dfd, "hard2", AT_SYMLINK_FOLLOW);
	fstatat(dfd, "hard2", &st, AT_SYMLINK_NOFOLLOW);
	printf(" follow %d reg %d nlink %ld\n", r, S_ISREG(st.st_mode), (long)st.st_nlink);
	r = renameat(dfd, "hard2", AT_FDCWD, "moved");
	r2 = stat("moved", &st) == 0 && st.st_size == 6;
	printf("renameat %d stat %d\n", r, r2);
	r = faccessat(dfd, "f", R_OK | W_OK, 0);
	r2 = faccessat(dfd, "f", R_OK, AT_EACCESS);
	printf("faccessat %d eaccess %d", r, r2);
	r = faccessat(dfd, "nope", F_OK, 0);
	printf(" missing %d\n", fails(r, ENOENT));
	r = fchmodat(dfd, "f", 0600, 0);
	fstatat(dfd, "f", &st, 0);
	printf("fchmodat %d mode %o\n", r, st.st_mode & 0777);
	struct timespec ts[2] = {{1000000000, 0}, {1234567890, 500}};
	r = utimensat(dfd, "f", ts, 0);
	fstatat(dfd, "f", &st, 0);
	printf("utimensat %d atime %ld mtime %ld.%ld", r, (long)st.st_atim.tv_sec, (long)st.st_mtim.tv_sec, st.st_mtim.tv_nsec);
	ts[0].tv_nsec = UTIME_NOW;
	ts[1].tv_nsec = UTIME_OMIT;
	utimensat(dfd, "f", ts, 0);
	fstatat(dfd, "f", &st, 0);
	printf(" omit %ld now %d\n", (long)st.st_mtim.tv_sec, st.st_atim.tv_sec > 1000000000);
	fd = openat(dfd, "f", O_RDWR);
	r = futimens(fd, NULL);
	fstat(fd, &st);
	printf("futimens %d %d\n", r, st.st_mtim.tv_sec > 1234567890);
	printf("posix_fadvise %d\n", posix_fadvise(fd, 0, 0, POSIX_FADV_SEQUENTIAL));
	r = lockf(fd, F_LOCK, 0);
	r2 = lockf(fd, F_TEST, 0);
	printf("lockf %d test %d", r, r2);
	r = lockf(fd, F_TLOCK, 10);
	r2 = lockf(fd, F_ULOCK, 0);
	printf(" tlock %d ulock %d", r, r2);
	r = lockf(fd, 99, 0);
	printf(" bad %d\n", fails(r, EINVAL));
	close(fd);
	printf("fchownat %d\n", fchownat(dfd, "f", (uid_t)-1, (gid_t)-1, 0));
	r = mkfifoat(dfd, "fifo", 0600);
	fstatat(dfd, "fifo", &st, 0);
	printf("mkfifoat %d fifo %d", r, S_ISFIFO(st.st_mode));
	r = mkfifo("fifo2", 0600);
	stat("fifo2", &st);
	printf(" mkfifo %d fifo %d\n", r, S_ISFIFO(st.st_mode));
	r = mknodat(dfd, "node", S_IFREG | 0600, 0);
	fstatat(dfd, "node", &st, 0);
	printf("mknodat %d reg %d", r, S_ISREG(st.st_mode));
	r = mknod("node2", S_IFIFO | 0600, 0);
	stat("node2", &st);
	printf(" mknod %d fifo %d\n", r, S_ISFIFO(st.st_mode));
	r = unlinkat(dfd, "sub", 0);
	printf("unlinkat dir %d", fails(r, EISDIR));
	printf(" removedir %d", unlinkat(dfd, "sub", AT_REMOVEDIR));
	r = unlinkat(dfd, "node", AT_REMOVEDIR);
	printf(" notdir %d", fails(r, ENOTDIR));
	printf(" file %d\n", unlinkat(dfd, "node", 0));
	r = openat(12345, "f", O_RDONLY);
	printf("openat badf %d", fails(r, EBADF));
	fd = openat(dfd, "f", O_RDONLY);
	r = openat(fd, "f", O_RDONLY);
	printf(" notdir %d", fails(r, ENOTDIR));
	close(fd);
	fd = openat(fd, "/dev/null", O_RDONLY);
	printf(" abs %d", fd >= 0);
	close(fd);
	fd = openat(AT_FDCWD, "d/f", O_RDONLY);
	printf(" cwd %d\n", fd >= 0);
	close(fd);

	int cwd = open(".", O_RDONLY | O_DIRECTORY);
	r = fchdir(dfd);
	printf("fchdir %d in %d", r, access("f", F_OK) == 0);
	fd = openat(cwd, "moved", O_RDONLY);
	printf(" cwd's %d", fd >= 0);
	close(fd);
	fd = openat(dfd, "f", O_RDONLY);
	printf(" dfd's %d", fd >= 0);
	close(fd);
	r = fchdir(cwd);
	printf(" back %d %d\n", r, access("moved", F_OK) == 0);
	close(cwd);
	close(dfd);
}

static int nodots(const struct dirent *e) { return e->d_name[0] != '.'; }

static void dirs(void) {
	int r;
	int fd = open("d", O_RDONLY | O_DIRECTORY);
	DIR *dp = fdopendir(fd);
	printf("fdopendir %d dirfd %d\n", dp != NULL, dirfd(dp) == fd);
	int count = 0;
	struct dirent *e;
	while ((e = readdir(dp)) != NULL) {
		if (e->d_name[0] != '.')
			count++;
	}
	printf("entries %d\n", count);
	rewinddir(dp);
	readdir(dp);
	long pos = telldir(dp);
	char first[256];
	strcpy(first, readdir(dp)->d_name);
	readdir(dp);
	seekdir(dp, pos);
	printf("seekdir %d\n", strcmp(readdir(dp)->d_name, first) == 0);
	struct dirent ent, *res;
	rewinddir(dp);
	r = readdir_r(dp, &ent, &res);
	printf("readdir_r %d %d\n", r, res == &ent);
	closedir(dp);
	r = close(fd);
	printf("closed fd %d\n", fails(r, EBADF));

	struct dirent **list;
	int n = scandir("d", &list, nodots, alphasort);
	printf("scandir %d:", n);
	for (int i = 0; i < n; i++) {
		printf(" %s", list[i]->d_name);
		free(list[i]);
	}
	printf("\n");
	free(list);
	r = scandir("nope", &list, NULL, alphasort);
	printf("scandir missing %d\n", fails(r, ENOENT));

	dp = opendir("d");
	printf("dirfd %d\n", dirfd(dp) >= 0);
	closedir(dp);
	fd = open("moved", O_RDONLY);
	dp = fdopendir(fd);
	printf("fdopendir file %d %d\n", dp == NULL, errno == ENOTDIR);
	close(fd);

	fd = open("d", O_RDONLY | O_DIRECTORY);
	char buf[4096];
	ssize_t got = posix_getdents(fd, buf, sizeof buf, 0);
	count = 0;
	for (ssize_t off = 0; off < got;) {
		struct posix_dent *d = (struct posix_dent *)(buf + off);
		if (d->d_name[0] != '.')
			count++;
		off += d->d_reclen;
	}
	printf("posix_getdents %d %d", got > 0, count);
	printf(" end %zd\n", posix_getdents(fd, buf, sizeof buf, 0));
	close(fd);
}

static void descriptors(void) {
	int p[2], r, r2;
	r = pipe2(p, O_CLOEXEC | O_NONBLOCK);
	printf("pipe2 %d cloexec %d %d", r, fcntl(p[0], F_GETFD) & FD_CLOEXEC, fcntl(p[1], F_GETFD) & FD_CLOEXEC);
	printf(" nonblock %d", (fcntl(p[0], F_GETFL) & O_NONBLOCK) != 0);
	char c = 0;
	ssize_t k = read(p[0], &c, 1);
	printf(" empty %d", fails(k, EAGAIN));
	write(p[1], "z", 1);
	struct pollfd pfd = {p[0], POLLIN, 0};
	poll(&pfd, 1, 5000); // what a pipe's write brings is read ahead (cc.md)
	k = read(p[0], &c, 1);
	printf(" read %zd %c\n", k, c);
	r = dup3(p[1], p[1], 0);
	printf("dup3 same %d", fails(r, EINVAL));
	r = dup3(p[1], 50, O_CLOEXEC);
	printf(" cloexec %d %d", r, fcntl(50, F_GETFD) & FD_CLOEXEC);
	r = dup3(p[1], 51, 0);
	printf(" plain %d %d", r, fcntl(51, F_GETFD) & FD_CLOEXEC);
	r = dup3(p[1], 52, 12345);
	printf(" badflags %d\n", fails(r, EINVAL));
	close(50);
	close(51);
	close(p[0]);
	close(p[1]);
	r = pipe2(p, 0);
	printf("pipe2 plain %d cloexec %d nonblock %d\n", r, fcntl(p[0], F_GETFD) & FD_CLOEXEC, (fcntl(p[1], F_GETFL) & O_NONBLOCK) != 0);
	r = posix_close(p[0], 0);
	r2 = posix_close(p[0], 0);
	printf("posix_close %d again %d\n", r, fails(r2, EBADF));
	close(p[1]);
}

static FILE *shared;
static int counter, tried;

static void *locker(void *arg) {
	for (int i = 0; i < 1000; i++) {
		flockfile(shared);
		flockfile(shared);
		int c = counter;
		counter = c + 1;
		funlockfile(shared);
		funlockfile(shared);
	}
	return NULL;
}

static void *trier(void *arg) {
	tried = ftrylockfile(shared);
	if (tried == 0)
		funlockfile(shared);
	return NULL;
}

static void streams(void) {
	int r;
	FILE *f = fopen("s.txt", "w+");
	fputs("0123456789", f);
	r = fseeko(f, 3, SEEK_SET);
	long at = (long)ftello(f);
	int c = getc_unlocked(f);
	printf("fseeko %d ftello %ld getc %c", r, at, c);
	fseeko(f, -2, SEEK_END);
	printf(" end %ld\n", (long)ftello(f));
	putc_unlocked('x', f);
	fseeko(f, 0, SEEK_SET);
	char buf[16] = {0};
	fread(buf, 1, 11, f);
	printf("putc_unlocked %s\n", buf);
	fclose(f);
	putchar_unlocked('o');
	putchar_unlocked('k');
	putchar_unlocked('\n');

	shared = fopen("s.txt", "r");
	pthread_t a, b;
	pthread_create(&a, NULL, locker, NULL);
	pthread_create(&b, NULL, locker, NULL);
	pthread_join(a, NULL);
	pthread_join(b, NULL);
	printf("flockfile %d\n", counter);
	flockfile(shared);
	r = ftrylockfile(shared);
	printf("ftrylockfile own %d", r);
	funlockfile(shared);
	pthread_create(&a, NULL, trier, NULL);
	pthread_join(a, NULL);
	printf(" other %d", tried != 0);
	funlockfile(shared);
	pthread_create(&a, NULL, trier, NULL);
	pthread_join(a, NULL);
	printf(" free %d\n", tried);
	fclose(shared);

	char t[L_ctermid];
	ctermid(t);
	printf("ctermid %s %s\n", ctermid(NULL), t);
}

static void limits(void) {
	int r, r2;
	int fd = open("moved", O_RDONLY);
	printf("pathconf %ld %ld %ld %ld\n", pathconf(".", _PC_NAME_MAX), pathconf(".", _PC_PATH_MAX),
	       pathconf(".", _PC_PIPE_BUF), fpathconf(fd, _PC_NAME_MAX));
	close(fd);
	char buf[64];
	size_t n = confstr(_CS_PATH, buf, sizeof buf);
	printf("confstr %zu %s %zu", n, buf, confstr(_CS_PATH, NULL, 0));
	confstr(_CS_PATH, buf, 5);
	printf(" short %s", buf);
	errno = 0;
	n = confstr(-1, buf, sizeof buf);
	printf(" bad %zu %d\n", n, errno == EINVAL);

	struct rlimit rl;
	r = getrlimit(RLIMIT_NOFILE, &rl);
	printf("getrlimit %d sane %d", r, rl.rlim_cur <= rl.rlim_max && rl.rlim_cur > 0);
	getrlimit(RLIMIT_STACK, &rl);
	printf(" stack %d", rl.rlim_cur > 0);
	r = getrlimit(9999, &rl);
	printf(" bad %d", fails(r, EINVAL));
	getrlimit(RLIMIT_CORE, &rl);
	printf(" set %d", setrlimit(RLIMIT_CORE, &rl));
	rl.rlim_cur = rl.rlim_max == RLIM_INFINITY ? RLIM_INFINITY : rl.rlim_max + 1;
	rl.rlim_max = 0;
	r = setrlimit(RLIMIT_CORE, &rl);
	printf(" over %d\n", fails(r, EINVAL));
	errno = 0;
	int prio = getpriority(PRIO_PROCESS, 0);
	r = errno == 0 && prio >= -20 && prio <= 19;
	printf("getpriority %d nice %d\n", r, nice(0) == prio);

	int ngroups = getgroups(0, NULL);
	gid_t groups[256];
	printf("getgroups %d\n", ngroups >= 0 && getgroups(256, groups) == ngroups);
	int ids[6] = {setuid(getuid()), setgid(getgid()), seteuid(geteuid()), setegid(getegid()), setreuid(-1, -1), setregid(-1, -1)};
	printf("setuid %d %d %d %d %d %d\n", ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]);
	if (getuid() != 0) {
		r = setuid(0);
		printf("setuid root %d\n", fails(r, EPERM));
	}
	sync();
}

static void shm(void) {
	char name[64];
	int r, r2;
	snprintf(name, sizeof name, "/pxfiles-%d", (int)getpid());
	int fd = shm_open(name, O_CREAT | O_EXCL | O_RDWR, 0600);
	printf("shm_open %d", fd >= 0);
	r = shm_open(name, O_CREAT | O_EXCL | O_RDWR, 0600);
	printf(" excl %d", fails(r, EEXIST));
	printf(" ftruncate %d", ftruncate(fd, 4096));
	char *p = mmap(NULL, 4096, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	int fd2 = shm_open(name, O_RDWR, 0);
	char *q = mmap(NULL, 4096, PROT_READ | PROT_WRITE, MAP_SHARED, fd2, 0);
	printf(" mmap %d", p != MAP_FAILED && q != MAP_FAILED);
	strcpy(p, "shared");
	printf(" %s", q);
	r = mlock(p, 4096);
	r2 = munlock(p, 4096);
	printf(" mlock %d %d %d\n", r, r2, posix_madvise(p, 4096, POSIX_MADV_NORMAL));
	munmap(p, 4096);
	munmap(q, 4096);
	close(fd);
	close(fd2);
	r = shm_unlink(name);
	r2 = shm_unlink(name);
	printf("shm_unlink %d again %d", r, fails(r2, ENOENT));
	r = shm_open("/a/b", O_RDONLY, 0);
	printf(" bad %d\n", fails(r, EINVAL));
}

static void users(void) {
	struct passwd pw, *res;
	char buf[1024];
	int r = getpwuid_r(getuid(), &pw, buf, sizeof buf, &res);
	char name[256] = "";
	if (res)
		strcpy(name, res->pw_name);
	struct passwd *me = getpwuid(getuid());
	printf("getpwuid_r %d %d", r, res == &pw && me && strcmp(name, me->pw_name) == 0);
	r = getpwnam_r(name, &pw, buf, sizeof buf, &res);
	printf(" getpwnam_r %d %d", r, res == &pw && pw.pw_uid == getuid());
	r = getpwuid_r(getuid(), &pw, buf, 1, &res);
	printf(" erange %d", r == ERANGE);
	r = getpwnam_r("no-such-user-pxfiles", &pw, buf, sizeof buf, &res);
	printf(" none %d %d\n", r, res == NULL);
	setpwent();
	int root = 0;
	struct passwd *e;
	while ((e = getpwent()) != NULL) {
		if (e->pw_uid == 0 && strcmp(e->pw_name, "root") == 0)
			root = 1;
	}
	endpwent();
	printf("getpwent root %d\n", root);

	struct group gr, *gres;
	r = getgrgid_r(0, &gr, buf, sizeof buf, &gres);
	printf("getgrgid_r %d %d", r, gres == &gr && strcmp(gr.gr_name, "root") == 0);
	r = getgrnam_r("root", &gr, buf, sizeof buf, &gres);
	printf(" getgrnam_r %d %d", r, gres == &gr && gr.gr_gid == 0 && gr.gr_mem != NULL);
	r = getgrnam_r("root", &gr, buf, 1, &gres);
	printf(" erange %d\n", r == ERANGE);
	setgrent();
	root = 0;
	struct group *g;
	while ((g = getgrent()) != NULL) {
		if (g->gr_gid == 0)
			root = 1;
	}
	endgrent();
	printf("getgrent root %d\n", root);

	setutxent();
	while (getutxent() != NULL)
		;
	endutxent();
	printf("utmpx %d\n", (int)sizeof(struct utmpx) > 0);
}

static void net(void) {
	if (access("/etc/services", R_OK) == 0) {
		struct servent *s = getservbyport(htons(22), "tcp");
		printf("getservbyport %d\n", s != NULL && strcmp(s->s_name, "ssh") == 0 && s->s_port == htons(22));
	}
	setprotoent(1);
	struct protoent *p;
	int tcp = 0;
	while ((p = getprotoent()) != NULL) {
		if (strcmp(p->p_name, "tcp") == 0 && p->p_proto == 6)
			tcp = 1;
	}
	endprotoent();
	printf("getprotoent tcp %d\n", tcp);

	struct if_nameindex *ifs = if_nameindex();
	int lo = 0;
	for (struct if_nameindex *i = ifs; i && i->if_index != 0; i++) {
		if (strcmp(i->if_name, "lo") == 0)
			lo = 1;
	}
	printf("if_nameindex lo %d\n", lo);
	if_freenameindex(ifs);
}

static void ptys(void) {
	int r, r2;
	int m = posix_openpt(O_RDWR | O_NOCTTY);
	if (m < 0) {
		printf("no ptys\n");
		return;
	}
	r = grantpt(m);
	r2 = unlockpt(m);
	printf("posix_openpt grantpt %d unlockpt %d", r, r2);
	char *name = ptsname(m);
	printf(" ptsname %d", name != NULL && strncmp(name, "/dev/pts/", 9) == 0);
	char buf[128], small[3], tty[128];
	r = ptsname_r(m, buf, sizeof buf);
	printf(" ptsname_r %d %d", r, strcmp(buf, name) == 0);
	printf(" erange %d\n", ptsname_r(m, small, sizeof small) == ERANGE);
	int s = open(buf, O_RDWR | O_NOCTTY);
	printf("slave %d isatty %d", s >= 0, isatty(s));
	char *tn = ttyname(s);
	printf(" ttyname %d", tn != NULL && strcmp(tn, buf) == 0);
	r = ttyname_r(s, tty, sizeof tty);
	printf(" ttyname_r %d %d", r, strcmp(tty, buf) == 0);
	printf(" erange %d\n", ttyname_r(s, small, sizeof small) == ERANGE);
	struct winsize ws = {24, 80, 0, 0}, got;
	r = tcsetwinsize(m, &ws);
	r2 = tcgetwinsize(s, &got);
	printf("tcsetwinsize %d tcgetwinsize %d %d %d\n", r, r2, got.ws_row, got.ws_col);
	int fd = open("moved", O_RDONLY);
	errno = 0;
	tn = ttyname(fd);
	printf("ttyname file %d %d", tn == NULL, errno == ENOTTY);
	printf(" ttyname_r %d", ttyname_r(fd, tty, sizeof tty) == ENOTTY);
	r = tcgetwinsize(fd, &got);
	printf(" tcgetwinsize %d\n", fails(r, ENOTTY));
	close(fd);
	close(s);
	close(m);
}

int main(void) {
	atfuncs();
	dirs();
	descriptors();
	streams();
	limits();
	shm();
	users();
	net();
	ptys();
	int fd = open("/bin/sh", O_RDONLY);
	if (fd >= 0) {
		char *argv[] = {"sh", "-c", "echo fexecve $0", NULL};
		fflush(stdout);
		fexecve(fd, argv, environ);
		printf("fexecve failed\n");
	}
	return 0;
}
