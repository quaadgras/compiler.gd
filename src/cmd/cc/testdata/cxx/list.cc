// Braced initializer lists as expressions, std::initializer_list, and <cmath>.
#include <cmath>
#include <cstdio>
#include <initializer_list>
struct V3 { float x, y, z; V3() : x(0), y(0), z(0) {} V3(float a, float b, float c) : x(a), y(b), z(c) {} };
struct Basis { V3 rows[3]; Basis(const V3 &a, const V3 &b, const V3 &c) { rows[0] = a; rows[1] = b; rows[2] = c; } };
struct Agg { int a; V3 v; };
V3 make() { return {1, 2, 3}; }
float len(const V3 &v) { return std::sqrt(v.x * v.x + v.y * v.y + v.z * v.z); }
int total(std::initializer_list<int> l) { int s = 0; for (const int *p = l.begin(); p != l.end(); ++p) s += *p; return s; }
struct Bag { int n; Bag(std::initializer_list<int> l) : n(int(l.size())) {} };
const Basis flip = { { -1, 0, 0 }, { 0, 1, 0 }, { 0, 0, 1 } };
int main() {
	V3 v = make();
	Agg a = {7, {4, 5, 6}};
	v = {3, 4, 0};
	Bag b = {1, 2, 3, 4};
	Bag c{5, 6};
	printf("%g %g %g %d %g\n", len(v), len({1, 0, 0}), flip.rows[0].x, a.a, a.v.z);
	printf("%d %d %d %d\n", total({1, 2, 3}), b.n, c.n, total({}));
	printf("%g %g %g %d %d %d\n", std::abs(-2.5), std::pow(2.0f, 3), std::sqrt(16), std::isnan(0.0 / (v.z)), std::isinf(1.0f), std::abs(-3));
	float f = std::sqrt(2.0f);
	printf("%d %d %g\n", int(sizeof(std::sqrt(2.0f))), int(sizeof(std::sqrt(2))), double(std::floor(f * 100)));
	return 0;
}
