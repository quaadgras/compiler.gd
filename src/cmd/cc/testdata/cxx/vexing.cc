// Declarations that could be read as functions, and are not.
#include <stdio.h>

struct V {
	int x, y;
	V() : x(0), y(0) {}
	V(int x, int y) : x(x), y(y) {}
	V operator*(const V &o) const { return V(x * o.x, y * o.y); }
	V twice() const { return V(2 * x, 2 * y); }
	void show() const { printf("V %d %d\n", x, y); }
};

struct R {
	V a, b;
	R(const V &a, const V &b) : a(a), b(b) {}
	R(const V &a, int k) : a(a), b(k, k) {}
	void show() const { printf("R %d %d %d %d\n", a.x, a.y, b.x, b.y); }
};

struct ok_color {
	struct Lab {
		int l;
	};
	Lab make(int v) { return Lab{v}; }
};

struct Opt {
	const char *name;
	int kind;
};

struct List {
	int n = 0;
	void push_back(const Opt &o) { printf("opt %s %d\n", o.name, o.kind); n++; }
};

static V size() { return V(3, 4); }

struct Holder {
	V member{5, 6};
	void show() const {
		R r(V(), member);
		r.show();
	}
};

int main() {
	V size2 = size();
	R r1(V(), size2);
	R r2(V(), V(1, 1));
	R r3(V(), size2.x > 0 ? 5 : 6);
	const R r4(V(), size());
	r1.show();
	r2.show();
	r3.show();
	r4.show();
	V v = (V(V(1, 2).x, 3) * size2.twice()).twice();
	v.show();
	V(size2.x, size2.y).show();
	int spmap[5]{0, 1, 4, 3, 2};
	const V *vs[2]{&v, &size2};
	printf("%d %d\n", spmap[2], vs[1]->y);
	ok_color ok_color;
	ok_color::Lab lab = ok_color.make(7);
	printf("%d\n", lab.l);
	Holder().show();
	List l;
	l.push_back({"plain", 3});
	printf("%d\n", l.n);
}
