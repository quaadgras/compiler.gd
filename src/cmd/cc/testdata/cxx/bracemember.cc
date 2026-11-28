// A member initialized with braces by a class that takes an initializer_list.
#include <stdio.h>
#include <initializer_list>

template <class T>
struct Vec {
	T items[8];
	int n = 0;
	Vec() {}
	Vec(std::initializer_list<T> l) {
		for (const T &v : l) {
			items[n++] = v;
		}
	}
};

struct Bus {
	Vec<float> levels = {1.0, 0.5, 0.25};
	Vec<const char *> names{"a", "b"};
	Vec<int> ids;
	Vec<int> more;
	Bus() : ids{7, 8, 9}, more{} {}
};

int main() {
	Bus b;
	printf("%d %g %d %s %d %d %d\n", b.levels.n, b.levels.items[2], b.names.n, b.names.items[1], b.ids.n, b.ids.items[2], b.more.n);
}

// A class whose constructors are all = default or = delete is an
// aggregate to braces (C++17).
struct Handle {
	void *value;
	Handle() = delete;
};
struct Pt {
	int x, y;
	Pt() = default;
};
static Handle make(void *p) { return {p}; }
static int agg() {
	int v = 3;
	Handle h = make(&v);
	h = {nullptr};
	Pt p = {4, 5};
	Pt q{};
	return printf("%d %d %d %d\n", h.value == nullptr, p.x + p.y, q.x, *(int *)make(&v).value);
}
static int aggDone = agg();
