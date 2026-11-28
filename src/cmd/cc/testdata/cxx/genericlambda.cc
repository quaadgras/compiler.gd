// Lambdas with auto parameters: an operator() for each type they are
// called with.
#include <stdio.h>
#include <string>
#include <vector>
#include <algorithm>

template <class T, class F> static T apply1(const T &v, F f) { return f(v); }
struct Scaler {
	double k;
	template <class T> std::vector<T> scale(const std::vector<T> &in, int &calls) const {
		std::vector<T> out;
		std::transform(in.begin(), in.end(), std::back_inserter(out), [=, &calls](const auto &x) {
			calls++;
			return (T)(x * k);
		});
		return out;
	}
};
int main() {
	int base = 10;
	std::string tag = "t";
	auto add = [base](auto a, auto b) { return a + b + base; };
	auto show = [&](const auto &v) {
		tag += "!";
		return tag.size() + sizeof v;
	};
	size_t s1 = show(1), s2 = show(2.0);
	printf("%d %g %zu %zu\n", add(1, 2), add(1.5, 2), s1, s2);
	printf("%d %s\n", apply1(4, [](auto x) { return x * x; }), apply1(std::string("ab"), [&tag](const auto &s) { return s + tag; }).c_str());
	Scaler sc{2.5};
	int calls = 0;
	std::vector<int> a = sc.scale(std::vector<int>{1, 2, 3}, calls);
	std::vector<double> b = sc.scale(std::vector<double>{1, 2}, calls);
	auto cmp = [](const auto &x, const auto &y) { return x > y; };
	std::sort(a.begin(), a.end(), cmp);
	printf("%d %d %g %d %d\n", a[0], a[2], b[1], calls, (int)cmp(std::string("b"), std::string("a")));
	return 0;
}
