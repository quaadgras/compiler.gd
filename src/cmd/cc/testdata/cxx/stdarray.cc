#include <cstdio>
#include <array>
struct Pow { unsigned long f; int e, k; };
static constexpr std::array<Pow, 3> kPows = {{ {0xAB70FE17C79AC6CA, -1060, -300}, {0xFF77B1FCBEBCDC4F, -1034, -292}, {7, 1, 2} }};
static int sum(const std::array<int, 4> &a) { int t = 0; for (int x : a) t += x; return t; }
int main() {
	std::array<int, 4> a = {1, 2, 3, 4};
	std::array<int, 4> b{};
	b.fill(2); b[1] = 9;
	std::array<char, 8> buf{};
	buf[0] = 'h'; buf[1] = 'i';
	printf("%d %d %zu %d %d\n", sum(a), sum(b), a.size(), a.back(), a == b);
	printf("%lx %d %d %zu %s\n", kPows[1].f, kPows[0].e, kPows[2].k, kPows.size(), buf.data());
}
