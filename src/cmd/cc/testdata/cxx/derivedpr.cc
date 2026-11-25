#include <cstdio>
struct Arr {
	int n;
	Arr() : n(0) { printf("Arr()\n"); }
	Arr(const Arr &o) : n(o.n) { printf("Arr(copy %d)\n", n); }
	~Arr() { printf("~Arr %d\n", n); }
};
template <typename T> struct Typed : Arr {
	Typed() { n = sizeof(T); }
	Typed(const Arr &a) { n = a.n + 100; }
	~Typed() { printf("~Typed %d\n", n); }
};
static Typed<double> list() { Typed<double> t; return t; }
static int count(const Arr &a) { return a.n; }
static int byval(Arr a) { return a.n; }
int main() {
	Arr a = list();
	printf("%d\n", a.n);
	printf("%d\n", count(list()));
	printf("%d\n", byval(list()));
	const Arr &r = list();
	printf("%d\n", r.n);
}
