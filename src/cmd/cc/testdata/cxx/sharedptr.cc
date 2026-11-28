// shared_ptr, weak_ptr and enable_shared_from_this.
#include <stdio.h>
#include <memory>
#include <vector>

static int live;
struct Base {
	int id;
	explicit Base(int id) : id(id) { live++; }
	virtual ~Base() { live--; }
	virtual int kind() const { return 0; }
};
struct Derived : Base, std::enable_shared_from_this<Derived> {
	std::vector<std::weak_ptr<Base>> seen;
	explicit Derived(int id) : Base(id) {}
	int kind() const override { return 1; }
	std::shared_ptr<Derived> self() { return shared_from_this(); }
};
static int closed;
int main() {
	std::weak_ptr<Base> w;
	{
		std::shared_ptr<Derived> d = std::make_shared<Derived>(7);
		std::shared_ptr<Base> b = d;
		std::shared_ptr<Base> c(new Base(3));
		w = b;
		d->seen.push_back(c);
		std::shared_ptr<Derived> s = d->self();
		std::shared_ptr<int> part(d, &d->id);
		printf("%ld %d %d %d %d %d\n", b.use_count(), b->kind(), c->kind(), *part, s == d, (int)w.expired());
		std::shared_ptr<Derived> back = std::static_pointer_cast<Derived>(b);
		c.reset();
		printf("%d %d %ld %d\n", (int)d->seen[0].expired(), live, back.use_count(), !c);
		std::unique_ptr<Base> u(new Base(9));
		std::shared_ptr<Base> fromu = std::move(u);
		std::shared_ptr<FILE> f(stdout, [](FILE *) { closed++; });
		printf("%d %d %d\n", fromu->id, !u, live);
	}
	printf("%d %d %d %d\n", live, (int)w.expired(), !w.lock(), closed);
	return 0;
}
