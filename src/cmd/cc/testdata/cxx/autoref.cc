// Functions whose return type is deduced, through templates.
#include <stdio.h>
#include <stddef.h>
#include <utility>

template <typename... Types>
struct Tuple;
template <>
struct Tuple<> {};
template <typename T, typename... Rest>
struct Tuple<T, Rest...> : Tuple<Rest...> {
	T value;
	Tuple() = default;
	template <typename F, typename... R>
	Tuple(F &&f, R &&...r) : Tuple<Rest...>(std::forward<R>(r)...), value(std::forward<F>(f)) {}
};

template <size_t I, typename TupleT>
struct TupleGet;
template <typename First, typename... Rest>
struct TupleGet<0, Tuple<First, Rest...>> {
	static First &tuple_get(Tuple<First, Rest...> &t) { return t.value; }
};
template <size_t I, typename First, typename... Rest>
struct TupleGet<I, Tuple<First, Rest...>> {
	static auto &tuple_get(Tuple<First, Rest...> &t) {
		return TupleGet<I - 1, Tuple<Rest...>>::tuple_get(static_cast<Tuple<Rest...> &>(t));
	}
};
template <size_t I, typename... Types>
auto &tuple_get(Tuple<Types...> &t) {
	return TupleGet<I, Tuple<Types...>>::tuple_get(t);
}

struct Id {
	long id = 0;
	Id() {}
	Id(const Id &o) : id(o.id) {}
};

template <size_t... I>
struct Seq {};

struct Target {
	void run(Id a, double b, const char *c) { printf("run %ld %g %s\n", a.id, b, c); }
};

template <typename T, typename M, typename... Args>
struct Command {
	T *instance;
	M method;
	Tuple<Args...> args;
	template <typename... Fwd>
	Command(T *i, M m, Fwd &&...a) : instance(i), method(m), args(std::forward<Fwd>(a)...) {}
	void call() { call_impl(Seq<0, 1, 2>{}); }

private:
	template <size_t... I>
	void call_impl(Seq<I...>) {
		(instance->*method)(std::move(get<I>())...);
	}
	template <size_t I>
	auto &get() { return ::tuple_get<I>(args); }
};

int main() {
	Target t;
	Id id;
	id.id = 42;
	Command<Target, void (Target::*)(Id, double, const char *), Id, double, const char *> c(&t, &Target::run, id, 2.5, "x");
	c.call();
	Tuple<int, Id, double> tp(1, id, 3.5);
	printf("%d %ld %g\n", tuple_get<0>(tp), tuple_get<1>(tp).id, tuple_get<2>(tp));
}
