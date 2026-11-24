// Classes: member functions, constructors and destructors, temporaries,
// operators, new and delete.
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

struct Vec {
	float x, y;
	Vec() : x(0), y(0) {}
	Vec(float x, float y) : x(x), y(y) {}
	float dot(const Vec &o) const { return x * o.x + y * o.y; }
	Vec operator+(const Vec &o) const { return Vec(x + o.x, y + o.y); }
	Vec operator*(float k) const { return Vec(x * k, y * k); }
	Vec &operator+=(const Vec &o) {
		x += o.x;
		y += o.y;
		return *this;
	}
	bool operator==(const Vec &o) const { return x == o.x && y == o.y; }
	bool operator!=(const Vec &o) const { return !(*this == o); }
	Vec operator-() const { return Vec(-x, -y); }
	float &operator[](int i) { return i == 0 ? x : y; }
	void print() const;
	static Vec unit() { return Vec(1, 1); }
	static int count;
	static const int dims = 2;
};

int Vec::count = 7;

void Vec::print() const { printf("(%g, %g)\n", x, y); }

Vec operator*(float k, const Vec &v) { return v * k; }

// A class that owns memory: every constructor, copy and destructor is
// seen.
class Str {
	char *s;
	int n;

public:
	static int live;
	Str() : s(nullptr), n(0) {
		live++;
		printf("Str()\n");
	}
	Str(const char *p) {
		n = strlen(p);
		s = (char *)malloc(n + 1);
		strcpy(s, p);
		live++;
		printf("Str(\"%s\")\n", s);
	}
	Str(const Str &o) : n(o.n) {
		s = (char *)malloc(n + 1);
		strcpy(s, o.s ? o.s : "");
		live++;
		printf("Str(copy \"%s\")\n", s);
	}
	~Str() {
		live--;
		printf("~Str(\"%s\")\n", s ? s : "");
		free(s);
	}
	Str &operator=(const Str &o) {
		if (this != &o) {
			free(s);
			n = o.n;
			s = (char *)malloc(n + 1);
			strcpy(s, o.s ? o.s : "");
		}
		printf("Str = \"%s\"\n", s);
		return *this;
	}
	Str operator+(const Str &o) const {
		Str r;
		r.n = n + o.n;
		r.s = (char *)malloc(r.n + 1);
		strcpy(r.s, s ? s : "");
		strcat(r.s, o.s ? o.s : "");
		return r;
	}
	int size() const { return n; }
	const char *c_str() const { return s ? s : ""; }
	operator bool() const { return n != 0; }
	char operator[](int i) const { return s[i]; }
};

int Str::live = 0;

Str greet(Str name) { return Str("hello ") + name; }
int length(const Str &s) { return s.size(); }

struct Pair {
	Str a, b;
	int n = 42;
	Vec v{3, 4};
};

class Counter {
	int n;
	mutable int reads = 0;

public:
	explicit Counter(int start = 0) : n(start) {}
	Counter &operator++() {
		++n;
		return *this;
	}
	Counter operator++(int) {
		Counter old = *this;
		n++;
		return old;
	}
	int get() const {
		reads++;
		return n;
	}
	int readCount() const { return reads; }
	operator int() const { return n; }
};

struct Node {
	int value;
	Node *next;
	Node(int v, Node *n) : value(v), next(n) {}
};

enum Flags { None = 0, A = 1, B = 2 };
Flags operator|(Flags x, Flags y) { return Flags(int(x) | int(y)); }

namespace geo {
struct Rect {
	int w, h;
	int area() const { return w * h; }
};
int area(const Rect &r) { return r.area(); }
bool operator<(const Rect &a, const Rect &b) { return a.area() < b.area(); }
}

int main() {
	Vec a(1, 2), b(3, 4);
	Vec c = a + b;
	c.print();
	(a * 2).print();
	(2 * a).print();
	(-a).print();
	c += a;
	c.print();
	c[0] = 10;
	printf("%g %g %d %d\n", c[0], a.dot(b), a == b, a != b);
	Vec::unit().print();
	printf("%d %d %zu\n", Vec::count, Vec::dims, sizeof(Vec));
	Vec z;
	Vec zz = Vec();
	z.print();
	zz.print();

	printf("-- strings\n");
	{
		Str s("one");
		Str t = s;
		Str u;
		u = t;
		printf("%d %s %d\n", length(u), u.c_str(), Str::live);
	}
	printf("live %d\n", Str::live);
	{
		Str g = greet("world");
		printf("%s %d\n", g.c_str(), g.size());
		printf("%d\n", length(Str("temp") + g));
		printf("%c\n", g[1]);
		if (g)
			printf("not empty\n");
		Str e;
		if (!e)
			printf("empty\n");
	}
	printf("live %d\n", Str::live);
	{
		const Str &r = Str("bound");
		printf("%s\n", r.c_str());
	}
	printf("live %d\n", Str::live);

	printf("-- members\n");
	{
		Pair p;
		Pair q = p;
		q.n++;
		p = q;
		printf("%d %d %g %s\n", p.n, q.n, p.v.x + p.v.y, p.a.c_str());
	}
	printf("live %d\n", Str::live);

	printf("-- loops\n");
	for (int i = 0; i < 2; i++) {
		Str s("loop");
		if (i == 0)
			continue;
		printf("%d\n", s.size());
	}
	printf("live %d\n", Str::live);

	Counter k(5);
	++k;
	Counter old = k++;
	int n = k;
	printf("%d %d\n", old.get(), k.get());
	printf("%d %d %d\n", n, k + 1, k.readCount());

	printf("-- new\n");
	Node *list = nullptr;
	for (int i = 0; i < 4; i++)
		list = new Node(i, list);
	int sum = 0;
	while (list) {
		Node *next = list->next;
		sum += list->value;
		delete list;
		list = next;
	}
	printf("%d\n", sum);
	Str *ps = new Str("heap");
	printf("%s %d\n", ps->c_str(), Str::live);
	delete ps;
	printf("live %d\n", Str::live);
	int *pi = new int(5);
	int *arr = new int[4]();
	arr[2] = *pi;
	printf("%d %d\n", arr[0], arr[2]);
	delete pi;
	delete[] arr;
	Vec *pv = new Vec[3];
	pv[1] = Vec(5, 6);
	pv[1].print();
	delete[] pv;

	Flags f = A | B;
	printf("%d\n", f);
	geo::Rect r1 = {2, 3}, r2{4, 5};
	printf("%d %d %d\n", area(r1), r2.area(), r1 < r2);
	return 0;
}
