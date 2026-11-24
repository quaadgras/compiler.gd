// Pointers to members.
#include <cstdio>

struct Base {
	int id = 1;
	virtual ~Base() {}
	virtual const char *name() const { return "base"; }
	int plain(int x) { return x + id; }
};
struct Derived : Base {
	int extra = 5;
	const char *name() const override { return "derived"; }
	int twice(int x) const { return 2 * x + extra; }
	int over(int x) { return x; }
	int over(double x) { return int(x) + 100; }
	static int stat(int x) { return -x; }
};
struct Point {
	int x, y;
	double z;
};

typedef int (Derived::*Method)(int) const;

template <class T, class R, class... P>
R invoke(T *obj, R (T::*m)(P...), P... args) { return (obj->*m)(args...); }
template <class T, class R, class... P>
R invoke(const T *obj, R (T::*m)(P...) const, P... args) { return (obj->*m)(args...); }

template <class T, class M>
M get(const T &obj, M T::*field) { return obj.*field; }

template <class T, class M, M T::*field>
struct Getter {
	static M of(const T &o) { return o.*field; }
};

struct Table {
	int (Derived::*on_int)(int) = nullptr;
	const char *(Base::*namer)() const = &Base::name;
};

int main() {
	Derived d;
	Base b;
	Base *bp = &d;

	const char *(Base::*namer)() const = &Base::name;
	printf("%s %s %s\n", (b.*namer)(), (bp->*namer)(), (d.*namer)());

	int (Base::*plain)(int) = &Base::plain;
	Method twice = &Derived::twice;
	printf("%d %d %d\n", (b.*plain)(10), (d.*twice)(10), (d.*plain)(1));
	int (Derived::*o1)(int) = &Derived::over;
	int (Derived::*o2)(double) = &Derived::over;
	int (Derived::*inherited)(int) = &Base::plain;
	printf("%d %d %d\n", (d.*o1)(3), (d.*o2)(3.5), (d.*inherited)(2));
	int (*stat)(int) = &Derived::stat;
	printf("%d\n", stat(4));

	Method none = nullptr;
	printf("%d %d %d %d\n", twice == &Derived::twice, twice != none, none == nullptr, bool(twice));
	if (!none)
		none = twice;
	printf("%d %zu\n", (d.*none)(1), sizeof(twice));

	int Point::*px = &Point::x;
	int Point::*py = &Point::y;
	double Point::*pz = &Point::z;
	Point pt = {3, 4, 5.5};
	Point *pp = &pt;
	pt.*px = 30;
	pp->*py += 1;
	printf("%d %d %g %d\n", pt.x, pt.y, pt.*pz, px == py);

	printf("%d %d %s\n", invoke(&d, &Derived::twice, 4), invoke(&b, &Base::plain, 4), invoke(bp, &Base::name));
	printf("%d %g %g\n", get(pt, &Point::x), get(pt, &Point::z), Getter<Point, double, &Point::z>::of(pt));
	Table t;
	t.on_int = &Derived::over;
	printf("%d %s\n", (d.*t.on_int)(9), (d.*t.namer)());
	return 0;
}
