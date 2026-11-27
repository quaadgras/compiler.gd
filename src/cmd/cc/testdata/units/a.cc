#include "shape.h"

static int local_count = 3;
static int helper(int x) { return x * local_count; }
int global_count = 40;

const char *Shape::name() const { return "shape"; }

struct Init {
	Init(const char *s) { printf("init %s %d\n", s, global_count); }
};
static Init init_a("a");

int main() {
	Box<int> b;
	b.push(1);
	b.push(helper(2));
	Square sq(3);
	Shape *c = make_circle(1);
	int c1 = counter(), c2 = counter();
	printf("%d %d %d\n", b.sum(), c1, c2);
	sq.describe();
	c->describe();
	Holder h;
	h.h = make_hidden(5);
	h.s = c;
	int uh = use_holder(h);
	printf("%d %d\n", uh, hidden_value(h.h));
	int ops = get_op(0)(3, 4) + get_op(1)(3, 4);
	printf("%d %d %s\n", get_op(0) == add_op, ops, global_name);
	shared_value++;
	printf("%d %d %d\n", shared_value, global_count, Registry::get().made);
	delete c;
}
