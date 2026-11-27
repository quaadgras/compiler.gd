// A parameter that is a template's instance with default arguments.
#include <stdio.h>

template <class T>
struct Hasher {
	static unsigned hash(const T &v) { return (unsigned)v; }
};
template <class T, class = void>
struct Compare {
	static bool eq(const T &a, const T &b) { return a == b; }
};
template <class T, class H = Hasher<T>, class C = Compare<T>>
struct Set {
	T one;
	int n = 1;
};
template <class K, class V, class H = Hasher<K>>
struct Map {
	K k;
	V v;
};
template <class T>
struct Arr {
	T first;
	int n;
};

template <class T>
static Arr<T> to_array(const Set<T> &s) { return Arr<T>{s.one, s.n}; }
template <class K, class V>
static int count(const Map<K, V> &m) { return (int)sizeof(m); }

int main() {
	Set<int> s;
	s.one = 7;
	const Set<double> cs{2.5, 3};
	Map<int, double> m;
	printf("%d %d %g %d %d\n", to_array(s).first, to_array(s).n, to_array(cs).first, to_array(cs).n, count(m) > 0);
}
