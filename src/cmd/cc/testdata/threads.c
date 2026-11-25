// <threads.h>, and C23 library functions cmd/cc lacked before: the
// float complex functions, iseqsig, and stdatomic.h's functions as
// functions, and errno in threads. TestThreads diffs the output with
// gcc's (and musl's).

#include <complex.h>
#include <errno.h>
#include <math.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <threads.h>
#include <time.h>

#ifndef iseqsig
#define iseqsig(x, y) __builtin_iseqsig(x, y)
#endif

static int square(void *arg) { return *(int *)arg * *(int *)arg; }

static int exits(void *arg) {
	thrd_exit(42);
	return 0;
}

enum { N = 8, ROUNDS = 1000 };

static mtx_t mu;
static long counter;

static int add(void *arg) {
	for (int i = 0; i < ROUNDS; i++) {
		mtx_lock(&mu);
		counter++;
		mtx_unlock(&mu);
	}
	return 0;
}

static int try_locked(void *arg) { return mtx_trylock(&mu); }

static struct timespec after(long ms) {
	struct timespec ts;
	timespec_get(&ts, TIME_UTC);
	ts.tv_nsec += ms * 1000000;
	ts.tv_sec += ts.tv_nsec / 1000000000;
	ts.tv_nsec %= 1000000000;
	return ts;
}

static int timed_lock(void *arg) {
	struct timespec ts = after(20);
	return mtx_timedlock(&mu, &ts);
}

// A queue of one item, with a condition variable for each side.
static cnd_t nonempty, nonfull;
static int item, full;

static int produce(void *arg) {
	for (int i = 1; i <= 100; i++) {
		mtx_lock(&mu);
		while (full)
			cnd_wait(&nonfull, &mu);
		item = i;
		full = 1;
		cnd_signal(&nonempty);
		mtx_unlock(&mu);
	}
	return 0;
}

static tss_t key;
static int destroyed;

static void dtor(void *p) {
	mtx_lock(&mu);
	destroyed += *(int *)p;
	mtx_unlock(&mu);
	free(p);
}

static int use_tss(void *arg) {
	int *p = malloc(sizeof *p);
	*p = *(int *)arg;
	tss_set(key, p);
	return *(int *)tss_get(key) * 10;
}

static once_flag once = ONCE_FLAG_INIT;
static int inits;
static void init(void) { inits++; }

static int call(void *arg) {
	call_once(&once, init);
	return 0;
}

static thrd_t main_thread;
static int is_main(void *arg) { return thrd_equal(thrd_current(), main_thread); }

static int sleeper(void *arg) {
	struct timespec d = {0, 1000000};
	return thrd_sleep(&d, NULL);
}

static void threads(void) {
	thrd_t t;
	int arg = 7, res = -1;
	printf("create %d", thrd_create(&t, square, &arg) == thrd_success);
	int ok = thrd_join(t, &res) == thrd_success;
	printf(", join %d, res %d\n", ok, res);
	thrd_create(&t, exits, NULL);
	thrd_join(t, &res);
	printf("thrd_exit %d\n", res);

	mtx_init(&mu, mtx_plain);
	thrd_t ts[N];
	for (int i = 0; i < N; i++)
		thrd_create(&ts[i], add, NULL);
	for (int i = 0; i < N; i++)
		thrd_join(ts[i], NULL);
	printf("counter %ld\n", counter);

	mtx_lock(&mu);
	thrd_create(&t, try_locked, NULL);
	thrd_join(t, &res);
	printf("trylock busy %d\n", res == thrd_busy);
	thrd_create(&t, timed_lock, NULL);
	thrd_join(t, &res);
	printf("timedlock timedout %d\n", res == thrd_timedout);
	struct timespec ts1 = after(10);
	cnd_t never;
	cnd_init(&never);
	printf("cnd_timedwait timedout %d\n", cnd_timedwait(&never, &mu, &ts1) == thrd_timedout);
	cnd_destroy(&never);
	mtx_unlock(&mu);
	printf("trylock free %d\n", mtx_trylock(&mu) == thrd_success);
	mtx_unlock(&mu);
	mtx_destroy(&mu);

	mtx_t rec;
	mtx_init(&rec, mtx_recursive | mtx_timed);
	printf("recursive %d", mtx_lock(&rec) == thrd_success);
	printf(" %d", mtx_lock(&rec) == thrd_success);
	printf(" %d", mtx_trylock(&rec) == thrd_success);
	mtx_unlock(&rec);
	mtx_unlock(&rec);
	printf(" %d\n", mtx_unlock(&rec) == thrd_success);
	mtx_destroy(&rec);

	mtx_init(&mu, mtx_plain);
	cnd_init(&nonempty);
	cnd_init(&nonfull);
	thrd_create(&t, produce, NULL);
	long sum = 0;
	for (int i = 0; i < 100; i++) {
		mtx_lock(&mu);
		while (!full)
			cnd_wait(&nonempty, &mu);
		sum += item;
		full = 0;
		cnd_broadcast(&nonfull);
		mtx_unlock(&mu);
	}
	thrd_join(t, NULL);
	printf("consumed %ld\n", sum);

	printf("tss_create %d\n", tss_create(&key, dtor) == thrd_success);
	int vals[3] = {1, 2, 3};
	int got = 0;
	for (int i = 0; i < 3; i++) {
		thrd_create(&ts[i], use_tss, &vals[i]);
	}
	for (int i = 0; i < 3; i++) {
		thrd_join(ts[i], &res);
		got += res;
	}
	printf("tss %d, destroyed %d, main's %d\n", got, destroyed, tss_get(key) == NULL);
	tss_delete(key);

	for (int i = 0; i < N; i++)
		thrd_create(&ts[i], call, NULL);
	for (int i = 0; i < N; i++)
		thrd_join(ts[i], NULL);
	printf("call_once %d\n", inits);

	main_thread = thrd_current();
	thrd_create(&t, is_main, NULL);
	thrd_join(t, &res);
	printf("equal self %d, other %d\n", thrd_equal(main_thread, thrd_current()) != 0, res);
	thrd_create(&t, sleeper, NULL);
	thrd_join(t, &res);
	printf("sleep %d\n", res);
	thrd_create(&t, sleeper, NULL);
	printf("detach %d\n", thrd_detach(t) == thrd_success);
	thrd_yield();
	mtx_destroy(&mu);
}

static void library(void) {
	float complex z = 0.5f + 0.25f * I;
	float complex (*fs[])(float complex) = {
		cacosf, casinf, catanf, ctanf, csinhf, ccoshf, ctanhf, cacoshf, casinhf, catanhf, cprojf,
	};
	for (unsigned i = 0; i < sizeof fs / sizeof fs[0]; i++) {
		float complex r = fs[i](z);
		printf("%.5f%+.5fi ", crealf(r), cimagf(r));
	}
	printf("\n");

	printf("iseqsig %d %d %d\n", iseqsig(1.0, 1.0), iseqsig(1.0f, 2.0f), iseqsig(1.0, NAN));

	atomic_flag f = ATOMIC_FLAG_INIT;
	bool (*tas)(volatile atomic_flag *) = atomic_flag_test_and_set;
	void (*clear)(volatile atomic_flag *) = atomic_flag_clear;
	void (*fence)(memory_order) = atomic_thread_fence;
	printf("flag %d", tas(&f));
	printf(" %d", (atomic_flag_test_and_set)(&f));
	clear(&f);
	fence(memory_order_seq_cst);
	(atomic_signal_fence)(memory_order_seq_cst);
	printf(" %d", atomic_flag_test_and_set_explicit(&f, memory_order_acquire));
	atomic_flag_clear_explicit(&f, memory_order_release);
	printf(" %d\n", atomic_flag_test_and_set(&f));
}

// errno is each thread's own: a new thread's is 0, and what one thread
// sets another does not see.

static atomic_int step;

static void await(int n) {
	while (atomic_load(&step) < n)
		thrd_yield();
}

static int errno_first(void *arg) {
	int was = errno;
	errno = EDOM;
	atomic_store(&step, 1);
	await(2); // errno_second has set its own
	int *res = arg;
	res[0] = was;
	res[1] = errno == EDOM;
	return 0;
}

static int errno_second(void *arg) {
	await(1);
	int was = errno;
	errno = ERANGE;
	atomic_store(&step, 2);
	await(3); // main has read its own
	int *res = arg;
	res[0] = was;
	res[1] = errno == ERANGE;
	return 0;
}

static int *main_errno;

static int errno_addr(void *arg) { return &errno != main_errno; }

static void errnos(void) {
	thrd_t a, b;
	int ra[2] = {-1, -1}, rb[2] = {-1, -1}, r;
	errno = ENOENT;
	thrd_create(&a, errno_first, ra);
	thrd_create(&b, errno_second, rb);
	await(2);
	printf("errno main %d", errno == ENOENT);
	atomic_store(&step, 3);
	thrd_join(a, NULL);
	thrd_join(b, NULL);
	printf(" new %d %d kept %d %d main %d", ra[0], rb[0], ra[1], rb[1], errno == ENOENT);
	main_errno = &errno;
	thrd_create(&a, errno_addr, NULL);
	thrd_join(a, &r);
	printf(" own %d\n", r);
}

int main(void) {
	threads();
	errnos();
	library();
	return 0;
}
