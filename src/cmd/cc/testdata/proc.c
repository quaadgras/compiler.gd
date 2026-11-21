// Processes, signals, scheduling and semaphores: what TestProc compares
// with the host's C library. Only what both print alike is printed (no
// pids, no timings).

#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <sched.h>
#include <semaphore.h>
#include <signal.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static void atexit_handler(void) { printf("atexit ran\n"); }

// The second program: argv[1] is "exec".
static int exec_child(void) {
	atexit(atexit_handler);
	printf("before exec\n");
	fflush(stdout);
	char *args[] = {"sh", "-c", "echo exec ran; exit 7", NULL};
	execvp("sh", args);
	printf("execvp failed\n");
	return 1;
}

// check prints r and whether errno, after the call that made r, is want.
static void check(const char *what, long r, int want) { printf("%s: %ld %d\n", what, r, errno == want); }

static int spawn_wait(char *const argv[], posix_spawn_file_actions_t *fa, char *const envp[]) {
	pid_t pid;
	fflush(stdout);
	int e = posix_spawnp(&pid, argv[0], fa, NULL, argv, envp);
	if (e != 0) {
		printf("spawn %s: %s\n", argv[0], strerror(e));
		return -1;
	}
	int st;
	pid_t w = waitpid(pid, &st, 0);
	if (w != pid) {
		printf("waitpid: %d\n", w);
		return -1;
	}
	return st;
}

static void show(const char *what, int st) {
	if (WIFEXITED(st))
		printf("%s: exited %d\n", what, WEXITSTATUS(st));
	else if (WIFSIGNALED(st))
		printf("%s: signal %d\n", what, WTERMSIG(st));
	else
		printf("%s: status %#x\n", what, st);
}

static void test_wait(char *self) {
	char *exit3[] = {"/bin/sh", "-c", "exit 3", NULL};
	show("exit 3", spawn_wait(exit3, NULL, NULL));
	char *term[] = {"/bin/sh", "-c", "kill -TERM $$", NULL};
	show("kill -TERM", spawn_wait(term, NULL, NULL));

	char *env[] = {"FOO=bar", NULL};
	char *echo[] = {"sh", "-c", "echo FOO=$FOO", NULL};
	show("env", spawn_wait(echo, NULL, env));

	pid_t pid;
	char *slow[] = {"/bin/sh", "-c", "sleep 0.3; exit 4", NULL};
	posix_spawn(&pid, "/bin/sh", NULL, NULL, slow, NULL);
	int st = -1;
	printf("WNOHANG: %d\n", waitpid(pid, &st, WNOHANG));
	printf("wait: %d\n", wait(&st) == pid);
	show("slow", st);
	check("no children", wait(&st), ECHILD);
	check("waitpid unknown", waitpid(1, &st, 0), ECHILD);

	char *none[] = {"/nonexistent/prog", NULL};
	printf("spawn missing: %s\n", strerror(posix_spawn(&pid, none[0], NULL, NULL, none, NULL)));

	// kill of a child.
	char *sleeper[] = {"/bin/sh", "-c", "exec sleep 10", NULL};
	posix_spawn(&pid, "/bin/sh", NULL, NULL, sleeper, NULL);
	printf("kill 0: %d\n", kill(pid, 0));
	printf("kill TERM: %d\n", kill(pid, SIGTERM));
	waitpid(pid, &st, 0);
	show("killed", st);

	// The exec family, in a second program.
	char *again[] = {self, "exec", NULL};
	show("exec child", spawn_wait(again, NULL, NULL));
	check("execl missing", execl("/nonexistent/prog", "prog", (char *)NULL), ENOENT);
	printf("fork in Go: skipped\n");
}

static void test_file_actions(void) {
	int p[2];
	pipe(p);
	posix_spawn_file_actions_t fa;
	posix_spawn_file_actions_init(&fa);
	posix_spawn_file_actions_adddup2(&fa, p[1], 1);
	posix_spawn_file_actions_addclose(&fa, p[0]);
	posix_spawn_file_actions_addclose(&fa, p[1]);
	char *echo[] = {"echo", "through", "a", "pipe", NULL};
	pid_t pid;
	fflush(stdout);
	printf("spawnp: %d\n", posix_spawnp(&pid, "echo", &fa, NULL, echo, NULL));
	close(p[1]);
	char buf[100];
	ssize_t n, k = 0;
	while ((n = read(p[0], buf + k, sizeof buf - 1 - k)) > 0)
		k += n;
	buf[k] = 0;
	close(p[0]);
	printf("read: %s", buf);
	int st;
	waitpid(pid, &st, 0);
	show("echo", st);
	posix_spawn_file_actions_destroy(&fa);

	// addopen: the child's standard output is a file.
	posix_spawn_file_actions_init(&fa);
	posix_spawn_file_actions_addopen(&fa, 1, "proc.out", O_WRONLY | O_CREAT | O_TRUNC, 0644);
	char *cat[] = {"sh", "-c", "echo to a file", NULL};
	show("addopen", spawn_wait(cat, &fa, NULL));
	posix_spawn_file_actions_destroy(&fa);
	FILE *f = fopen("proc.out", "r");
	fgets(buf, sizeof buf, f);
	fclose(f);
	remove("proc.out");
	printf("file: %s", buf);

	// A descriptor that is close-on-exec is not the child's, unless
	// dup2 makes it one.
	int fd = open("proc.out", O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
	posix_spawn_file_actions_init(&fa);
	posix_spawn_file_actions_adddup2(&fa, fd, 1);
	char *echo2[] = {"echo", "via dup2", NULL};
	show("cloexec dup2", spawn_wait(echo2, &fa, NULL));
	posix_spawn_file_actions_destroy(&fa);
	close(fd);
	f = fopen("proc.out", "r");
	fgets(buf, sizeof buf, f);
	fclose(f);
	remove("proc.out");
	printf("file: %s", buf);

	posix_spawnattr_t attr;
	short flags;
	posix_spawnattr_init(&attr);
	posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETPGROUP);
	posix_spawnattr_getflags(&attr, &flags);
	printf("attr flags: %d\n", flags);
	char *exit0[] = {"/bin/sh", "-c", "exit 0", NULL};
	fflush(stdout);
	printf("spawn setpgroup: %d\n", posix_spawn(&pid, "/bin/sh", NULL, &attr, exit0, NULL));
	waitpid(pid, &st, 0);
	show("setpgroup", st);
	posix_spawnattr_destroy(&attr);
}

static volatile sig_atomic_t got;

static void handler(int sig) {
	sigset_t cur;
	sigprocmask(SIG_BLOCK, NULL, &cur);
	printf("handler %d (blocked in it: %d %d)\n", sig, sigismember(&cur, sig), sigismember(&cur, SIGUSR2));
	got = sig;
}

static void info_handler(int sig, siginfo_t *si, void *uc) {
	printf("info handler %d: signo %d, code %s, pid %s\n", sig, si->si_signo,
	       si->si_code == SI_USER ? "SI_USER" : si->si_code == SI_TKILL ? "SI_TKILL" : "other",
	       si->si_pid == getpid() ? "self" : "other");
}

static void plain(int sig) {
	printf("plain %d\n", sig);
	got = sig;
}

static void *killer(void *arg) {
	struct timespec ts = {0, 20000000};
	nanosleep(&ts, NULL);
	kill(getpid(), *(int *)arg);
	return NULL;
}

static void test_signals(void) {
	sigset_t s;
	sigemptyset(&s);
	sigaddset(&s, SIGINT);
	sigaddset(&s, SIGUSR1);
	printf("set: %d %d %d\n", sigismember(&s, SIGINT), sigismember(&s, SIGUSR1), sigismember(&s, SIGTERM));
	sigdelset(&s, SIGINT);
	printf("del: %d %d\n", sigismember(&s, SIGINT), sigaddset(&s, 0));
	sigfillset(&s);
	printf("fill: %d %d\n", sigismember(&s, SIGTERM), sigismember(&s, SIGKILL));

	struct sigaction sa, old;
	memset(&sa, 0, sizeof sa);
	sa.sa_handler = handler;
	sigemptyset(&sa.sa_mask);
	sigaddset(&sa.sa_mask, SIGUSR2);
	sigaction(SIGUSR1, &sa, NULL);
	sigaction(SIGUSR1, NULL, &old);
	printf("old: %d %d\n", old.sa_handler == handler, sigismember(&old.sa_mask, SIGUSR2));
	printf("kill self: %d\n", kill(getpid(), SIGUSR1));
	printf("raise: %d\n", raise(SIGUSR1));
	check("sigaction SIGKILL", sigaction(SIGKILL, &sa, NULL), EINVAL);

	memset(&sa, 0, sizeof sa);
	sa.sa_sigaction = info_handler;
	sa.sa_flags = SA_SIGINFO;
	sigaction(SIGUSR2, &sa, NULL);
	kill(getpid(), SIGUSR2);
	raise(SIGUSR2);

	// SA_RESETHAND: once, then the default.
	memset(&sa, 0, sizeof sa);
	sa.sa_handler = plain;
	sa.sa_flags = SA_RESETHAND;
	sigaction(SIGWINCH, &sa, NULL);
	raise(SIGWINCH);
	sigaction(SIGWINCH, NULL, &old);
	printf("reset: %d\n", old.sa_handler == SIG_DFL);
	raise(SIGWINCH); // ignored by default

	// signal() and sigaction agree.
	printf("signal: %d\n", signal(SIGUSR1, plain) == handler);
	sigaction(SIGUSR1, NULL, &old);
	printf("signal's action: %d\n", old.sa_handler == plain);
	signal(SIGUSR1, SIG_IGN);
	raise(SIGUSR1);
	printf("ignored\n");
	signal(SIGUSR1, plain);

	// A mask defers raise and kill.
	sigset_t block, prev, pend;
	sigemptyset(&block);
	sigaddset(&block, SIGUSR1);
	sigaddset(&block, SIGHUP);
	sigaddset(&block, SIGKILL);
	sigprocmask(SIG_BLOCK, &block, &prev);
	printf("prev: %d\n", sigismember(&prev, SIGUSR1));
	sigprocmask(SIG_BLOCK, NULL, &prev);
	printf("blocked: %d %d\n", sigismember(&prev, SIGUSR1), sigismember(&prev, SIGKILL));
	got = 0;
	raise(SIGUSR1);
	signal(SIGHUP, plain);
	kill(getpid(), SIGHUP);
	printf("deferred: %d\n", got);
	sigpending(&pend);
	printf("pending: %d %d %d\n", sigismember(&pend, SIGUSR1), sigismember(&pend, SIGHUP), sigismember(&pend, SIGUSR2));
	printf("unblocking\n");
	sigprocmask(SIG_UNBLOCK, &block, NULL);
	printf("unblocked: %d\n", got);
	sigpending(&pend);
	printf("pending: %d\n", sigismember(&pend, SIGUSR1));
	signal(SIGHUP, SIG_DFL);

	// sigsuspend: another thread's kill wakes it.
	sigemptyset(&block);
	sigaddset(&block, SIGUSR1);
	pthread_sigmask(SIG_BLOCK, &block, NULL);
	got = 0;
	pthread_t t;
	int usr1 = SIGUSR1;
	pthread_create(&t, NULL, killer, &usr1);
	sigset_t empty;
	sigemptyset(&empty);
	int r;
	while (!got) {
		errno = 0;
		r = sigsuspend(&empty);
		printf("sigsuspend: %d %d\n", r, errno == EINTR);
	}
	pthread_join(t, NULL);
	sigprocmask(SIG_BLOCK, NULL, &prev);
	printf("mask restored: %d\n", sigismember(&prev, SIGUSR1));

	// sigwait takes a blocked signal.
	raise(SIGUSR1);
	int sig = 0;
	r = sigwait(&block, &sig);
	printf("sigwait: %d %d\n", r, sig);
	pthread_create(&t, NULL, killer, &usr1);
	sig = 0;
	r = sigwait(&block, &sig);
	printf("sigwait: %d %d\n", r, sig);
	pthread_join(t, NULL);
	sigprocmask(SIG_UNBLOCK, &block, NULL);

	// alarm.
	printf("alarm: %u\n", alarm(5));
	printf("alarm left: %u\n", alarm(0));
	signal(SIGALRM, plain);
	got = 0;
	alarm(1);
	errno = 0;
	r = pause();
	printf("pause: %d %d %d\n", r, errno == EINTR, got);

	// SIGCHLD.
	signal(SIGCHLD, plain);
	sigemptyset(&block);
	sigaddset(&block, SIGCHLD);
	sigprocmask(SIG_BLOCK, &block, NULL);
	char *exit0[] = {"/bin/sh", "-c", "exit 0", NULL};
	got = 0;
	show("child", spawn_wait(exit0, NULL, NULL));
	sigpending(&pend);
	printf("SIGCHLD pending: %d %d\n", sigismember(&pend, SIGCHLD), got);
	sigprocmask(SIG_UNBLOCK, &block, NULL);
	printf("SIGCHLD: %d\n", got);
	signal(SIGCHLD, SIG_DFL);

	// strsignal and psignal (into a file).
	printf("%s; %s; %s; %s\n", strsignal(SIGINT), strsignal(SIGTERM), strsignal(SIGUSR1), strsignal(SIGKILL));
	fflush(stderr);
	int save = dup(2);
	int fd = open("proc.err", O_WRONLY | O_CREAT | O_TRUNC, 0644);
	dup2(fd, 2);
	close(fd);
	psignal(SIGSEGV, "psignal");
	psignal(SIGPIPE, NULL);
	dup2(save, 2);
	close(save);
	char buf[200];
	FILE *f = fopen("proc.err", "r");
	while (fgets(buf, sizeof buf, f))
		printf("stderr: %s", buf);
	fclose(f);
	remove("proc.err");
}

static sem_t items, done;
static int sum;

static void *producer(void *arg) {
	for (int i = 1; i <= 10; i++) {
		sum += i;
		sem_post(&items);
	}
	sem_wait(&done);
	return NULL;
}

static void test_sched_sem(void) {
	printf("sched_yield: %d\n", sched_yield());
	printf("sched_getcpu: %d\n", sched_getcpu() >= 0);
	cpu_set_t cs;
	CPU_ZERO(&cs);
	CPU_SET(1, &cs);
	CPU_SET(70, &cs);
	printf("cpu set: %d %d %d %d\n", !!CPU_ISSET(0, &cs), !!CPU_ISSET(1, &cs), !!CPU_ISSET(70, &cs), CPU_COUNT(&cs));
	CPU_CLR(1, &cs);
	printf("cpu clr: %d %d\n", !!CPU_ISSET(1, &cs), CPU_COUNT(&cs));
	printf("affinity: %d %d\n", sched_getaffinity(0, sizeof cs, &cs), CPU_COUNT(&cs) >= 1);
	printf("priorities: %d %d %d %d\n", sched_get_priority_min(SCHED_FIFO), sched_get_priority_max(SCHED_FIFO),
	       sched_get_priority_min(SCHED_OTHER), sched_get_priority_max(SCHED_OTHER));

	sem_init(&items, 0, 0);
	sem_init(&done, 0, 0);
	pthread_t t;
	pthread_create(&t, NULL, producer, NULL);
	for (int i = 0; i < 10; i++)
		sem_wait(&items);
	printf("sum: %d\n", sum);
	int v = -1;
	sem_getvalue(&items, &v);
	printf("value: %d\n", v);
	check("trywait", sem_trywait(&items), EAGAIN);
	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	ts.tv_nsec += 50000000;
	if (ts.tv_nsec >= 1000000000) {
		ts.tv_sec++;
		ts.tv_nsec -= 1000000000;
	}
	check("timedwait", sem_timedwait(&items, &ts), ETIMEDOUT);
	sem_post(&done);
	pthread_join(t, NULL);
	sem_post(&items);
	printf("timedwait posted: %d\n", sem_timedwait(&items, &ts));
	sem_destroy(&items);
	sem_destroy(&done);

	sem_unlink("/cc_proc_test");
	sem_t *s = sem_open("/cc_proc_test", O_CREAT | O_EXCL, 0600, 2);
	printf("sem_open: %d\n", s != SEM_FAILED);
	sem_getvalue(s, &v);
	printf("named value: %d\n", v);
	check("excl", sem_open("/cc_proc_test", O_CREAT | O_EXCL, 0600, 0) == SEM_FAILED, EEXIST);
	sem_t *s2 = sem_open("/cc_proc_test", 0);
	sem_wait(s2);
	sem_getvalue(s, &v);
	printf("shared value: %d\n", v);
	sem_close(s2);
	printf("sem_close: %d\n", sem_close(s));
	printf("sem_unlink: %d\n", sem_unlink("/cc_proc_test"));
	check("sem_unlink again", sem_unlink("/cc_proc_test"), ENOENT);
	check("missing", sem_open("/cc_proc_test", 0) == SEM_FAILED, ENOENT);
}

int main(int argc, char **argv) {
	if (argc > 1 && strcmp(argv[1], "exec") == 0)
		return exec_child();
	printf("getpgrp: %d\n", getpgrp() == getpgid(0));
	printf("getppid: %d\n", getppid() > 0);
	test_wait(argv[0]);
	test_file_actions();
	test_signals();
	test_sched_sem();
	return 0;
}
