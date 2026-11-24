#include <cstdio>
#include <algorithm>
struct P { int k; double w; bool operator<(const P &o) const { return k < o.k; } };
int main() {
	int a[8] = {5, 3, 9, 1, 7, 2, 8, 6};
	std::sort(a, a + 8);
	for (int x : a) printf("%d ", x);
	std::sort(a, a + 8, [](int x, int y) { return x > y; });
	std::reverse(a + 2, a + 6);
	for (int x : a) printf("%d ", x);
	P ps[3] = {{3, 1.5}, {1, 2.5}, {2, 3.5}};
	std::sort(ps, ps + 3);
	float f = 2.5f;
	printf("\n%d %d %g %g %d %g\n", std::min(3, 4), std::max(3, 4), std::clamp(f, 0.0f, 1.0f), std::clamp(-1.5, -1.0, 1.0), std::max({1, 9, 4}), ps[0].w);
	printf("%ld %ld %d %d\n", std::find(a, a + 8, 7) - a, std::count(a, a + 8, 3), *std::min_element(a, a + 8), *std::max_element(a, a + 8));
}
