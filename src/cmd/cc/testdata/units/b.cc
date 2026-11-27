#include "shape.h"

struct Hidden {
	int v;
	double pad[2];
};

static int local_count = 1000; // another file's static of the same name
static int helper(int x) { return x + local_count; }
const char *global_name = "from b";

class Circle : public Shape {
	double r;

public:
	Circle(double r) : r(r) {}
	~Circle() { printf("~Circle\n"); }
	double area() const override { return 3 * r * r; }
	const char *name() const override { return "circle"; }
};

struct InitB {
	InitB() { global_count += 2; printf("init b %d\n", shared_value); }
};
static InitB init_b;

Shape *make_circle(double r) { return new Circle(r); }
Hidden *make_hidden(int v) { return new Hidden{v + helper(0), {1, 2}}; }
int hidden_value(const Hidden *h) { return h->v; }
static int mul_op(int a, int b) { return a * b; }
binop get_op(int which) { return which == 0 ? add_op : mul_op; }
int use_holder(Holder &h) {
	Box<double> b;
	b.push(1.5);
	b.push(h.s->area());
	shared_value += 10;
	return (int)b.sum() + counter() + (h.n == nullptr) + Registry::get().made;
}
