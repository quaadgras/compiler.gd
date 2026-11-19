// epoll, eventfd, timerfd, signalfd, waitid and wait4, sigtimedwait and
// its kin, pthread_kill, sigaltstack and setitimer: what TestEvents
// compares with the host's C library. Only what both print alike is
// printed (no pids, no exact timings).

#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <spawn.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/epoll.h>
#include <sys/eventfd.h>
#include <sys/resource.h>
#include <sys/signalfd.h>
#include <sys/time.h>
#include <sys/timerfd.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

// check prints r and whether errno, after the call that made r, is want.
static void check(const char *what, long r, int want) { printf("%s: %ld %d\n", what, r, errno == want); }

static void sleep_ms(long ms) {
	struct timespec ts = {ms / 1000, ms % 1000 * 1000000};
	nanosleep(&ts, NULL);
}

static int marker = 77;

static void test_epoll(void) {
	int p[2];
	pipe(p);
	int ep = epoll_create1(EPOLL_CLOEXEC);
	printf("epoll_create1: %d\n", ep >= 0);
	check("epoll_create 0", epoll_create(0), EINVAL);
	check("epoll_create1 bad flags", epoll_create1(1), EINVAL);
	struct epoll_event ev = {.events = EPOLLIN, .data.ptr = &marker}, out[4];
	printf("add: %d\n", epoll_ctl(ep, EPOLL_CTL_ADD, p[0], &ev));
	check("add again", epoll_ctl(ep, EPOLL_CTL_ADD, p[0], &ev), EEXIST);
	check("mod absent", epoll_ctl(ep, EPOLL_CTL_MOD, p[1], &ev), ENOENT);
	check("del absent", epoll_ctl(ep, EPOLL_CTL_DEL, p[1], &ev), ENOENT);
	check("add bad fd", epoll_ctl(ep, EPOLL_CTL_ADD, 999, &ev), EBADF);
	check("add self", epoll_ctl(ep, EPOLL_CTL_ADD, ep, &ev), EINVAL);
	check("not epoll", epoll_ctl(p[0], EPOLL_CTL_ADD, p[1], &ev), EINVAL);
	check("bad op", epoll_ctl(ep, 99, p[1], &ev), EINVAL);
	int fd = open("events.tmp", O_RDWR | O_CREAT | O_TRUNC, 0644);
	unlink("events.tmp");
	check("regular file", epoll_ctl(ep, EPOLL_CTL_ADD, fd, &ev), EPERM);
	close(fd);
	check("maxevents 0", epoll_wait(ep, out, 0, 0), EINVAL);

	printf("nothing: %d\n", epoll_wait(ep, out, 4, 0));
	write(p[1], "ab", 2);
	int n = epoll_wait(ep, out, 4, 1000);
	printf("level: %d %#x %d\n", n, out[0].events, *(int *)out[0].data.ptr);
	n = epoll_wait(ep, out, 4, 1000);
	printf("level again: %d %#x\n", n, out[0].events);
	char buf[16];
	printf("read: %d\n", (int)read(p[0], buf, sizeof buf));
	printf("drained: %d\n", epoll_wait(ep, out, 4, 0));

	// Edge-triggered: reported again only when more arrives.
	ev.events = EPOLLIN | EPOLLET;
	ev.data.u64 = 0x1122334455667788;
	printf("mod: %d\n", epoll_ctl(ep, EPOLL_CTL_MOD, p[0], &ev));
	write(p[1], "c", 1);
	n = epoll_wait(ep, out, 4, 1000);
	printf("edge: %d %#x %#llx\n", n, out[0].events, (unsigned long long)out[0].data.u64);
	printf("edge again: %d\n", epoll_wait(ep, out, 4, 50));
	write(p[1], "d", 1);
	printf("edge after write: %d\n", epoll_wait(ep, out, 4, 1000));
	printf("edge again: %d\n", epoll_wait(ep, out, 4, 50));

	// One-shot: disabled once reported, until EPOLL_CTL_MOD.
	ev.events = EPOLLIN | EPOLLONESHOT;
	ev.data.fd = p[0];
	printf("mod oneshot: %d\n", epoll_ctl(ep, EPOLL_CTL_MOD, p[0], &ev));
	n = epoll_wait(ep, out, 4, 1000);
	printf("oneshot: %d %d\n", n, out[0].data.fd == p[0]);
	printf("oneshot again: %d\n", epoll_wait(ep, out, 4, 50));
	check("oneshot add", epoll_ctl(ep, EPOLL_CTL_ADD, p[0], &ev), EEXIST);
	printf("rearm: %d\n", epoll_ctl(ep, EPOLL_CTL_MOD, p[0], &ev));
	printf("rearmed: %d\n", epoll_wait(ep, out, 4, 1000));

	// The write end, and an epoll instance in poll and in another.
	int ep2 = epoll_create(1);
	ev.events = EPOLLOUT;
	ev.data.u32 = 7;
	epoll_ctl(ep2, EPOLL_CTL_ADD, p[1], &ev);
	n = epoll_wait(ep2, out, 4, 1000);
	printf("writable: %d %#x %u\n", n, out[0].events, out[0].data.u32);
	struct pollfd pfd = {ep2, POLLIN, 0};
	n = poll(&pfd, 1, 1000);
	printf("poll epoll: %d %#x\n", n, pfd.revents);
	int ep3 = epoll_create1(0);
	ev.events = EPOLLIN;
	ev.data.u32 = 9;
	printf("nest: %d\n", epoll_ctl(ep3, EPOLL_CTL_ADD, ep2, &ev));
	check("loop", epoll_ctl(ep2, EPOLL_CTL_ADD, ep3, &ev), ELOOP);
	n = epoll_wait(ep3, out, 4, 1000);
	printf("nested: %d %#x %u\n", n, out[0].events, out[0].data.u32);
	printf("del: %d\n", epoll_ctl(ep2, EPOLL_CTL_DEL, p[1], NULL));
	printf("nested empty: %d\n", epoll_wait(ep3, out, 4, 0));
	close(ep3);
	close(ep2);

	// The writer's end: hang-up.
	read(p[0], buf, sizeof buf);
	ev.events = EPOLLIN;
	epoll_ctl(ep, EPOLL_CTL_MOD, p[0], &ev);
	close(p[1]);
	n = epoll_wait(ep, out, 4, 1000);
	printf("hangup: %d %#x\n", n, out[0].events);
	close(p[0]);
	printf("closed: %d\n", epoll_wait(ep, out, 4, 0));

	// maxevents smaller than what is ready: the others come next.
	int a[2], b[2];
	pipe(a);
	pipe(b);
	ev.events = EPOLLIN;
	ev.data.fd = a[0];
	epoll_ctl(ep, EPOLL_CTL_ADD, a[0], &ev);
	ev.data.fd = b[0];
	epoll_ctl(ep, EPOLL_CTL_ADD, b[0], &ev);
	write(a[1], "x", 1);
	write(b[1], "y", 1);
	struct pollfd both[2] = {{a[0], POLLIN, 0}, {b[0], POLLIN, 0}};
	while (poll(both, 2, 1000) < 2)
		;
	int first, second;
	epoll_wait(ep, out, 1, 1000);
	first = out[0].data.fd;
	epoll_wait(ep, out, 1, 1000);
	second = out[0].data.fd;
	printf("round robin: %d\n", first != second);
	n = epoll_wait(ep, out, 4, 1000);
	printf("both: %d\n", n);
	close(a[0]);
	close(a[1]);
	close(b[0]);
	close(b[1]);
	close(ep);
}

static void *late_write(void *arg) {
	sleep_ms(20);
	eventfd_write(*(int *)arg, 9);
	return NULL;
}

static void test_eventfd(void) {
	int efd = eventfd(3, EFD_NONBLOCK | EFD_SEMAPHORE);
	uint64_t v = 0;
	for (int i = 0; i < 3; i++) {
		read(efd, &v, 8);
		printf("semaphore read: %llu\n", (unsigned long long)v);
	}
	check("semaphore empty", read(efd, &v, 8), EAGAIN);
	printf("eventfd_write: %d\n", eventfd_write(efd, 5));
	printf("eventfd_read: %d", eventfd_read(efd, &v));
	printf(" %llu\n", (unsigned long long)v);
	close(efd);

	efd = eventfd(0, EFD_NONBLOCK | EFD_CLOEXEC);
	check("empty", read(efd, &v, 8), EAGAIN);
	v = 2;
	write(efd, &v, 8);
	v = 3;
	write(efd, &v, 8);
	read(efd, &v, 8);
	printf("counter: %llu\n", (unsigned long long)v);
	uint32_t small;
	check("short read", read(efd, &small, 4), EINVAL);
	check("short write", write(efd, &small, 4), EINVAL);
	v = UINT64_MAX;
	check("write max", write(efd, &v, 8), EINVAL);
	v = 1;
	write(efd, &v, 8);
	v = UINT64_MAX - 1;
	check("write full", write(efd, &v, 8), EAGAIN);
	struct pollfd pfd = {efd, POLLIN | POLLOUT, 0};
	int k = poll(&pfd, 1, 0);
	printf("poll: %d %#x\n", k, pfd.revents);
	read(efd, &v, 8);
	k = poll(&pfd, 1, 0);
	printf("poll empty: %d %#x\n", k, pfd.revents);

	int ep = epoll_create1(0);
	struct epoll_event ev = {.events = EPOLLIN | EPOLLET, .data.fd = efd}, out[2];
	epoll_ctl(ep, EPOLL_CTL_ADD, efd, &ev);
	printf("edge none: %d\n", epoll_wait(ep, out, 2, 0));
	eventfd_write(efd, 1);
	printf("edge: %d\n", epoll_wait(ep, out, 2, 1000));
	printf("edge again: %d\n", epoll_wait(ep, out, 2, 0));
	eventfd_write(efd, 1);
	printf("edge after write: %d\n", epoll_wait(ep, out, 2, 1000));
	close(ep);

	// A blocking read waits for a write from another thread.
	int bfd = eventfd(0, 0);
	pthread_t t;
	pthread_create(&t, NULL, late_write, &bfd);
	read(bfd, &v, 8);
	printf("blocking read: %llu\n", (unsigned long long)v);
	pthread_join(t, NULL);
	close(bfd);
	close(efd);
	check("bad flags", eventfd(0, 0x10), EINVAL);
}

static void test_timerfd(void) {
	check("bad clock", timerfd_create(99, 0), EINVAL);
	int tfd = timerfd_create(CLOCK_MONOTONIC, TFD_CLOEXEC);
	struct itimerspec its = {{0, 0}, {0, 30000000}}, cur;
	printf("settime: %d\n", timerfd_settime(tfd, 0, &its, NULL));
	timerfd_gettime(tfd, &cur);
	printf("armed: %d\n", cur.it_value.tv_sec == 0 && cur.it_value.tv_nsec > 0 && cur.it_value.tv_nsec <= 30000000);
	uint64_t n = 0;
	read(tfd, &n, 8);
	printf("one-shot: %llu\n", (unsigned long long)n);
	timerfd_gettime(tfd, &cur);
	printf("disarmed: %d\n", cur.it_value.tv_sec == 0 && cur.it_value.tv_nsec == 0);

	// An interval, read late: the expirations are counted.
	its = (struct itimerspec){{0, 20000000}, {0, 20000000}};
	timerfd_settime(tfd, 0, &its, NULL);
	sleep_ms(150);
	read(tfd, &n, 8);
	printf("interval count: %d\n", n >= 4 && n <= 8);
	read(tfd, &n, 8);
	printf("next: %llu\n", (unsigned long long)n);
	struct itimerspec old;
	its = (struct itimerspec){0};
	timerfd_settime(tfd, 0, &its, &old);
	printf("old interval: %ld %ld\n", (long)old.it_interval.tv_sec, (long)old.it_interval.tv_nsec);
	close(tfd);

	tfd = timerfd_create(CLOCK_REALTIME, TFD_NONBLOCK);
	check("not expired", read(tfd, &n, 8), EAGAIN);
	uint32_t small;
	check("short read", read(tfd, &small, 4), EINVAL);
	its = (struct itimerspec){{0, 0}, {0, 2000000000}};
	check("bad nsec", timerfd_settime(tfd, 0, &its, NULL), EINVAL);
	check("bad flags", timerfd_settime(tfd, 8, &its, NULL), EINVAL);
	check("not a timerfd", timerfd_settime(0, 0, &its, NULL), EINVAL);

	// Absolute, on CLOCK_REALTIME; then poll and epoll wait for it.
	struct timespec now;
	clock_gettime(CLOCK_REALTIME, &now);
	now.tv_nsec += 30000000;
	if (now.tv_nsec >= 1000000000) {
		now.tv_sec++;
		now.tv_nsec -= 1000000000;
	}
	its = (struct itimerspec){{0, 0}, now};
	printf("abstime: %d\n", timerfd_settime(tfd, TFD_TIMER_ABSTIME, &its, NULL));
	struct pollfd pfd = {tfd, POLLIN, 0};
	int k = poll(&pfd, 1, 2000);
	printf("poll: %d %#x\n", k, pfd.revents);
	read(tfd, &n, 8);
	printf("expired: %llu\n", (unsigned long long)n);

	int mfd = timerfd_create(CLOCK_MONOTONIC, 0);
	clock_gettime(CLOCK_MONOTONIC, &now);
	// now: in the past by the time it is set, so at once
	its = (struct itimerspec){{0, 0}, now};
	timerfd_settime(mfd, TFD_TIMER_ABSTIME, &its, NULL);
	int ep = epoll_create1(0);
	struct epoll_event ev = {.events = EPOLLIN, .data.fd = mfd}, out[1];
	epoll_ctl(ep, EPOLL_CTL_ADD, mfd, &ev);
	k = epoll_wait(ep, out, 1, 2000);
	printf("epoll timerfd: %d %d\n", k, out[0].data.fd == mfd);
	read(mfd, &n, 8);
	printf("past: %llu\n", (unsigned long long)n);
	close(ep);
	close(mfd);
	close(tfd);
}

static void test_signalfd(void) {
	sigset_t set, old;
	sigemptyset(&set);
	sigaddset(&set, SIGUSR2);
	sigprocmask(SIG_BLOCK, &set, &old);
	int sfd = signalfd(-1, &set, SFD_NONBLOCK | SFD_CLOEXEC);
	struct signalfd_siginfo si;
	printf("sizeof(struct signalfd_siginfo): %d\n", (int)sizeof si);
	check("nothing", read(sfd, &si, sizeof si), EAGAIN);
	check("short read", read(sfd, &si, 64), EINVAL);
	kill(getpid(), SIGUSR2);
	struct pollfd pfd = {sfd, POLLIN, 0};
	int k = poll(&pfd, 1, 1000);
	printf("poll: %d %#x\n", k, pfd.revents);
	ssize_t n = read(sfd, &si, sizeof si);
	printf("read: %d %d %d %d\n", (int)n, si.ssi_signo == SIGUSR2, si.ssi_code == SI_USER, si.ssi_pid == (uint32_t)getpid());
	sigqueue(getpid(), SIGUSR2, (union sigval){.sival_int = 42});
	n = read(sfd, &si, sizeof si);
	printf("sigqueue: %d %d %d %d\n", (int)n, si.ssi_signo == SIGUSR2, si.ssi_code == SI_QUEUE, si.ssi_int);
	sigset_t pending;
	sigpending(&pending);
	printf("not pending: %d\n", sigismember(&pending, SIGUSR2));
	sigaddset(&set, SIGUSR1);
	printf("new mask: %d\n", signalfd(sfd, &set, 0) == sfd);
	check("not a signalfd", signalfd(0, &set, 0), EINVAL);
	close(sfd);
	sigprocmask(SIG_SETMASK, &old, NULL);
}

static pid_t spawn(const char *script) {
	pid_t pid;
	char *argv[] = {"/bin/sh", "-c", (char *)script, NULL};
	fflush(stdout);
	posix_spawn(&pid, "/bin/sh", NULL, NULL, argv, NULL);
	return pid;
}

static void test_wait(void) {
	pid_t pid = spawn("exit 3");
	int st = 0;
	struct rusage ru;
	memset(&ru, 0xff, sizeof ru);
	pid_t w = wait4(pid, &st, 0, &ru);
	printf("wait4: %d %d %d %d\n", w == pid, WIFEXITED(st), WEXITSTATUS(st), ru.ru_utime.tv_sec >= 0 && ru.ru_utime.tv_usec >= 0 && ru.ru_utime.tv_usec < 1000000);

	pid = spawn("kill -TERM $$");
	siginfo_t si;
	memset(&si, 0, sizeof si);
	printf("waitid: %d", waitid(P_PID, pid, &si, WEXITED));
	printf(" %d %d %d %d %d\n", si.si_signo == SIGCHLD, si.si_code == CLD_KILLED, si.si_status == SIGTERM, si.si_pid == pid, si.si_uid == getuid());

	pid = spawn("exit 5");
	memset(&si, 0, sizeof si);
	waitid(P_ALL, 0, &si, WEXITED | WNOWAIT);
	printf("WNOWAIT: %d %d %d\n", si.si_pid == pid, si.si_code == CLD_EXITED, si.si_status);
	memset(&si, 0, sizeof si);
	waitid(P_PGID, 0, &si, WEXITED);
	printf("still there: %d %d\n", si.si_pid == pid, si.si_status);

	pid = spawn("sleep 0.3; exit 6");
	memset(&si, 0, sizeof si);
	si.si_pid = 123;
	int r = waitid(P_PID, pid, &si, WEXITED | WNOHANG);
	printf("WNOHANG: %d %d\n", r, si.si_pid);
	printf("wait4 WNOHANG: %d\n", wait4(pid, &st, WNOHANG, NULL));
	w = wait3(&st, 0, &ru);
	printf("wait3: %d %d\n", w == pid, WEXITSTATUS(st));

	check("no children", waitid(P_ALL, 0, &si, WEXITED), ECHILD);
	check("no options", waitid(P_ALL, 0, &si, 0), EINVAL);
	check("wait4 none", wait4(-1, &st, 0, &ru), ECHILD);
}

static volatile sig_atomic_t got;
static volatile int got_code, got_value;

static void on_signal(int sig) { got = sig; }

static void on_info(int sig, siginfo_t *si, void *ctx) {
	got = sig;
	got_code = si->si_code;
	got_value = si->si_value.sival_ptr ? *(int *)si->si_value.sival_ptr : -1;
}

static void *waiter(void *arg) {
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGUSR1);
	siginfo_t si;
	int sig = sigwaitinfo(&set, &si);
	*(int *)arg = sig == SIGUSR1 && si.si_code == SI_TKILL;
	return NULL;
}

static void *spinner(void *arg) {
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGUSR2);
	pthread_sigmask(SIG_UNBLOCK, &set, NULL);
	*(volatile int *)arg = 1;
	while (got != SIGUSR2)
		sleep_ms(1);
	return NULL;
}

static void test_signals(void) {
	sigset_t set, old;
	sigemptyset(&set);
	sigaddset(&set, SIGUSR1);
	sigprocmask(SIG_BLOCK, &set, &old);

	struct timespec ts = {0, 50000000};
	siginfo_t si;
	check("sigtimedwait timeout", sigtimedwait(&set, &si, &ts), EAGAIN);
	ts.tv_nsec = 2000000000;
	check("sigtimedwait bad timeout", sigtimedwait(&set, &si, &ts), EINVAL);
	kill(getpid(), SIGUSR1);
	ts = (struct timespec){1, 0};
	int sig = sigtimedwait(&set, &si, &ts);
	printf("sigtimedwait: %d %d %d %d\n", sig == SIGUSR1, si.si_signo == SIGUSR1, si.si_code == SI_USER, si.si_pid == getpid());
	sigqueue(getpid(), SIGUSR1, (union sigval){.sival_int = 1234});
	sig = sigwaitinfo(&set, &si);
	printf("sigwaitinfo: %d %d %d\n", sig == SIGUSR1, si.si_code == SI_QUEUE, si.si_value.sival_int);

	// sigqueue to a handler, with a pointer.
	struct sigaction sa = {0};
	sa.sa_sigaction = on_info;
	sa.sa_flags = SA_SIGINFO;
	sigaction(SIGUSR2, &sa, NULL);
	got = 0;
	sigqueue(getpid(), SIGUSR2, (union sigval){.sival_ptr = &marker});
	printf("sigqueue handler: %d %d %d\n", got == SIGUSR2, got_code == SI_QUEUE, got_value);
	check("sigqueue bad", sigqueue(getpid(), 999, (union sigval){0}), EINVAL);

	// pthread_kill: to a thread in sigwaitinfo, to one that runs a
	// handler, and to the calling thread.
	int ok = 0;
	pthread_t t;
	pthread_create(&t, NULL, waiter, &ok); // inherits the mask: SIGUSR1 blocked
	sleep_ms(20);
	printf("pthread_kill: %d\n", pthread_kill(t, SIGUSR1));
	pthread_join(t, NULL);
	printf("taken by sigwaitinfo: %d\n", ok);

	sa = (struct sigaction){0};
	sa.sa_handler = on_signal;
	sigaction(SIGUSR2, &sa, NULL);
	sigset_t usr2;
	sigemptyset(&usr2);
	sigaddset(&usr2, SIGUSR2);
	sigprocmask(SIG_BLOCK, &usr2, NULL);
	got = 0;
	volatile int started = 0;
	pthread_create(&t, NULL, spinner, (void *)&started);
	while (!started)
		sleep_ms(1);
	printf("pthread_kill 0: %d\n", pthread_kill(t, 0));
	printf("pthread_kill handler: %d\n", pthread_kill(t, SIGUSR2));
	pthread_join(t, NULL);
	printf("handler ran: %d\n", got == SIGUSR2);
	sigprocmask(SIG_UNBLOCK, &usr2, NULL);
	got = 0;
	int r = pthread_kill(pthread_self(), SIGUSR2);
	printf("pthread_kill self: %d %d\n", r, got == SIGUSR2);
	printf("pthread_kill bad: %d\n", pthread_kill(pthread_self(), 999) == EINVAL);

	// epoll_pwait's mask lets a pending signal's handler run: EINTR.
	sa.sa_handler = on_signal;
	sigaction(SIGUSR1, &sa, NULL);
	got = 0;
	kill(getpid(), SIGUSR1);
	int ep = epoll_create1(0);
	struct epoll_event out[1];
	sigset_t none;
	sigemptyset(&none);
	check("epoll_pwait", epoll_pwait(ep, out, 1, 1000, &none), EINTR);
	printf("handler: %d\n", got == SIGUSR1);
	close(ep);
	sigprocmask(SIG_SETMASK, &old, NULL);

	// sigaltstack: kept and given back.
	stack_t ss, oss;
	sigaltstack(NULL, &oss);
	printf("altstack initially: %d\n", oss.ss_flags == SS_DISABLE);
	ss.ss_sp = malloc(SIGSTKSZ);
	ss.ss_size = SIGSTKSZ;
	ss.ss_flags = 0;
	printf("sigaltstack: %d\n", sigaltstack(&ss, NULL));
	sigaltstack(NULL, &oss);
	printf("altstack: %d %d %d\n", oss.ss_sp == ss.ss_sp, oss.ss_flags, oss.ss_size == SIGSTKSZ);
	ss.ss_size = 100;
	check("altstack small", sigaltstack(&ss, NULL), ENOMEM);
	ss.ss_flags = 99;
	ss.ss_size = SIGSTKSZ;
	check("altstack flags", sigaltstack(&ss, NULL), EINVAL);
	ss.ss_flags = SS_DISABLE;
	sigaltstack(&ss, NULL);
	sigaltstack(NULL, &oss);
	printf("altstack disabled: %d\n", oss.ss_flags == SS_DISABLE);

	// siginterrupt: SA_RESTART.
	sa = (struct sigaction){0};
	sa.sa_handler = on_signal;
	sa.sa_flags = SA_RESTART;
	sigaction(SIGUSR1, &sa, NULL);
	siginterrupt(SIGUSR1, 1);
	sigaction(SIGUSR1, NULL, &sa);
	printf("siginterrupt 1: %d\n", (sa.sa_flags & SA_RESTART) != 0);
	siginterrupt(SIGUSR1, 0);
	sigaction(SIGUSR1, NULL, &sa);
	printf("siginterrupt 0: %d\n", (sa.sa_flags & SA_RESTART) != 0);
	check("siginterrupt bad", siginterrupt(0, 1), EINVAL);
	signal(SIGUSR1, SIG_DFL);
	signal(SIGUSR2, SIG_DFL);
}

static volatile sig_atomic_t alarms;

static void on_alarm(int sig) { alarms++; }

static void test_itimer(void) {
	signal(SIGALRM, on_alarm);
	struct itimerval it = {{0, 20000}, {0, 20000}}, old;
	printf("setitimer: %d\n", setitimer(ITIMER_REAL, &it, NULL));
	while (alarms < 3)
		pause();
	it = (struct itimerval){0};
	setitimer(ITIMER_REAL, &it, &old);
	printf("alarms: %d, old interval %ld %ld\n", alarms >= 3, (long)old.it_interval.tv_sec, (long)old.it_interval.tv_usec);
	getitimer(ITIMER_REAL, &old);
	printf("disarmed: %d\n", old.it_value.tv_sec == 0 && old.it_value.tv_usec == 0);

	// ITIMER_REAL is alarm's.
	it = (struct itimerval){{0, 0}, {5, 0}};
	setitimer(ITIMER_REAL, &it, NULL);
	printf("alarm sees it: %u\n", alarm(0));
	alarm(10);
	getitimer(ITIMER_REAL, &old);
	printf("getitimer sees alarm: %d\n", old.it_value.tv_sec >= 9 && old.it_value.tv_sec <= 10);
	alarm(0);

	it = (struct itimerval){{0, 0}, {0, 2000000}};
	check("bad usec", setitimer(ITIMER_REAL, &it, NULL), EINVAL);
	check("bad which", getitimer(5, &old), EINVAL);
	it = (struct itimerval){0};
	printf("virtual: %d\n", setitimer(ITIMER_VIRTUAL, &it, NULL));
	getitimer(ITIMER_PROF, &old);
	printf("prof: %d\n", old.it_value.tv_sec == 0 && old.it_value.tv_usec == 0);
	signal(SIGALRM, SIG_DFL);
}

int main(void) {
	setvbuf(stdout, NULL, _IOLBF, 0);
	test_epoll();
	test_eventfd();
	test_timerfd();
	test_signalfd();
	test_wait();
	test_signals();
	test_itimer();
	return 0;
}
