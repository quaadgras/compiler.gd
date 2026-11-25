// Index sequences: packs of values, specializations that end a recursion,
// deduction from a base class that is a template's instance.
#include <cstddef>
#include <cstdio>
template <size_t... Is> struct IndexSequence {};
template <size_t N, size_t... Is> struct BuildIndexSequence : BuildIndexSequence<N - 1, N - 1, Is...> {};
template <size_t... Is> struct BuildIndexSequence<0, Is...> : IndexSequence<Is...> {};
struct V { int v; };
struct Obj { int twice(int a, int b) const { return a * 2 + b; } };
template <typename T, typename R, typename... P, size_t... Is>
void helper(T *p_instance, R (T::*p_method)(P...) const, const V **p_args, V &r_ret, IndexSequence<Is...>) {
	r_ret.v = (p_instance->*p_method)(p_args[Is]->v...);
}
template <typename T, typename R, typename... P>
void call(T *p_instance, R (T::*p_method)(P...) const, const V **p_args, V &r_ret) {
	helper(p_instance, p_method, p_args, r_ret, BuildIndexSequence<sizeof...(P)>{});
}
template <size_t... Is> int count(IndexSequence<Is...>) { return sizeof...(Is); }
int main() {
	Obj o;
	V a = {3}, b = {4}, r = {0};
	const V *args[2] = {&a, &b};
	call(&o, &Obj::twice, args, r);
	printf("%d %d\n", r.v, count(BuildIndexSequence<5>{}));
	return 0;
}
