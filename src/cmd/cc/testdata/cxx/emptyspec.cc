// An explicit specialization with no arguments, chosen for an empty pack.
#include <stdio.h>
#include <type_traits>
template <typename T>
using Simple = std::remove_cv_t<std::remove_reference_t<T>>;
template <typename... T>
struct Tuple;
template <>
struct Tuple<> {};
template <typename T, typename... R>
struct Tuple<T, R...> : Tuple<R...> {
	T value;
};
struct Base {
	virtual void call() = 0;
	virtual ~Base() = default;
};
template <typename T, typename... A>
struct Cmd : Base {
	T *inst;
	Tuple<Simple<A>...> args;
	void call() override { printf("call %d\n", (int)sizeof...(A)); }
};
struct Queue {
	template <typename T, typename... A>
	void push(T *t, A... a) {
		static_assert(sizeof(Cmd<T, A...>) <= 1024, "too big");
		Cmd<T, A...> c;
		c.inst = t;
		c.call();
	}
};
int main() {
	Queue q;
	int x;
	q.push(&x);
	q.push(&x, 1, 2.5);
}
