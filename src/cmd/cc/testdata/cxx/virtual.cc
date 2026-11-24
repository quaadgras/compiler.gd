// Base classes and virtual functions.
#include <stdio.h>

struct Tracer {
	const char *name;
	Tracer(const char *n) : name(n) { printf("+%s\n", name); }
	~Tracer() { printf("-%s\n", name); }
};

class Shape {
	int id;
	static int next;

protected:
	Tracer t;

public:
	Shape() : id(next++), t("shape") {}
	Shape(const Shape &o) : id(next++), t("shape copy") {}
	virtual ~Shape() { printf("~Shape %d\n", id); }
	virtual double area() const = 0;
	virtual const char *name() const { return "shape"; }
	int ident() const { return id; }
	void describe() const { printf("%s %d: %g\n", name(), id, area()); }
};

int Shape::next = 1;

class Rect : public Shape {
protected:
	double w, h;

public:
	Rect(double w, double h) : w(w), h(h) {}
	~Rect() override { printf("~Rect\n"); }
	double area() const override { return w * h; }
	const char *name() const override { return "rect"; }
	virtual void scale(double k) {
		w *= k;
		h *= k;
	}
};

class Square final : public Rect {
	Tracer extra;

public:
	explicit Square(double s) : Rect(s, s), extra("square") {}
	const char *name() const override { return "square"; }
	void scale(double k) override {
		Rect::scale(k);
		printf("scaled a square to %g\n", w);
	}
};

class Circle : public Shape {
	double r;

public:
	Circle(double r) : r(r) {}
	double area() const override { return 3 * r * r; }
	// name is Shape's
};

struct Empty {
	int twice(int x) const { return 2 * x; }
};

struct Base1 {
	int a = 1;
	int get() const { return a; }
	void set(int v) { a = v; }
};

struct Base2 {
	int b = 2;
	int get2() const { return b; }
};

struct Multi : Base1, Base2, Empty {
	int c = 3;
	int sum() const { return a + b + c + get() + get2() + twice(c); }
};

struct Animal {
	virtual const char *sound() { return "..."; }
	const char *speak() { return sound(); }
	Animal() { printf("made: %s\n", sound()); }
	virtual ~Animal() = default;
};

struct Dog : Animal {
	const char *sound() override { return "woof"; }
};

struct Registry {
	int n;
	Registry() : n(0) { printf("registry made\n"); }
	int add() { return ++n; }
};

Registry &registry() {
	static Registry r;
	return r;
}

int counted(int start) {
	static int calls = start * 10;
	return ++calls;
}

const char *kind(bool) { return "bool"; }
const char *kind(int) { return "int"; }

int total(const Base2 &b) { return b.b; }
int first(Base1 *p) { return p ? p->a : -1; }

double sumAreas(Shape **shapes, int n) {
	double s = 0;
	for (int i = 0; i < n; i++)
		s += shapes[i]->area();
	return s;
}

int main() {
	{
		Rect r(2, 3);
		Square s(4);
		Circle c(1);
		Shape *shapes[] = {&r, &s, &c};
		for (int i = 0; i < 3; i++)
			shapes[i]->describe();
		printf("%g\n", sumAreas(shapes, 3));
		Rect *rp = &s;
		rp->scale(2);
		r.scale(2);
		const Shape &ref = s;
		printf("%s %g %d\n", ref.name(), ref.area(), ref.ident());
		printf("%s\n", s.Rect::name());
		Rect copy = s; // sliced
		printf("%s %g\n", copy.name(), copy.area());
		Square *sq = static_cast<Square *>(rp);
		printf("%s\n", sq->name());
	}
	printf("-- heap\n");
	Shape *p = new Square(1);
	p->describe();
	delete p;
	p = new Circle(2);
	delete p;

	printf("-- multiple\n");
	Multi m;
	m.set(10);
	Base2 *b2 = &m;
	Base1 *b1 = &m;
	printf("%d %d %d %d\n", m.sum(), total(m), first(&m), b2->b + b1->a);
	Multi *back = static_cast<Multi *>(b2);
	printf("%d %d\n", back->c, back == &m);
	Multi *none = nullptr;
	b2 = none;
	printf("%d %d\n", b2 == nullptr, first(none));
	printf("%zu %zu %zu\n", sizeof(Empty), sizeof(Multi), sizeof(Base1));

	printf("-- statics\n");
	printf("before\n");
	registry().add();
	printf("%d\n", registry().add());
	printf("%d\n", counted(1));
	printf("%d\n", counted(5));
	int x = 1, y = 2;
	auto lt = x < y;
	printf("%s %s %s %s %zu\n", kind(x < y), kind(!x), kind(x && y), kind(x + y), sizeof(lt));

	printf("-- arrays\n");
	Tracer *ts = new Tracer[2]{Tracer("a"), Tracer("b")};
	delete[] ts;
	Shape *line = new Rect[2]{Rect(1, 1), Rect(2, 2)};
	delete[] static_cast<Rect *>(line);

	printf("-- construction\n");
	Dog d;
	Animal *a = &d;
	printf("%s %s\n", a->speak(), d.speak());
	Animal plain = d;
	printf("%s\n", plain.speak());
	return 0;
}
