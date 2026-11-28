// A struct that ends with an "unlimited" array, allocated with as much
// of the array as is used.
#include <stdio.h>
#include <stdlib.h>
#include <stddef.h>

struct match {
	void *(*alloc)(size_t);
	const char *subject;
	unsigned short count;
	size_t ovector[131072];
};

static match *create(unsigned n) {
	match *m = (match *)malloc(offsetof(match, ovector) + 2 * n * sizeof(size_t));
	m->alloc = malloc;
	m->subject = "abc";
	m->count = n;
	for (unsigned i = 0; i < 2 * n; i++)
		m->ovector[i] = i * 3;
	return m;
}

int main() {
	match *m = create(3);
	size_t *v = m->ovector, sum = 0;
	for (int i = 0; i < 2 * m->count; i++)
		sum += v[i] + m->ovector[i];
	printf("%s %d %zu %zu\n", m->subject, m->count, sum, m->ovector[5]);
	free(m);
	static match whole;
	whole.ovector[131071] = 7;
	match *w = &whole;
	printf("%zu\n", w->ovector[131071]);
	return 0;
}
