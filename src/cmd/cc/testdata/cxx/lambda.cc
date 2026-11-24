// Lambda expressions.
#include <cstdio>
#include <utility>

template <class F>
int apply(F f, int x) { return f(x); }

template <class F>
void repeat(int n, F f) {
	for (int i = 0; i < n; i++)
		f(i);
}

int call_ptr(int (*fn)(int), int x) { return fn(x); }

struct Counter {
	int n = 0;
	int step = 2;
	void run(int times) {
		repeat(times, [this](int i) { n += step * i; });
		auto get = [=]() { return n + step; };
		printf("%d %d\n", n, get());
	}
};

struct Tracker {
	static int live;
	int id;
	Tracker(int id) : id(id) { live++; }
	Tracker(const Tracker &o) : id(o.id) { live++; }
	~Tracker() { live--; }
};
int Tracker::live = 0;

template <class... Ts>
int sum_later(Ts... xs) {
	auto f = [=]() { return (xs + ... + 0); };
	return f();
}

auto make_adder(int k) {
	return [k](int x) { return x + k; };
}

int main() {
	auto square = [](int x) { return x * x; };
	printf("%d %d\n", square(7), apply(square, 3));

	int base = 10;
	auto by_value = [base](int x) { return base + x; };
	auto by_ref = [&base](int x) { base += x; };
	base = 20;
	by_ref(5);
	printf("%d %d\n", by_value(1), base);

	int total = 0;
	repeat(5, [&](int i) { total += i * base; });
	printf("%d\n", total);

	auto counter = [n = 0]() mutable { return ++n; };
	counter();
	counter();
	printf("%d\n", counter());

	double scale = 1.5;
	auto typed = [=](double x) -> double { return x * scale; };
	printf("%g %zu %d\n", typed(2), sizeof(square), int(sizeof(by_value) == sizeof(int)));

	printf("%d %d\n", call_ptr([](int x) { return x + 100; }, 1), call_ptr(square, 9));
	int (*fp)(int) = [](int x) { return -x; };
	printf("%d\n", fp(4));

	Counter c;
	c.run(4);

	{
		Tracker t(7);
		auto keep = [t]() { return t.id; };
		auto copy = keep;
		printf("%d %d %d\n", keep(), copy(), Tracker::live);
	}
	printf("live %d\n", Tracker::live);

	auto outer = [&](int a) {
		auto inner = [&, a](int b) { return a * b + base; };
		return inner(2);
	};
	printf("%d\n", outer(4));
	printf("%d %d\n", sum_later(1, 2, 3), make_adder(5)(6));

	auto moved = [v = std::move(base)]() { return v; };
	printf("%d\n", moved());
	bool flag = [](int a, int b) { return a < b; }(1, 2);
	printf("%d\n", flag);
	return 0;
}
