#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
static void swap_ends(unsigned long *a, size_t n) {
	unsigned long *l, *r;
	if (n == 0)
		return;
	for (l = a, r = a + (n - 1); l <= r; l++, r--) {
		unsigned long t = *l;
		*l = *r;
		*r = t;
	}
}
int main(void) {
	unsigned long *a = malloc(3 * sizeof *a), *one = malloc(sizeof *one);
	a[0] = 1, a[1] = 2, a[2] = 3, one[0] = 9;
	swap_ends(a, 3);
	swap_ends(one, 1);
	unsigned long *before = one - 1, *end = one + 1;
	printf("%lu %lu %lu %d %d %d %ld %d\n", a[0], a[2], one[0], before < one, end > one, before < end, (long)(end - before), (uintptr_t)before + sizeof *one == (uintptr_t)one);
	free(a);
	free(one);
	return 0;
}
