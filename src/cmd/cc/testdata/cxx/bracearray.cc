#include <cstdio>
#include <initializer_list>
struct Var { int v; Var() : v(0) {} Var(int i) : v(i) {} Var(const char *s) : v(s[0]) {} Var(const Var &o) : v(o.v) {} ~Var() {} };
struct Arr {
	int sum = 0, n = 0;
	Arr() {}
	Arr(std::initializer_list<Var> l) { for (const Var &x : l) { sum += x.v; n++; } }
};
struct Cmp { const Var &base; bool operator()(int a, int b) const { return a + base.v < b; } };
template <typename C, typename... A> bool run(int a, int b, A &&...args) { C c{ args... }; return c(a, b); }
static int total(const Var *a, int n) { int t = 0; for (int i = 0; i < n; i++) t += a[i].v; return t; }
int main() {
	const Arr &k = { 1, "a", Var(3) };
	const Var args[2] = { 5, "b" };
	Var id(7);
	const Var *ptrs[2] = { &id, &args[1] };
	printf("%d %d %d %d\n", k.sum, k.n, total(args, 2), ptrs[1]->v);
	Var base(10);
	printf("%d %d\n", run<Cmp>(1, 20, base), run<Cmp>(1, 5, base));
}
