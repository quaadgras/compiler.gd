#include <cstdio>
#include <cstddef>
struct V3 { double x, y, z; V3() : x(0), y(0), z(0) {} V3(double a, double b, double c) : x(a), y(b), z(c) {} };
struct Var {
	int kind; long i; double d; V3 v;
	operator long() const { return i; }
	operator double() const { return d; }
	operator V3() const { return v; }
};
template <typename T> struct Caster { static T cast(const Var &v) { return v; } };
template <typename T> struct Caster<T &> { static T cast(const Var &v) { return v; } };
template <typename T> struct Caster<const T &> { static T cast(const Var &v) { return v; } };
template <typename T> struct CV { static T cast(const Var **a, unsigned i, int &err) { return Caster<T>::cast(*a[i]); } };
template <typename T> struct CV<T &> { static T cast(const Var **a, unsigned i, int &err) { return Caster<T>::cast(*a[i]); } };
template <typename T> struct CV<const T &> { static T cast(const Var **a, unsigned i, int &err) { err++; return Caster<T>::cast(*a[i]); } };
template <size_t... Is> struct IndexSequence {};
template <size_t N, size_t... Is> struct BuildIndexSequence : BuildIndexSequence<N - 1, N - 1, Is...> {};
template <size_t... Is> struct BuildIndexSequence<0, Is...> : IndexSequence<Is...> {};
struct A {
	void add(long id, const V3 &p, double w) { printf("%ld %g %g %g %g\n", id, p.x, p.y, p.z, w); }
};
template <typename T, typename... P, size_t... Is>
void helper(T *inst, void (T::*m)(P...), const Var **args, int &err, IndexSequence<Is...>) {
	(inst->*m)(CV<P>::cast(args, Is, err)...);
}
template <typename T, typename... P>
void call(T *inst, void (T::*m)(P...), const Var **args, int &err) {
	helper<T, P...>(inst, m, args, err, BuildIndexSequence<sizeof...(P)>{});
}
int main() {
	A a;
	Var v0{0, 7, 0, V3()}, v1{1, 0, 0, V3(1, 2, 3)}, v2{2, 0, 2.5, V3()};
	const Var *args[3] = {&v0, &v1, &v2};
	int err = 0;
	call(&a, &A::add, args, err);
	printf("%d\n", err);
}
