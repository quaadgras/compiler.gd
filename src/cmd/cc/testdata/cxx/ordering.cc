#include <cstdio>
template <typename T, typename = void> struct Info;
template <> struct Info<double> { static const int TYPE = 2; };
template <> struct Info<int> { static const int TYPE = 1; };
struct A { double f(int) { return 0; } int g(int, int) const { return 0; } void h() {} void k(int) const {} };
template <typename R, typename T, typename... P> static inline int ret(R (T::*m)(P...)) { return Info<R>::TYPE; }
template <typename R, typename T, typename... P> static inline int ret(R (T::*m)(P...) const) { return Info<R>::TYPE + 10; }
template <typename T, typename... P> static inline int ret(void (T::*m)(P...)) { return 0; }
template <typename T, typename... P> static inline int ret(void (T::*m)(P...) const) { return -1; }
template <typename R, typename... P> static inline int ret(R (*m)(P...)) { return Info<R>::TYPE + 20; }
template <typename... P> static inline int ret(void (*m)(P...)) { return 20; }
template <typename T> struct Vec { T v; };
template <typename T> int kind(const T &) { return 1; }
template <typename T> int kind(const Vec<T> &) { return 2; }
template <typename T> int kind(T *) { return 3; }
template <typename T> int kind(const Vec<T *> &) { return 4; }
static double fd(int) { return 0; }
static void fv(int, int) {}
int main() {
	printf("%d %d %d %d %d %d\n", ret(&A::f), ret(&A::g), ret(&A::h), ret(&A::k), ret(fd), ret(fv));
	Vec<int> v{1}; Vec<int *> vp{nullptr}; int i = 0;
	printf("%d %d %d %d\n", kind(i), kind(v), kind(&i), kind(vp));
}
