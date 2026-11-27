// With explicit template arguments, an argument converts to the
// parameter's type.
#include <stdio.h>

template <class T>
struct Vec {
	T first;
	int n;
};

struct Any {
	double d;
	operator double() const { return d; }
	operator Vec<int>() const { return Vec<int>{(int)d, 1}; }
	operator Vec<double>() const { return Vec<double>{d, 2}; }
};

template <class T>
static void item(const T &v, float *out) { *out = (float)v; }

template <class T>
static Vec<T> remap(Vec<T> a, const Vec<unsigned> &r, unsigned count) {
	a.n += r.n + count;
	return a;
}

template <class T, class U>
static T both(T a, U b) { return a + (T)b; }

int main() {
	Any a{2.5};
	float f = 0;
	item<double>(a, &f);
	Vec<unsigned> r{0, 10};
	Vec<int> vi = remap<int>(a, r, 100);
	Vec<double> vd = remap<double>(a, r, 200);
	printf("%g %d %d %g %d %g\n", f, vi.first, vi.n, vd.first, vd.n, both<double>(a, 3));
}
