// POSIX threads, timers and signals: attributes, read-write locks,
// barriers, spin locks, cancellation and cleanup handlers, robust
// mutexes, condition variables on CLOCK_MONOTONIC, timer_create,
// clock_nanosleep, killpg, the System V signal functions and
// sigsetjmp. TestPxThreads compares the output with the host C
// library's: it prints only what is deterministic, and what glibc and
// musl agree on.

#include <errno.h>
#include <limits.h>
#include <pthread.h>
#include <sched.h>
#include <semaphore.h>
#include <setjmp.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

static const char *ename(int e) {
	switch (e) {
	case 0: return "0";
	case EINVAL: return "EINVAL";
	case EBUSY: return "EBUSY";
	case EDEADLK: return "EDEADLK";
	case EPERM: return "EPERM";
	case ESRCH: return "ESRCH";
	case ETIMEDOUT: return "ETIMEDOUT";
	case ENOTSUP: return "ENOTSUP";
	case EOWNERDEAD: return "EOWNERDEAD";
	case ENOTRECOVERABLE: return "ENOTRECOVERABLE";
	case EAGAIN: return "EAGAIN";
	}
	static char buf[32];
	snprintf(buf, sizeof buf, "errno %d", e);
	return buf;
}

static long long nsnow(clockid_t c) {
	struct timespec ts;
	clock_gettime(c, &ts);
	return ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

static struct timespec after(clockid_t c, long ms) {
	struct timespec ts;
	clock_gettime(c, &ts);
	ts.tv_nsec += ms * 1000000;
	ts.tv_sec += ts.tv_nsec / 1000000000;
	ts.tv_nsec %= 1000000000;
	return ts;
}

static void attrs(void) {
	pthread_attr_t a;
	int v;
	size_t sz;
	void *p;
	static char stack[1 << 16];
	struct sched_param sp;
	printf("attr_init %d\n", pthread_attr_init(&a));
	pthread_attr_getdetachstate(&a, &v);
	printf("detachstate %d\n", v);
	printf("set detached %d\n", pthread_attr_setdetachstate(&a, PTHREAD_CREATE_DETACHED));
	pthread_attr_getdetachstate(&a, &v);
	printf("detachstate %d\n", v == PTHREAD_CREATE_DETACHED);
	printf("set bad detachstate %s\n", ename(pthread_attr_setdetachstate(&a, 5)));
	pthread_attr_setguardsize(&a, 3 * 4096);
	pthread_attr_getguardsize(&a, &sz);
	printf("guardsize %zu\n", sz);
	printf("stacksize 1M %d\n", pthread_attr_setstacksize(&a, 1 << 20));
	pthread_attr_getstacksize(&a, &sz);
	printf("stacksize %zu\n", sz);
	printf("stacksize 100 %s\n", ename(pthread_attr_setstacksize(&a, 100)));
	printf("PTHREAD_STACK_MIN ok %d\n", PTHREAD_STACK_MIN >= 2048);
	printf("setstack %d\n", pthread_attr_setstack(&a, stack, sizeof stack));
	pthread_attr_getstack(&a, &p, &sz);
	printf("stack %d %zu\n", p == stack, sz);
	pthread_attr_getinheritsched(&a, &v);
	printf("inheritsched %d\n", v == PTHREAD_INHERIT_SCHED);
	printf("explicit %d\n", pthread_attr_setinheritsched(&a, PTHREAD_EXPLICIT_SCHED));
	pthread_attr_getinheritsched(&a, &v);
	printf("inheritsched %d\n", v == PTHREAD_EXPLICIT_SCHED);
	pthread_attr_getschedpolicy(&a, &v);
	printf("schedpolicy %d\n", v == SCHED_OTHER);
	pthread_attr_getschedparam(&a, &sp);
	printf("priority %d\n", sp.sched_priority);
	sp.sched_priority = 0;
	printf("setschedparam %d\n", pthread_attr_setschedparam(&a, &sp));
	pthread_attr_getscope(&a, &v);
	printf("scope %d\n", v == PTHREAD_SCOPE_SYSTEM);
	printf("scope process %s\n", ename(pthread_attr_setscope(&a, PTHREAD_SCOPE_PROCESS)));
	printf("scope system %d\n", pthread_attr_setscope(&a, PTHREAD_SCOPE_SYSTEM));
	printf("attr_destroy %d\n", pthread_attr_destroy(&a));

	pthread_mutexattr_t ma;
	pthread_mutexattr_init(&ma);
	pthread_mutexattr_settype(&ma, PTHREAD_MUTEX_RECURSIVE);
	pthread_mutexattr_setrobust(&ma, PTHREAD_MUTEX_ROBUST);
	pthread_mutexattr_setpshared(&ma, PTHREAD_PROCESS_SHARED);
	pthread_mutexattr_setprotocol(&ma, PTHREAD_PRIO_INHERIT);
	pthread_mutexattr_gettype(&ma, &v);
	printf("mutex type %d", v == PTHREAD_MUTEX_RECURSIVE);
	pthread_mutexattr_getrobust(&ma, &v);
	printf(" robust %d", v == PTHREAD_MUTEX_ROBUST);
	pthread_mutexattr_getpshared(&ma, &v);
	printf(" pshared %d", v == PTHREAD_PROCESS_SHARED);
	pthread_mutexattr_getprotocol(&ma, &v);
	printf(" protocol %d\n", v == PTHREAD_PRIO_INHERIT);
	printf("bad robust %s\n", ename(pthread_mutexattr_setrobust(&ma, 7)));
	pthread_mutex_t m;
	printf("mutex init %d\n", pthread_mutex_init(&m, &ma));
	printf("lock %d", pthread_mutex_lock(&m));
	printf(" lock %d", pthread_mutex_lock(&m));
	printf(" unlock %d", pthread_mutex_unlock(&m));
	printf(" unlock %d\n", pthread_mutex_unlock(&m));
	pthread_mutex_destroy(&m);
	pthread_mutexattr_destroy(&ma);

	pthread_condattr_t ca;
	clockid_t clk;
	pthread_condattr_init(&ca);
	pthread_condattr_getclock(&ca, &clk);
	printf("cond clock %d\n", clk == CLOCK_REALTIME);
	printf("setclock monotonic %d\n", pthread_condattr_setclock(&ca, CLOCK_MONOTONIC));
	printf("setclock cputime %s\n", ename(pthread_condattr_setclock(&ca, CLOCK_PROCESS_CPUTIME_ID)));
	pthread_condattr_getclock(&ca, &clk);
	printf("cond clock %d\n", clk == CLOCK_MONOTONIC);
	pthread_condattr_setpshared(&ca, PTHREAD_PROCESS_SHARED);
	pthread_condattr_getpshared(&ca, &v);
	pthread_condattr_getclock(&ca, &clk);
	printf("cond pshared %d clock %d\n", v == PTHREAD_PROCESS_SHARED, clk == CLOCK_MONOTONIC);
	pthread_condattr_destroy(&ca);

	pthread_rwlockattr_t ra;
	pthread_rwlockattr_init(&ra);
	pthread_rwlockattr_getpshared(&ra, &v);
	printf("rwlock pshared %d", v);
	pthread_rwlockattr_setpshared(&ra, PTHREAD_PROCESS_SHARED);
	pthread_rwlockattr_getpshared(&ra, &v);
	printf(" %d bad %s\n", v == PTHREAD_PROCESS_SHARED, ename(pthread_rwlockattr_setpshared(&ra, 9)));
	pthread_rwlockattr_destroy(&ra);

	pthread_barrierattr_t ba;
	pthread_barrierattr_init(&ba);
	pthread_barrierattr_setpshared(&ba, PTHREAD_PROCESS_SHARED);
	pthread_barrierattr_getpshared(&ba, &v);
	printf("barrier pshared %d\n", v == PTHREAD_PROCESS_SHARED);
	pthread_barrierattr_destroy(&ba);
}

static sem_t detached_done;

static void *detached(void *arg) {
	*(int *)arg = 42;
	sem_post(&detached_done);
	return NULL;
}

static void *nothing(void *arg) { return arg; }

static void threads(void) {
	pthread_attr_t a;
	pthread_t t;
	static int v;
	sem_init(&detached_done, 0, 0);
	pthread_attr_init(&a);
	pthread_attr_setdetachstate(&a, PTHREAD_CREATE_DETACHED);
	printf("create detached %d\n", pthread_create(&t, &a, detached, &v));
	sem_wait(&detached_done);
	printf("detached ran %d\n", v);
	pthread_attr_destroy(&a);

	void *r;
	pthread_create(&t, NULL, nothing, &v);
	printf("detach %d\n", pthread_detach(t));
	pthread_create(&t, NULL, nothing, &v);
	int e = pthread_join(t, &r);
	printf("join %d %d\n", e, r == &v);

	int policy;
	struct sched_param sp = {0};
	printf("setschedparam %d", pthread_setschedparam(pthread_self(), SCHED_OTHER, &sp));
	sp.sched_priority = 5;
	printf(" getschedparam %d", pthread_getschedparam(pthread_self(), &policy, &sp));
	printf(" policy %d priority %d", policy == SCHED_OTHER, sp.sched_priority);
	printf(" setschedprio %d\n", pthread_setschedprio(pthread_self(), 0));
	printf("setconcurrency %d", pthread_setconcurrency(0));
	printf(" %s", ename(pthread_setconcurrency(-1)));
	printf(" getconcurrency %d\n", pthread_getconcurrency());
	printf("atfork %d\n", pthread_atfork(NULL, NULL, NULL));

	clockid_t c;
	struct timespec ts;
	printf("getcpuclockid %d", pthread_getcpuclockid(pthread_self(), &c));
	printf(" gettime %d", clock_gettime(c, &ts));
	printf(" process %d", clock_getcpuclockid(0, &c));
	printf(" gettime %d\n", clock_gettime(c, &ts));
}

// Read-write locks: readers share the lock, a writer has it alone.

static pthread_rwlock_t rw = PTHREAD_RWLOCK_INITIALIZER;
static pthread_barrier_t rbar;
static long shared_count;

static void *reader(void *arg) {
	pthread_rwlock_rdlock(&rw);
	pthread_barrier_wait(&rbar); // all three hold the read lock at once
	pthread_rwlock_unlock(&rw);
	return NULL;
}

static void *writer(void *arg) {
	for (int i = 0; i < 1000; i++) {
		pthread_rwlock_wrlock(&rw);
		long v = shared_count;
		sched_yield();
		shared_count = v + 1;
		pthread_rwlock_unlock(&rw);
		if (i % 100 == 0) {
			pthread_rwlock_rdlock(&rw);
			(void)shared_count;
			pthread_rwlock_unlock(&rw);
		}
	}
	return NULL;
}

static void rwlocks(void) {
	pthread_t t[4];
	pthread_barrier_init(&rbar, NULL, 3);
	for (int i = 0; i < 3; i++)
		pthread_create(&t[i], NULL, reader, NULL);
	for (int i = 0; i < 3; i++)
		pthread_join(t[i], NULL);
	pthread_barrier_destroy(&rbar);
	printf("readers shared the lock\n");

	printf("rdlock %d", pthread_rwlock_rdlock(&rw));
	printf(" tryrdlock %d", pthread_rwlock_tryrdlock(&rw));
	printf(" trywrlock %s", ename(pthread_rwlock_trywrlock(&rw)));
	struct timespec ts = after(CLOCK_REALTIME, 20);
	printf(" timedwrlock %s", ename(pthread_rwlock_timedwrlock(&rw, &ts)));
	printf(" unlock %d", pthread_rwlock_unlock(&rw));
	printf(" unlock %d\n", pthread_rwlock_unlock(&rw));
	printf("wrlock %d", pthread_rwlock_wrlock(&rw));
	printf(" tryrdlock %s", ename(pthread_rwlock_tryrdlock(&rw)));
	printf(" trywrlock %s", ename(pthread_rwlock_trywrlock(&rw)));
	printf(" unlock %d\n", pthread_rwlock_unlock(&rw));
	ts = after(CLOCK_REALTIME, 20);
	printf("timedrdlock %d", pthread_rwlock_timedrdlock(&rw, &ts));
	printf(" unlock %d\n", pthread_rwlock_unlock(&rw));

	for (int i = 0; i < 4; i++)
		pthread_create(&t[i], NULL, writer, NULL);
	for (int i = 0; i < 4; i++)
		pthread_join(t[i], NULL);
	printf("writers counted %ld\n", shared_count);
	pthread_rwlock_destroy(&rw);
}

// Barriers: each round, one thread is the serial thread.

static pthread_barrier_t bar;
static atomic_int arrived, serials, bad_rounds;

static void *barrier_thread(void *arg) {
	for (int round = 0; round < 5; round++) {
		atomic_fetch_add(&arrived, 1);
		int r = pthread_barrier_wait(&bar);
		if (r == PTHREAD_BARRIER_SERIAL_THREAD)
			atomic_fetch_add(&serials, 1);
		else if (r != 0)
			atomic_fetch_add(&bad_rounds, 1);
		if (atomic_load(&arrived) < 4 * (round + 1))
			atomic_fetch_add(&bad_rounds, 1);
		r = pthread_barrier_wait(&bar);
		if (r != 0 && r != PTHREAD_BARRIER_SERIAL_THREAD)
			atomic_fetch_add(&bad_rounds, 1);
	}
	return NULL;
}

static void barriers(void) {
	pthread_t t[4];
	printf("barrier count 0 %s\n", ename(pthread_barrier_init(&bar, NULL, 0)));
	pthread_barrier_init(&bar, NULL, 4);
	for (int i = 0; i < 4; i++)
		pthread_create(&t[i], NULL, barrier_thread, NULL);
	for (int i = 0; i < 4; i++)
		pthread_join(t[i], NULL);
	// One thread of each round is the serial thread with glibc; musl's
	// is of every other round.
	printf("barrier rounds: arrived %d serial %d bad %d\n", atomic_load(&arrived), atomic_load(&serials) >= 5,
	       atomic_load(&bad_rounds));
	pthread_barrier_destroy(&bar);
}

// Spin locks.

static pthread_spinlock_t spin;
static long spin_count;

static void *spinner(void *arg) {
	for (int i = 0; i < 10000; i++) {
		pthread_spin_lock(&spin);
		spin_count++;
		pthread_spin_unlock(&spin);
	}
	return NULL;
}

static void spins(void) {
	pthread_t t[4];
	pthread_spin_init(&spin, PTHREAD_PROCESS_PRIVATE);
	printf("spin lock %d", pthread_spin_lock(&spin));
	printf(" trylock %s", ename(pthread_spin_trylock(&spin)));
	printf(" unlock %d", pthread_spin_unlock(&spin));
	printf(" trylock %d", pthread_spin_trylock(&spin));
	printf(" unlock %d\n", pthread_spin_unlock(&spin));
	for (int i = 0; i < 4; i++)
		pthread_create(&t[i], NULL, spinner, NULL);
	for (int i = 0; i < 4; i++)
		pthread_join(t[i], NULL);
	printf("spinners counted %ld\n", spin_count);
	pthread_spin_destroy(&spin);
}

// Cancellation, cleanup handlers and thread-specific data's destructors.

static sem_t ready, go;
static char trace[256];
static pthread_mutex_t trace_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_key_t key;

static void note(const char *s) {
	pthread_mutex_lock(&trace_mu);
	strcat(trace, s);
	strcat(trace, " ");
	pthread_mutex_unlock(&trace_mu);
}

static void cleanup(void *arg) { note(arg); }

static void destructor(void *arg) { note(arg); }

static void *cancel_testcancel(void *arg) {
	pthread_setspecific(key, "destructor");
	pthread_cleanup_push(cleanup, "first");
	pthread_cleanup_push(cleanup, "second");
	pthread_cleanup_push(cleanup, "popped");
	pthread_cleanup_pop(1);
	sem_post(&ready);
	for (;;) {
		pthread_testcancel();
		sched_yield();
	}
	pthread_cleanup_pop(0);
	pthread_cleanup_pop(0);
	return NULL;
}

static pthread_mutex_t cmu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t ccond = PTHREAD_COND_INITIALIZER;

static void unlock_cmu(void *arg) {
	note("unlock");
	pthread_mutex_unlock(&cmu);
}

static void *cancel_condwait(void *arg) {
	pthread_mutex_lock(&cmu);
	pthread_cleanup_push(unlock_cmu, NULL);
	sem_post(&ready);
	for (;;)
		pthread_cond_wait(&ccond, &cmu);
	pthread_cleanup_pop(1);
	return NULL;
}

static void *cancel_sleep(void *arg) {
	pthread_cleanup_push(cleanup, arg);
	sem_post(&ready);
	sleep(100);
	pthread_cleanup_pop(0);
	return NULL;
}

static void *cancel_nanosleep(void *arg) {
	pthread_cleanup_push(cleanup, arg);
	sem_post(&ready);
	struct timespec ts = {100, 0};
	nanosleep(&ts, NULL);
	pthread_cleanup_pop(0);
	return NULL;
}

static void *cancel_semwait(void *arg) {
	static sem_t never;
	sem_init(&never, 0, 0);
	pthread_cleanup_push(cleanup, arg);
	sem_post(&ready);
	sem_wait(&never);
	pthread_cleanup_pop(0);
	return NULL;
}

static sem_t join_go;

static void *wait_join_go(void *arg) {
	sem_wait(&join_go);
	return arg;
}

static void *cancel_join(void *arg) {
	pthread_cleanup_push(cleanup, "join");
	sem_post(&ready);
	pthread_join(*(pthread_t *)arg, NULL);
	pthread_cleanup_pop(0);
	return NULL;
}

static void *cancel_disabled(void *arg) {
	int old;
	pthread_setcancelstate(PTHREAD_CANCEL_DISABLE, &old);
	note(old == PTHREAD_CANCEL_ENABLE ? "was-enabled" : "?");
	sem_post(&ready);
	sem_wait(&go); // canceled meanwhile: not acted on while disabled
	usleep(1000);
	note("still-running");
	pthread_cleanup_push(cleanup, "enabled");
	pthread_setcancelstate(PTHREAD_CANCEL_ENABLE, &old);
	pthread_testcancel();
	pthread_cleanup_pop(0);
	return NULL;
}

static void *cancel_async(void *arg) {
	int old;
	pthread_cleanup_push(cleanup, "async");
	sem_post(&ready);
	sem_wait(&go);
	pthread_setcanceltype(PTHREAD_CANCEL_ASYNCHRONOUS, &old); // acts at once: canceled meanwhile
	note("not-reached");
	pthread_cleanup_pop(0);
	return NULL;
}

static void *exit_with_handlers(void *arg) {
	pthread_cleanup_push(cleanup, "exit1");
	pthread_cleanup_push(cleanup, "exit2");
	pthread_exit(arg);
	pthread_cleanup_pop(0);
	pthread_cleanup_pop(0);
	return NULL;
}

static void run_cancel(const char *name, void *(*f)(void *), void *arg, int post) {
	pthread_t t;
	void *r;
	trace[0] = 0;
	pthread_create(&t, NULL, f, arg);
	sem_wait(&ready);
	int c = pthread_cancel(t);
	if (post)
		sem_post(&go);
	int j = pthread_join(t, &r);
	printf("%s: cancel %d join %d canceled %d trace: %s\n", name, c, j, r == PTHREAD_CANCELED, trace);
}

static void cancellation(void) {
	sem_init(&ready, 0, 0);
	sem_init(&go, 0, 0);
	pthread_key_create(&key, destructor);
	run_cancel("testcancel", cancel_testcancel, NULL, 0);
	run_cancel("cond_wait", cancel_condwait, NULL, 0);
	printf("mutex unlocked by the handler: trylock %d\n", pthread_mutex_trylock(&cmu));
	pthread_mutex_unlock(&cmu);
	run_cancel("sleep", cancel_sleep, "sleep", 0);
	run_cancel("nanosleep", cancel_nanosleep, "nanosleep", 0);
	run_cancel("sem_wait", cancel_semwait, "sem_wait", 0);
	pthread_t waiter;
	void *r;
	sem_init(&join_go, 0, 0);
	pthread_create(&waiter, NULL, wait_join_go, "joinable");
	run_cancel("join", cancel_join, &waiter, 0);
	sem_post(&join_go);
	int e = pthread_join(waiter, &r);
	printf("still joinable: %d %s\n", e, (char *)r);
	run_cancel("disabled", cancel_disabled, NULL, 1);
	run_cancel("async", cancel_async, NULL, 1);

	pthread_t t;
	trace[0] = 0;
	pthread_create(&t, NULL, exit_with_handlers, "ret");
	pthread_join(t, &r);
	printf("exit: %s trace: %s\n", (char *)r, trace);

	int old;
	printf("setcancelstate bad %s", ename(pthread_setcancelstate(7, &old)));
	e = pthread_setcanceltype(PTHREAD_CANCEL_DEFERRED, &old);
	printf(" setcanceltype %d old %d\n", e, old);
}

// Robust mutexes: a mutex whose owner ended holding it.

static pthread_mutex_t robust1, robust2;

static void *lock_and_end(void *arg) {
	pthread_mutex_lock(arg);
	return NULL;
}

static void robust(void) {
	pthread_mutexattr_t a;
	pthread_t t;
	pthread_mutexattr_init(&a);
	pthread_mutexattr_setrobust(&a, PTHREAD_MUTEX_ROBUST);
	pthread_mutex_init(&robust1, &a);
	pthread_mutex_init(&robust2, &a);
	pthread_mutexattr_destroy(&a);

	pthread_create(&t, NULL, lock_and_end, &robust1);
	pthread_join(t, NULL);
	printf("robust lock %s", ename(pthread_mutex_lock(&robust1)));
	printf(" unlock %d", pthread_mutex_unlock(&robust1));
	printf(" lock %s", ename(pthread_mutex_lock(&robust1)));
	printf(" trylock %s\n", ename(pthread_mutex_trylock(&robust1)));

	pthread_create(&t, NULL, lock_and_end, &robust2);
	pthread_join(t, NULL);
	printf("robust trylock %s", ename(pthread_mutex_trylock(&robust2)));
	printf(" consistent %d", pthread_mutex_consistent(&robust2));
	printf(" unlock %d", pthread_mutex_unlock(&robust2));
	printf(" lock %d", pthread_mutex_lock(&robust2));
	printf(" unlock %d\n", pthread_mutex_unlock(&robust2));
}

// Condition variables timing out on CLOCK_MONOTONIC.

static void condclock(void) {
	pthread_condattr_t ca;
	pthread_cond_t c;
	pthread_mutex_t m = PTHREAD_MUTEX_INITIALIZER;
	pthread_condattr_init(&ca);
	pthread_condattr_setclock(&ca, CLOCK_MONOTONIC);
	pthread_cond_init(&c, &ca);
	pthread_condattr_destroy(&ca);
	pthread_mutex_lock(&m);
	long long t0 = nsnow(CLOCK_MONOTONIC);
	struct timespec ts = after(CLOCK_MONOTONIC, 50);
	int e = pthread_cond_timedwait(&c, &m, &ts);
	long long dt = nsnow(CLOCK_MONOTONIC) - t0;
	printf("monotonic timedwait %s waited %d\n", ename(e), dt >= 50000000 && dt < 5000000000LL);
	// A monotonic time at the clock's start is past.
	ts.tv_sec = 0;
	ts.tv_nsec = 1000;
	printf("past timedwait %s\n", ename(pthread_cond_timedwait(&c, &m, &ts)));
	pthread_mutex_unlock(&m);
	pthread_cond_destroy(&c);

	pthread_cond_init(&c, NULL);
	pthread_mutex_lock(&m);
	t0 = nsnow(CLOCK_MONOTONIC);
	ts = after(CLOCK_REALTIME, 30);
	e = pthread_cond_timedwait(&c, &m, &ts);
	dt = nsnow(CLOCK_MONOTONIC) - t0;
	printf("realtime timedwait %s waited %d\n", ename(e), dt >= 25000000 && dt < 5000000000LL);
	pthread_mutex_unlock(&m);
	pthread_cond_destroy(&c);
}

// Timers.

static atomic_int sig_count, sig_value, sig_code_ok;

static void on_timer(int sig, siginfo_t *si, void *ctx) {
	atomic_fetch_add(&sig_count, 1);
	atomic_store(&sig_value, si->si_value.sival_int);
	atomic_store(&sig_code_ok, si->si_code == SI_TIMER);
}

static atomic_int thread_count, thread_value;

static void on_thread(union sigval v) {
	atomic_fetch_add(&thread_count, 1);
	atomic_store(&thread_value, v.sival_int);
}

static void wait_for(atomic_int *n, int want) {
	for (int i = 0; i < 5000 && atomic_load(n) < want; i++)
		usleep(1000);
}

static void timers(void) {
	struct sigaction sa = {0};
	sa.sa_sigaction = on_timer;
	sa.sa_flags = SA_SIGINFO;
	sigemptyset(&sa.sa_mask);
	sigaction(SIGRTMIN, &sa, NULL);

	struct sigevent ev = {0};
	ev.sigev_notify = SIGEV_SIGNAL;
	ev.sigev_signo = SIGRTMIN;
	ev.sigev_value.sival_int = 42;
	timer_t tm;
	printf("timer_create %d\n", timer_create(CLOCK_MONOTONIC, &ev, &tm));
	struct itimerspec its = {{0, 10000000}, {0, 10000000}}, old;
	printf("timer_settime %d\n", timer_settime(tm, 0, &its, NULL));
	wait_for(&sig_count, 3);
	printf("signals >= 3 %d value %d code SI_TIMER %d\n", atomic_load(&sig_count) >= 3, atomic_load(&sig_value),
	       atomic_load(&sig_code_ok));
	its.it_value.tv_sec = 10;
	its.it_value.tv_nsec = 0;
	its.it_interval.tv_sec = 5;
	its.it_interval.tv_nsec = 0;
	timer_settime(tm, 0, &its, &old);
	printf("old interval %ld.%09ld\n", (long)old.it_interval.tv_sec, old.it_interval.tv_nsec);
	timer_gettime(tm, &its);
	printf("gettime value %d interval %ld\n", its.it_value.tv_sec >= 9 && its.it_value.tv_sec < 10,
	       (long)its.it_interval.tv_sec);
	memset(&its, 0, sizeof its);
	timer_settime(tm, 0, &its, NULL);
	timer_gettime(tm, &its);
	printf("disarmed %ld %ld\n", (long)its.it_value.tv_sec, its.it_value.tv_nsec);
	printf("timer_delete %d\n", timer_delete(tm));

	// An absolute time on CLOCK_MONOTONIC.
	atomic_store(&sig_count, 0);
	timer_create(CLOCK_MONOTONIC, &ev, &tm);
	memset(&its, 0, sizeof its);
	its.it_value = after(CLOCK_MONOTONIC, 20);
	long long t0 = nsnow(CLOCK_MONOTONIC);
	printf("abstime settime %d", timer_settime(tm, TIMER_ABSTIME, &its, NULL));
	wait_for(&sig_count, 1);
	long long dt = nsnow(CLOCK_MONOTONIC) - t0;
	printf(" fired %d after %d\n", atomic_load(&sig_count), dt >= 15000000);
	timer_delete(tm);

	// Overruns: the signal stays pending (blocked) while the timer
	// expires again.
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGRTMIN + 1);
	sigprocmask(SIG_BLOCK, &set, NULL);
	ev.sigev_signo = SIGRTMIN + 1;
	ev.sigev_value.sival_int = 7;
	timer_create(CLOCK_REALTIME, &ev, &tm);
	struct itimerspec fast = {{0, 5000000}, {0, 5000000}};
	timer_settime(tm, 0, &fast, NULL);
	usleep(100000);
	siginfo_t si;
	int sig = sigwaitinfo(&set, &si);
	int over = timer_getoverrun(tm);
	timer_delete(tm);
	printf("sigwaitinfo %d value %d code %d overrun > 5 %d\n", sig == SIGRTMIN + 1, si.si_value.sival_int,
	       si.si_code == SI_TIMER, over > 5);

	// SIGEV_THREAD.
	memset(&ev, 0, sizeof ev);
	ev.sigev_notify = SIGEV_THREAD;
	ev.sigev_notify_function = on_thread;
	ev.sigev_value.sival_int = 99;
	printf("thread timer %d", timer_create(CLOCK_REALTIME, &ev, &tm));
	its = (struct itimerspec){{0, 10000000}, {0, 10000000}};
	timer_settime(tm, 0, &its, NULL);
	wait_for(&thread_count, 3);
	timer_delete(tm);
	printf(" calls >= 3 %d value %d\n", atomic_load(&thread_count) >= 3, atomic_load(&thread_value));

	// SIGEV_NONE: nothing happens, but the time passes.
	memset(&ev, 0, sizeof ev);
	ev.sigev_notify = SIGEV_NONE;
	timer_create(CLOCK_MONOTONIC, &ev, &tm);
	its = (struct itimerspec){{0, 0}, {5, 0}};
	timer_settime(tm, 0, &its, NULL);
	usleep(20000);
	timer_gettime(tm, &its);
	printf("none: left < 5s %d\n", its.it_value.tv_sec < 5 && its.it_value.tv_sec >= 4);
	timer_delete(tm);

	ev.sigev_notify = 99;
	errno = 0;
	int r = timer_create(CLOCK_MONOTONIC, &ev, &tm);
	printf("bad notify %d %s\n", r, ename(errno));
	ev.sigev_notify = SIGEV_NONE;
	errno = 0;
	r = timer_create(12345, &ev, &tm);
	printf("bad clock %d %s\n", r, ename(errno));
}

static void clocks(void) {
	struct timespec ts = after(CLOCK_MONOTONIC, 30);
	long long target = ts.tv_sec * 1000000000LL + ts.tv_nsec;
	int e = clock_nanosleep(CLOCK_MONOTONIC, TIMER_ABSTIME, &ts, NULL);
	printf("clock_nanosleep abs %d reached %d\n", e, nsnow(CLOCK_MONOTONIC) >= target);
	ts = after(CLOCK_REALTIME, 10);
	target = ts.tv_sec * 1000000000LL + ts.tv_nsec;
	e = clock_nanosleep(CLOCK_REALTIME, TIMER_ABSTIME, &ts, NULL);
	printf("clock_nanosleep realtime abs %d reached %d\n", e, nsnow(CLOCK_REALTIME) >= target);
	struct timespec rel = {0, 10000000};
	long long t0 = nsnow(CLOCK_MONOTONIC);
	e = clock_nanosleep(CLOCK_MONOTONIC, 0, &rel, NULL);
	printf("clock_nanosleep rel %d slept %d\n", e, nsnow(CLOCK_MONOTONIC) - t0 >= 10000000);
	ts.tv_sec = 0; // past
	ts.tv_nsec = 1000;
	printf("clock_nanosleep past %d\n", clock_nanosleep(CLOCK_MONOTONIC, TIMER_ABSTIME, &ts, NULL));
	rel.tv_nsec = 2000000000;
	printf("clock_nanosleep bad %s\n", ename(clock_nanosleep(CLOCK_MONOTONIC, 0, &rel, NULL)));
	rel.tv_nsec = 1000;
	printf("clock_nanosleep thread clock %s\n", ename(clock_nanosleep(CLOCK_THREAD_CPUTIME_ID, 0, &rel, NULL)));
}

// Signals.

static volatile sig_atomic_t usr2, usr1;

static void on_usr2(int sig) { usr2++; }

static void on_usr1(int sig) { usr1++; }

static int blocked(int sig) {
	sigset_t s;
	sigprocmask(SIG_BLOCK, NULL, &s);
	return sigismember(&s, sig);
}

static void signals(void) {
	signal(SIGUSR2, on_usr2);
	setpgid(0, 0); // a group of its own (unless it leads one already), which killpg signals
	printf("killpg %d", killpg(getpgrp(), SIGUSR2));
	printf(" handled %d", usr2);
	printf(" killpg 0 %d", killpg(0, SIGUSR2));
	printf(" handled %d", usr2);
	errno = 0;
	int r = killpg(-1, SIGUSR2);
	printf(" negative %d %s\n", r, ename(errno));

	printf("sighold %d", sighold(SIGUSR1));
	printf(" blocked %d", blocked(SIGUSR1));
	printf(" sigrelse %d", sigrelse(SIGUSR1));
	printf(" blocked %d\n", blocked(SIGUSR1));
	void (*h)(int) = sigset(SIGUSR1, on_usr1);
	printf("sigset old dfl %d", h == SIG_DFL);
	raise(SIGUSR1);
	printf(" handled %d", usr1);
	h = sigset(SIGUSR1, SIG_HOLD);
	printf(" hold: old handler %d", h == on_usr1);
	printf(" blocked %d", blocked(SIGUSR1));
	raise(SIGUSR1);
	printf(" handled %d", usr1);
	h = sigset(SIGUSR1, on_usr1);
	printf(" set: old hold %d handled %d", h == SIG_HOLD, usr1);
	printf(" blocked %d\n", blocked(SIGUSR1));
	printf("sigignore %d", sigignore(SIGUSR1));
	raise(SIGUSR1);
	printf(" handled %d", usr1);
	errno = 0;
	r = sigignore(SIGKILL);
	printf(" sigignore SIGKILL %d %s\n", r, ename(errno));
	signal(SIGUSR1, SIG_DFL);

	// sigsetjmp(env, 1) saves the mask, which siglongjmp restores.
	static sigjmp_buf env;
	static volatile int jumps;
	jumps = 0;
	if (sigsetjmp(env, 1) == 0) {
		sighold(SIGUSR1);
		printf("before siglongjmp blocked %d", blocked(SIGUSR1));
		jumps++;
		siglongjmp(env, 1);
	}
	printf(" after: blocked %d", blocked(SIGUSR1));
	printf(" jumps %d\n", jumps);
	// sigsetjmp(env, 0) does not.
	if (sigsetjmp(env, 0) == 0) {
		sighold(SIGUSR1);
		siglongjmp(env, 2);
	}
	printf("no mask saved: blocked %d\n", blocked(SIGUSR1));
	sigrelse(SIGUSR1);
	jmp_buf jb;
	r = _setjmp(jb);
	if (r == 0)
		_longjmp(jb, 5);
	printf("_longjmp %d\n", r);
}

int main(void) {
	setvbuf(stdout, NULL, _IOLBF, 0);
	attrs();
	threads();
	rwlocks();
	barriers();
	spins();
	cancellation();
	robust();
	condclock();
	timers();
	clocks();
	signals();
	return 0;
}
