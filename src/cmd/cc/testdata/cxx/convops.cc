// Built-in operators on a class through its conversion functions, and
// a reference to the base of a template's instance that nothing has
// completed yet.
#include <stdio.h>

struct Obj {
	int id;
};
struct Node : Obj {
	int extra;
};
struct Var {
	Obj *o;
	operator Obj *() const { return o; }
	bool operator==(const Var &v) const { return o == v.o; }
};
struct Name {
	const char *s;
	explicit operator bool() const { return s && *s; }
};

struct Dict {
	int n = 3;
};
template <class K, class V>
struct TypedDict : Dict {
	int extra = 4;
};
template <class T>
struct Holder {
	T t;
	T &get() { return t; }
};

int main() {
	Node n, m;
	Var a{&n};
	Node *p = &n, *q = &m;
	Name full{"x"}, empty{""};
	printf("%d %d %d %d\n", p == a, q == a, p != a, a == a);
	printf("%d %d %d %d\n", p && full, p && empty, empty || full, empty || (Obj *)nullptr);
	Holder<TypedDict<int, long>> h;
	Dict &d = h.get();
	printf("%d\n", d.n);
}

// A class template's arguments: its defaults, or another object's.
template <int N = 8>
struct Buf {
	char b[N];
	int size() const { return N; }
};
template <class A, class B>
struct Two {
	A a;
	B b;
	Two(const A &a, const B &b) : a(a), b(b) {}
};
static int ctad() {
	Buf k;
	Two t = Two(3, 2.5);
	Two u = t;
	return printf("%d %d %g %g\n", k.size(), t.a, t.b, u.b);
}
static int ctadDone = ctad();

// A conditional of pointers to a class and to its base.
struct Pad {
	int pad = 1;
};
struct Wide : Pad, Obj {
	int w = 5;
};
static int which(Obj *o) { return o->id; }
static int cond() {
	Wide w;
	w.id = 11;
	Obj o{22};
	Wide *wp = &w;
	Obj *op = &o;
	bool yes = w.w == 5;
	return printf("%d %d\n", which(yes ? wp : op), which(!yes ? wp : op));
}
static int condDone = cond();
