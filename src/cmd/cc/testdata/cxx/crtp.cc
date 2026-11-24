#include <cstdio>
template <typename T> struct GetInfo { static const int TYPE = 1; };
template <> struct GetInfo<double> { static const int TYPE = 2; };
template <> struct GetInfo<void> { static const int TYPE = 0; };
struct A { double f(int) { return 0; } int g(int, int) const { return 0; } void h() {} };
template <typename R, typename T, typename... P>
static inline int ret_type(R (T::*method)(P...)) { return GetInfo<R>::TYPE; }
template <typename R, typename T, typename... P>
static inline int ret_type(R (T::*method)(P...) const) { return GetInfo<R>::TYPE; }
template <typename R, typename T, typename... P>
static inline int argc(R (T::*method)(P...) const) { return sizeof...(P); }
template <typename T> struct Common { static int eval(int a) { return T::go(a) + 1; } };
class EqObj : public Common<EqObj> { public: static int go(int a) { return a * 2; } };
struct Stream { int x; };
template <typename T> struct Vec { T d[4]; int n = 0; void push(T v) { d[n++] = v; } };
struct Parser {
	template <typename T> static int construct(Stream *s, Vec<T> &r, int &line, const char *&err);
	static int parse(Stream *s) {
		Vec<double> args; int line = 0; const char *err = "";
		int e = construct<double>(s, args, line, err);
		return e + args.n + line;
	}
};
template <typename T> int Parser::construct(Stream *s, Vec<T> &r, int &line, const char *&err) { r.push(T(s->x)); line = 5; return 1; }
int main() {
	printf("%d %d %d %d\n", ret_type(&A::f), ret_type(&A::g), ret_type(&A::h), argc(&A::g));
	printf("%d\n", Common<EqObj>::eval(4));
	Stream s{3};
	printf("%d\n", Parser::parse(&s));
}
