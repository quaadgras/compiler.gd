#include <cstdio>
#define AI __attribute__((always_inline)) inline
struct V {
	int x;
	AI V() : x(1) {}
	AI V(const V &o) : x(o.x) {}
	AI explicit V(int i) : x(i) {}
	AI constexpr V(int a, int b) : x(a + b) {}
	AI ~V() {}
	AI operator int() const { return x; }
	AI V &operator=(const V &o) { x = o.x; return *this; }
	static void set(const V &a, V &r) { r.x = a.x; }
	template <typename T> AI V(T *p) : x(7) {}
};
int main() { V a(3), b, c(1, 2), d(&a); V::set(a, b); b = c; printf("%d %d %d\n", (int)b, a.x, d.x); }
