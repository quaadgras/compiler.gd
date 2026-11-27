// A pack expanded in a member's braced initializer, and in an alias template's arguments.
#include <stdio.h>
#include <type_traits>
template <typename T>
using Simple = std::remove_cv_t<std::remove_reference_t<T>>;
template <typename... T>
struct Tup;
template <>
struct Tup<> { int n() { return 0; } };
template <typename A, typename... R>
struct Tup<A, R...> : Tup<R...> {
	A v;
	Tup(A a, R... r) : Tup<R...>(r...), v(a) {}
	int n() { return 1 + Tup<R...>::n(); }
};
struct Outer {
	template <typename T, typename R, typename... Args>
	struct Cmd {
		T *inst;
		R *ret;
		Tup<Simple<Args>...> args;
		Cmd(T *i, R *r, Simple<Args>... a) : inst(i), ret(r), args{a...} {}
	};
	template <typename T, typename R, typename... Args>
	int push(T *t, R *r, Simple<Args>... a) {
		Cmd<T, R, Args...> c(t, r, a...);
		return c.args.n();
	}
};
int main() {
	int x = 1;
	double d;
	Outer o;
	Outer::Cmd<int, double, const int &, const char *> c(&x, &d, 3, "s");
	printf("%d %d %s\n", c.args.n(), c.args.v, static_cast<Tup<const char *> &>(c.args).v);
}
