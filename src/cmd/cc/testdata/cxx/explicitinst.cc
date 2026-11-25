#include <cstdio>
#include <cstdlib>
#include <cstring>
namespace Mem {
int count = 0;
template <bool p_zero = false> void *alloc(size_t n, bool pad = false);
inline void *alloc_zeroed(size_t n) { return alloc<true>(n); }
}
template <typename T> T twice(T x);
template <bool p_zero>
void *Mem::alloc(size_t n, bool pad) {
	count++;
	void *p = malloc(n + (pad ? 16 : 0));
	if (p_zero) memset(p, 0, n);
	return p;
}
template <typename T> T twice(T x) { return x + x; }
template void *Mem::alloc<true>(size_t n, bool pad);
template void *Mem::alloc<false>(size_t n, bool pad);
template int twice<int>(int);
int main() {
	int *z = (int *)Mem::alloc_zeroed(4 * sizeof(int));
	int *u = (int *)Mem::alloc<false>(sizeof(int));
	*u = 5;
	printf("%d %d %d %d\n", z[0] + z[3], *u, Mem::count, twice(21));
}
