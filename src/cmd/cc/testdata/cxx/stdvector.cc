// std::vector, whose elements are in a slice of Go's.
#include <stdio.h>
#include <vector>

static int live;
struct Tracked {
	int v;
	Tracked(int v = 0) : v(v) { live++; }
	Tracked(const Tracked &o) : v(o.v) { live++; }
	Tracked(Tracked &&o) noexcept : v(o.v) {
		o.v = -1;
		live++;
	}
	Tracked &operator=(const Tracked &o) {
		v = o.v;
		return *this;
	}
	~Tracked() { live--; }
};
struct Holder {
	std::vector<int> ints;
	std::vector<std::vector<Tracked>> nested;
};

static int sum(const std::vector<int> &v) {
	int t = 0;
	for (int x : v)
		t += x;
	return t;
}

int main() {
	std::vector<int> a;
	for (int i = 0; i < 100; i++)
		a.push_back(i * i);
	printf("%zu %d %d %d %d\n", a.size(), a[7], a.front(), a.back(), sum(a));
	std::vector<int> b = {5, 3, 9};
	b.insert(b.begin() + 1, 42);
	b.erase(b.begin());
	b.resize(6, 7);
	a = b;
	a.pop_back();
	for (auto p = a.begin(); p != a.end(); ++p)
		printf("%d ", *p);
	printf("| %d %d\n", a == b, (int)(a.end() - a.begin()));
	{
		std::vector<Tracked> t;
		for (int i = 0; i < 10; i++)
			t.emplace_back(i);
		t.push_back(t[2]);
		t.erase(t.begin() + 3, t.begin() + 5);
		std::vector<Tracked> u = t, w = std::move(t);
		u.resize(3);
		printf("%zu %zu %zu %d %d %d\n", t.size(), u.size(), w.size(), w[3].v, w.back().v, live);
		Holder *h = new Holder;
		h->ints.assign(3, 4);
		h->nested.push_back(w);
		h->nested.push_back(u);
		h->nested[0].insert(h->nested[0].begin(), Tracked(77));
		Holder copy = *h;
		delete h;
		printf("%d %zu %d %d\n", sum(copy.ints), copy.nested.size(), copy.nested[0][0].v, live);
		for (auto it = copy.nested[1].rbegin(); it != copy.nested[1].rend(); ++it)
			printf("%d ", it->v);
		printf("\n");
	}
	printf("%d\n", live);
	std::vector<const char *> names(2, "x");
	names.push_back("y");
	std::vector<double> d(a.begin(), a.end());
	printf("%s%s%s %g %zu\n", names[0], names[1], names[2], d[1], d.capacity() >= d.size());
	return 0;
}
