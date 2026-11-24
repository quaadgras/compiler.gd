#include <cstdio>
struct Array {
	int n = 0;
	Array &operator=(const Array &o) { n = o.n + 1; return *this; }
	bool operator==(const Array &o) const { return n == o.n; }
	int operator[](int i) const { return n + i; }
};
struct Typed : Array {
	Typed &operator=(const Array &o) { Array::operator=(o); n *= 2; return *this; }
	bool same(const Array &o) const { return Array::operator==(o) && this->operator[](1) == Array::operator[](1); }
};
int main() {
	Array a; a.n = 3;
	Typed t; t = a;
	printf("%d %d\n", t.n, t.same(t));
}
