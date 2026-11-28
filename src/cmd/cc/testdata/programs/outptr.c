// A pointer that arithmetic takes out of its object is one that the
// program may keep and compare: a limit that is never reached.
#include <stdio.h>
#include <stdlib.h>

static long to_int(const char *s, int len) {
	const char *limit = &s[len]; // len is -1 for "to the terminator"
	long n = 0;
	while (*s && s != limit)
		n = n * 10 + (*s++ - '0');
	return n;
}

int main(void) {
	char *buf = malloc(8);
	for (int i = 0; i < 7; i++)
		buf[i] = '1' + i;
	buf[7] = 0;
	printf("%ld %ld\n", to_int(buf, -1), to_int(buf, 3));
	int a[4] = {1, 2, 3, 4}, sum = 0;
	int *before = a - 1;
	for (int *p = a + 3; p != before; p--)
		sum += *p;
	printf("%d\n", sum);
	free(buf);
	return 0;
}
