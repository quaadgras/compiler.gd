// NULL as a pointer of any type, a pointer to arrays as one to arrays
// of const, and the language's allocation functions by name.
#include <stdio.h>
#include <stddef.h>
#include <new>

struct Storage {
	void *(*allocate)(size_t);
	void (*deallocate)(void *);
};
static Storage &storage() {
	static Storage s = {::operator new, ::operator delete};
	return s;
}
static int sum(const unsigned int buffer[][3], unsigned int n, const unsigned int *remap) {
	int t = 0;
	for (unsigned int i = 0; i < n; i++)
		for (int j = 0; j < 3; j++)
			t += remap ? remap[buffer[i][j]] : buffer[i][j];
	return t;
}
int main() {
	unsigned int buffer[2][3] = {{1, 2, 3}, {0, 1, 2}};
	unsigned int remap[4] = {10, 20, 30, 40};
	int *p = (int *)storage().allocate(sizeof(int));
	*p = sum(buffer, 2, NULL);
	printf("%d %d\n", *p, sum(buffer, 2, remap));
	storage().deallocate(p);
	return 0;
}
