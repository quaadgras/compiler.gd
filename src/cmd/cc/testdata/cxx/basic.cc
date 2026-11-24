// C++ without classes: references, overloads, namespaces, default
// arguments, casts, enumerations.
#include <stdio.h>
#include <string.h>

namespace math {
int square(int x) { return x * x; }
double square(double x) { return x * x; }
namespace detail {
int twice(int x) { return 2 * x; }
}
inline namespace v1 {
int version() { return 1; }
}
const int limit = 10;
int counter = 0;
}

namespace {
int hidden(int x) { return x + 100; }
}

void swap(int &a, int &b) {
	int t = a;
	a = b;
	b = t;
}

int &pick(int &a, int &b, bool first) { return first ? a : b; }

int sum(int a, int b = 10, int c = 100) { return a + b + c; }

const char *kind(int) { return "int"; }
const char *kind(long) { return "long"; }
const char *kind(unsigned) { return "unsigned"; }
const char *kind(char) { return "char"; }
const char *kind(bool) { return "bool"; }
const char *kind(double) { return "double"; }
const char *kind(float) { return "float"; }
const char *kind(const char *) { return "const char *"; }
const char *kind(void *) { return "void *"; }
const char *kind(int *) { return "int *"; }

const char *ref(int &) { return "int &"; }
const char *ref(const int &) { return "const int &"; }
const char *ref(int &&) { return "int &&"; }

enum Color { Red, Green = 5, Blue };
enum class Shape : unsigned char { Circle, Square = 7 };
const char *kind(Color) { return "Color"; }
const char *kind(Shape) { return "Shape"; }

struct Point {
	int x, y;
};

int dot(const Point &a, const Point &b) { return a.x * b.x + a.y * b.y; }
void move(Point &p, int dx, int dy = 0) {
	p.x += dx;
	p.y += dy;
}

extern "C" int c_add(int a, int b) { return a + b; }
extern "C" {
int c_mul(int a, int b) { return a * b; }
}

using math::square;
using Number = long;
typedef unsigned Count;

int fact(int n) { return n <= 1 ? 1 : n * fact(n - 1); }

int main() {
	printf("%d %g\n", math::square(7), math::square(1.5));
	printf("%d %d %d\n", math::detail::twice(4), math::version(), math::v1::version());
	printf("%d %d\n", square(3), ::math::square(4));
	printf("%d\n", hidden(1));

	int a = 1, b = 2;
	swap(a, b);
	printf("%d %d\n", a, b);
	pick(a, b, true) = 10;
	pick(a, b, false) = 20;
	printf("%d %d\n", a, b);
	int &r = a;
	r++;
	r += 5;
	const int &cr = a + 1;
	int &&rr = 3;
	rr++;
	printf("%d %d %d %d\n", a, r, cr, rr);

	printf("%d %d %d\n", sum(1), sum(1, 2), sum(1, 2, 3));

	long l = 1;
	unsigned u = 2;
	char c = 'c';
	short s = 3;
	float f = 1;
	printf("%s %s %s %s %s\n", kind(1), kind(l), kind(u), kind(c), kind(s));
	printf("%s %s %s %s\n", kind(true), kind(1.0), kind(f), kind('x'));
	printf("%s %s %s\n", kind("s"), kind(&a), kind((void *)0));
	printf("%s %s\n", kind(Green), kind(Shape::Square));
	const int ci = 1;
	printf("%s %s %s %s\n", ref(a), ref(ci), ref(1), ref(a + 1));

	Point p = {1, 2}, q = {3, 4};
	move(p, 10);
	move(q, 1, 1);
	printf("%d %d %d %d %d\n", p.x, p.y, q.x, q.y, dot(p, q));
	Point &pr = p;
	pr.x = 0;
	printf("%d\n", p.x);

	printf("%d %d\n", c_add(1, 2), c_mul(3, 4));

	Number n = 5;
	Count k(6);
	int z{7};
	int zero{};
	auto x = 1.5;
	auto &ar = a;
	ar = 99;
	const auto *ap = &a;
	printf("%ld %u %d %d %g %d %d\n", n, k, z, zero, x, a, *ap);

	printf("%d %d %d\n", static_cast<int>(3.9), int(2.9), (int)1.9);
	printf("%u\n", static_cast<unsigned char>(300));
	const char *str = "hello";
	char *mut = const_cast<char *>(str);
	printf("%zu %c\n", strlen(mut), *reinterpret_cast<const char *>(str));
	printf("%d %d %d\n", Red, Blue, static_cast<int>(Shape::Square));
	printf("%d %zu\n", int(Color::Green), sizeof(Shape));

	int arr[math::limit];
	for (int i = 0; i < math::limit; i++)
		arr[i] = i * i;
	int total = 0;
	for (int i = 0; i < math::limit; i++)
		total += arr[i];
	math::counter += total;
	printf("%d %d %d\n", total, math::counter, fact(5));
	bool t = true, fl = false;
	printf("%d %d %d\n", t, fl, t && !fl);
	return 0;
}
