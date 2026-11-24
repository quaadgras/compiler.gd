#include <cstdio>
typedef int Error;
template <typename T>
class Cow {
	T *ptr = nullptr; long n = 0, cap = 0;
	Error _alloc_exact(unsigned long c);
	[[nodiscard]] Error _copy(unsigned long c, unsigned long from);
public:
	Error push_back(T &&v);
	Error reserve(unsigned long c);
	long size() const { return n; }
	const T &get(long i) const { return ptr[i]; }
};
template <typename T>
Error Cow<T>::push_back(T &&v) {
	if (!ptr) {
		Error e0 = _alloc_exact(4); if (e0) return e0;
	} else if (n == cap) {
		const Error e = _copy(cap * 2, n);
		if (e) return e;
	}
	ptr[n++] = v;
	return 0;
}
template <typename T>
Error Cow<T>::reserve(unsigned long c) { if (!ptr) return _alloc_exact(c); return _copy(c, n); }
template <typename T>
Error Cow<T>::_alloc_exact(unsigned long c) { ptr = new T[c]; cap = c; return 0; }
template <typename T>
Error Cow<T>::_copy(unsigned long c, unsigned long from) {
	T *np = new T[c];
	for (unsigned long i = 0; i < from; i++) np[i] = ptr[i];
	ptr = np; cap = c;
	return 0;
}
template <typename T> struct Vec {
	Cow<T> c;
	bool push_back(T v) { return c.push_back(static_cast<T &&>(v)) == 0; }
	long size() const { return c.size(); }
};
struct Tri { int a, b; };
int main() {
	Vec<int> v; Vec<Tri> t;
	for (int i = 0; i < 9; i++) { v.push_back(i * i); t.push_back(Tri{i, -i}); }
	printf("%ld %d %ld %d\n", v.size(), v.c.get(8), t.size(), t.c.get(5).b);
}
