#include <cstdio>
#include <cstddef>
template <typename T>
struct Span {
	const T *p = nullptr; size_t n = 0;
	constexpr Span() = default;
	Span(const T *a, size_t len) : p(a), n(len) {}
	template <size_t N>
	constexpr Span(const T (&a)[N]) : p(a), n(N) {}
	size_t size() const { return n; }
	const T &operator[](size_t i) const { return p[i]; }
};
static long sum(const Span<long> &s) { long t = 0; for (size_t i = 0; i < s.size(); i++) t += s[i]; return t; }
template <typename... A>
long total(A... a) {
	long v[sizeof...(a)] = { a... };
	return sum(Span(v));
}
int main() {
	double d[3] = {1.5, 2.5, 3};
	Span s(d);
	Span t(d, 2);
	printf("%zu %zu %g\n", s.size(), t.size(), s[2]);
	printf("%ld\n", total(1, 2L, 3));
}
