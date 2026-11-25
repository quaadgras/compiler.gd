// intptr_t and uintptr_t carry a pointer's capability (CHERI C's __intcap):
// a pointer converted to one and back is the pointer. The output must match
// gcc's, which needs no capabilities for any of it.
#include <inttypes.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

struct node {
	uintptr_t link; // a pointer, its low bit a flag
	int v;
};

static uintptr_t sum(int n, ...) {
	va_list ap;
	va_start(ap, n);
	uintptr_t s = 0;
	for (int i = 0; i < n; i++) {
		s += va_arg(ap, uintptr_t);
	}
	va_end(ap);
	return s;
}

static int *global;
static uintptr_t gaddr = 0;

int main(void) {
	int x = 42;
	uintptr_t u = (uintptr_t)&x;
	int *p = (int *)u;
	printf("round trip %d %d\n", *p, p == &x);

	// Alignment: the result is still buf's.
	char buf[64];
	char *a = (char *)(((uintptr_t)buf + 15) & ~(uintptr_t)15);
	a[0] = 'x';
	printf("aligned %d %d\n", (int)((uintptr_t)a % 16), a >= buf && a < buf + 16);

	// intptr_t: signed differences and comparisons.
	int arr[8] = {0, 1, 2, 3, 4, 5, 6, 7};
	intptr_t d = (intptr_t)&arr[2] - (intptr_t)&arr[6];
	printf("diff %d %d\n", (int)d, d < 0);
	int *q = (int *)((intptr_t)&arr[6] + d);
	printf("back %d\n", *q);

	// A tagged pointer in a struct field, through memory.
	struct node n2 = {0, 7}, n1 = {(uintptr_t)&n2 | 1, 5};
	struct node *next = (struct node *)(n1.link & ~(uintptr_t)1);
	printf("tagged %d %d\n", (int)(n1.link & 1), next->v);

	// An array of them, ++, shifts, compound assignment.
	uintptr_t us[3] = {(uintptr_t)&arr[0], (uintptr_t)&arr[1], (uintptr_t)&arr[2]};
	us[0] += sizeof(int);
	us[1]++;
	us[1]--;
	uintptr_t w = us[2];
	uintptr_t sh = 6; // (a shift of one that is a pointer gives an integer: of an integer here)
	sh <<= 1;
	sh >>= 1;
	w += sh - 6;
	printf("array %d %d %d\n", *(int *)us[0], *(int *)us[1], *(int *)w);

	// Hashing, switch, truth, plain integers.
	uintptr_t h = (uintptr_t)p % 4;
	switch ((uintptr_t)p & 3) {
	case 0:
		printf("switch aligned %d\n", h == 0);
		break;
	default:
		printf("switch unaligned\n");
	}
	uintptr_t z = 0, big = UINTPTR_MAX;
	printf("plain %d %d %d %d\n", !z, big == (uintptr_t)-1, (int)(uintptr_t)3.75, (unsigned char)(uintptr_t)300);

	// Integers written into one as bytes, and its bytes read as one.
	unsigned long l = 12345;
	uintptr_t m;
	memcpy(&m, &l, sizeof m);
	unsigned long l2;
	memcpy(&l2, &m, sizeof l2);
	printf("bytes %lu %lu\n", (unsigned long)m, l2);

	// printf, va_arg, atomics, a global.
	printf("printf %" PRIuPTR " %" PRIxPTR "\n", (uintptr_t)10, (uintptr_t)255);
	printf("va_arg %lu\n", (unsigned long)sum(3, (uintptr_t)1, (uintptr_t)2, (uintptr_t)3));
	_Atomic uintptr_t au = (uintptr_t)&x;
	printf("atomic %d\n", *(int *)atomic_load(&au));
	gaddr = (uintptr_t)&x;
	global = (int *)gaddr;
	printf("global %d\n", *global);

	// The usual conversions keep intptr_t's capability; _Generic tells it apart.
	long off = 2 * (long)sizeof(int);
	int *r = (int *)((uintptr_t)&arr[1] + off);
	printf("usual %d %s\n", *r, _Generic((uintptr_t)0 + 1L, uintptr_t: "uintptr_t", default: "other"));
	return 0;
}
