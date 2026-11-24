#include <cstdio>
template <typename T>
struct Cow {
	T *ptr = nullptr; long n = 0;
	template <bool p_init = false> int resize(long size);
	template <typename U> U sum(U start) const;
	long size() const { return n; }
};
template <typename T>
template <bool p_initialize>
int Cow<T>::resize(long p_size) {
	T *np = new T[p_size];
	for (long i = 0; i < p_size; i++) {
		if (i < size()) np[i] = ptr[i];
		else if (p_initialize) np[i] = T(7);
	}
	ptr = np; n = p_size;
	return p_initialize ? 1 : 0;
}
template <typename T>
template <typename U>
U Cow<T>::sum(U start) const {
	for (long i = 0; i < n; i++) start += U(ptr[i]);
	return start;
}
struct Str { Cow<char> c; int grow(long n) { return c.template resize<false>(n); } int init(long n) { return c.resize<true>(n); } };
int main() {
	Str s; int a = s.init(3); int b = s.grow(2); int d = s.init(4);
	Cow<double> cd; cd.resize<true>(2);
	printf("%d %d %d %ld %d %g\n", a, b, d, s.c.size(), s.c.sum(0), cd.sum(0.5));
}
