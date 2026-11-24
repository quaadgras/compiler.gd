#include <cstdio>
#include <utility>
struct Buf {
	int *p; int n;
	Buf(int k) : p(new int[k]), n(k) { p[0] = k; }
	Buf(const Buf &o) : p(new int[o.n]), n(o.n) { p[0] = o.p[0]; printf("copy %d\n", n); }
	Buf(Buf &&o) : p(o.p), n(o.n) { o.p = nullptr; o.n = 0; printf("move %d\n", n); }
	Buf &operator=(const Buf &o) { n = o.n; p = new int[n]; p[0] = o.p[0]; printf("copy= %d\n", n); return *this; }
	Buf &operator=(Buf &&o) { p = o.p; n = o.n; o.p = nullptr; o.n = 0; printf("move= %d\n", n); return *this; }
};
template <typename T> struct Vec {
	Buf data; T tag;
	Vec(int k, T t) : data(k), tag(t) {}
	Vec(const Vec &o) = default;
	Vec(Vec &&o) = default;
	Vec &operator=(const Vec &o) = default;
	Vec &operator=(Vec &&o) = default;
};
struct Plain { int a; double b; Plain(int x) : a(x), b(x * 0.5) {} Plain(Plain &&) = default; Plain &operator=(Plain &&) = default; };
static Vec<char> make(int k) { Vec<char> v(k, 'm'); return v; }
int main() {
	Vec<int> a(3, 7);
	Vec<int> b(a);
	Vec<int> c(std::move(a));
	b = c;
	b = std::move(c);
	Vec<char> d = make(5);
	Vec<char> e(2, 'e');
	e = make(6);
	Plain p(4); Plain q(std::move(p)); p = Plain(9);
	printf("%d %d %d %d %c %d %g %d\n", a.data.n, b.data.n, c.data.n, d.data.n, e.tag, e.data.n, q.b, p.a);
}
