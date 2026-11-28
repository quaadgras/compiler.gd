// An attribute before a parameter, a constructor that is defaulted
// outside its class, a subscript of an object that converts to a
// pointer, and the comparisons of pairs.
#include <stdio.h>
#include <utility>
#define UNUSED __attribute__((__unused__))

struct Fill {
	int v = 7;
	Fill();
	~Fill();
	bool add(UNUSED const int &a) { return true; }
	bool add(UNUSED const int &a, UNUSED const int &b) { return false; }
};
Fill::Fill() = default;
Fill::~Fill() = default;
template <class T> struct Scoped {
	T *p;
	explicit Scoped(T *p) : p(p) {}
	~Scoped() { delete[] p; }
	operator T *() { return p; }
};
int main() {
	Fill f;
	Scoped<int> s(new int[4]);
	s[2] = 5;
	s[3] = s[2] * 2;
	printf("%d %d %d %d %d\n", f.v, f.add(1), f.add(1, 2), s[3], std::make_pair(2, 1) >= std::make_pair(1, 9));
	return 0;
}
