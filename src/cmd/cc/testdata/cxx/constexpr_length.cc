// A call of a constexpr function, of a template too, as an operand of
// the length of an array: of a variable and of a member.
#include <cstdio>
template <typename T>
constexpr T fl(T x) {
	return x < 2 ? x : 1 + fl(x >> 1);
}
constexpr int plain(int x) { return x < 2 ? x : 1 + plain(x >> 1); }
enum E { EA, EB, EC, ED, EE, EMAX };
int a[plain(5) + 2];
int b[fl(5) + 2];
int c[fl(EMAX - 1)];
struct S {
	int d[fl(5)];
	int e[fl(5) + 2];
	int f[plain(5) + 2];
	int g[fl(EMAX - 1) + 2];
};
int main() { S s; printf("%d %d %d | %d %d %d %d\n", (int)sizeof(a), (int)sizeof(b), (int)sizeof(c), (int)sizeof(s.d), (int)sizeof(s.e), (int)sizeof(s.f), (int)sizeof(s.g)); }
