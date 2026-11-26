// constexpr functions called with constants: the values are constants,
// and the static members they initialize are not set at run time.
#include <stdio.h>
#include <stddef.h>
namespace M {
constexpr size_t align(size_t p, size_t a) {
	const size_t n = p % a;
	return (n == 0) ? p : (p + a - n);
}
constexpr int fact(int n) { int r = 1; for (int i = 2; i <= n; i++) r *= i; return r; }
constexpr int fib(int n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
inline constexpr size_t A = align(9, 8);
}
template <class T> struct Cow {
	static constexpr size_t REF = 0;
	static constexpr size_t SIZE = M::align(REF + sizeof(int), alignof(size_t));
	static constexpr size_t DATA = M::align(SIZE + sizeof(size_t), 16);
	T *p;
	size_t size() const { return *(size_t *)((char *)p - DATA + SIZE); }
};
static_assert(M::fact(5) == 120, "fact");
int arr[M::fib(10)];
int main() {
	printf("%zu %zu %zu %d %zu\n", M::A, Cow<int>::SIZE, Cow<int>::DATA, M::fact(6), sizeof arr / sizeof arr[0]);
	char buf[64] = {0};
	*(size_t *)(buf + 8) = 42;
	Cow<int> c{(int *)(buf + 16)};
	printf("%zu\n", c.size());
	int x = 7;
	printf("%d\n", M::fact(x));
}
