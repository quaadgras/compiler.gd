// A constructor whose parameters all have default arguments is the
// default constructor: of a variable with no initializer, of a member
// and of a base that no initializer names, and of new T().
#include <cstdio>
struct Name {
	const char *s;
	Name(const char *p = "none") : s(p) {}
};
struct Base {
	Name name;
	unsigned id = 7;
	int x;
	Base() : x(3) {}
};
struct Base2 {
	Name name;
	unsigned id = 7;
};
struct D : Base {
	int y = 4;
};
int main() {
	Base b;
	Base2 b2;
	D d;
	D *e = new D();
	Base2 *f = new Base2();
	Name n;
	printf("%s %s %s %s %s %s %d %d\n", b.name.s, b2.name.s, d.name.s, e->name.s, f->name.s, n.s, e->y, e->x);
}
