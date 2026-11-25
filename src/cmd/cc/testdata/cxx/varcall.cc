#include <cstdio>
struct Var { int v; Var() : v(0) {} Var(int i) : v(i) {} Var(unsigned i) : v(i + 100) {} Var(const char *s) : v(s[0]) {} Var(const Var &o) : v(o.v) {} ~Var() {} };
struct Str { char b[8]; Str() { b[0] = 0; } Str(const wchar_t *s) { b[0] = 'w'; b[1] = 0; } Str(const char *s) { int i = 0; for (; s[i] && i < 7; i++) b[i] = s[i]; b[i] = 0; } };
struct Call {
	int base;
	int callp(const Var **a, int n) const { int t = base; for (int i = 0; i < n; i++) t += a[i]->v; return t; }
	template <typename... VarArgs> int call(VarArgs... p_args) const {
		Var args[sizeof...(p_args) + 1] = { p_args..., 0 };
		const Var *ptrs[sizeof...(p_args) + 1];
		for (unsigned i = 0; i < sizeof...(p_args); i++) ptrs[i] = &args[i];
		return callp(sizeof...(p_args) ? ptrs : nullptr, sizeof...(p_args));
	}
	template <typename... VarArgs> Call bind(VarArgs... p_args) const { Call c{base + call(p_args...)}; return c; }
};
enum VType { NIL, DICT = 27 };
struct Prop { int type; Str name; int hint; Prop() : type(0), hint(0) {} Prop(const VType t, const Str &n, const int h = 0, const Str &hs = "") : type(t), name(n), hint(h) {} Prop(const Str &cls) : type(-1), name(cls), hint(0) {} };
struct Method {
	int kind; Str name; int nargs = 0;
	Method() : kind(0) {}
	Method(const Str &n) : kind(1), name(n) {}
	template <typename... A> Method(const Str &n, A... p) : kind(2), name(n), nargs(sizeof...(p)) {}
	Method(VType ret) : kind(3) {}
	Method(VType ret, const Str &n) : kind(4), name(n) {}
	template <typename... A> Method(VType ret, const Str &n, A... p) : kind(5), name(n), nargs(sizeof...(p)) {}
	Method(const Prop &ret, const Str &n) : kind(6), name(n) {}
	template <typename... A> Method(const Prop &ret, const Str &n, A... p) : kind(7), name(n), nargs(sizeof...(p)) {}
};
static Call make(int b) { return Call{b}; }
int main() {
	Call c{1};
	unsigned w = 4;
	const Str err("e");
	printf("%d %d %d\n", c.call(w), c.call(), c.call(1, "a"));
	printf("%d\n", make(2).bind(err.b).base);
	Method m(NIL, "_validate", Prop(DICT, "property"));
	Method n("x"), o("y", Prop(DICT, "p"), Prop(NIL, "q")), q(Prop(DICT, "r"), "z");
	printf("%d %s %d  %d %d %d %d\n", m.kind, m.name.b, m.nargs, n.kind, o.kind, o.nargs, q.kind);
}
