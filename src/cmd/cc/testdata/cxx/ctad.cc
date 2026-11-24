// Class template argument deduction, and a class value bound to a reference.
#include <cstdio>
#include <cstring>
template <class T>
struct Span {
	const T *p;
	unsigned long n;
	constexpr Span() : p(nullptr), n(0) {}
	constexpr Span(const T *p, unsigned long n) : p(p), n(n) {}
	unsigned long size() const { return n; }
};
struct Q {
	float x;
	Q(float x) : x(x) {}
	Q(const Q &o) : x(o.x) { copies++; }
	static int copies;
};
int Q::copies = 0;
float take(const Q &q) { return q.x; }
unsigned long len(Span<char> s) { return s.size(); }
int main() {
	const char *s = "hello";
	int v[3] = {1, 2, 3};
	bool c = s[0] == 'h';
	Q a(1), b(2);
	printf("%lu %lu %lu %g %d\n", len(Span(s, strlen(s))), Span(v, 3).size(), sizeof(Span(v, 3)), take(c ? a : Q(5)), Q::copies);
	return 0;
}
