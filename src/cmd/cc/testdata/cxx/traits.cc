// The type traits, parameter packs, if constexpr, move and forward.
#include <cstdio>
#include <type_traits>
#include <utility>

struct Plain {
	int x;
};
struct Base {
	virtual ~Base() {}
};
struct Derived : Base {
	int v = 0;
};
struct Owner {
	int *p;
	Owner() : p(new int(1)) {}
	Owner(const Owner &o) : p(new int(*o.p)) { copies++; }
	Owner(Owner &&o) : p(o.p) {
		o.p = nullptr;
		moves++;
	}
	Owner &operator=(const Owner &o) {
		*p = *o.p;
		copies++;
		return *this;
	}
	Owner &operator=(Owner &&o) {
		delete p;
		p = o.p;
		o.p = nullptr;
		moves++;
		return *this;
	}
	~Owner() { delete p; }
	static int copies, moves;
};
int Owner::copies = 0;
int Owner::moves = 0;
enum Color { Red };
enum class Wide : unsigned long { A };

// SFINAE: by a default template argument, a return type, a parameter.
template <class T, class = std::enable_if_t<std::is_integral_v<T>>>
const char *kind(T) { return "integral"; }
template <class T, std::enable_if_t<std::is_floating_point<T>::value, int> = 0>
const char *kind(T) { return "floating"; }
template <class T>
std::enable_if_t<std::is_pointer_v<T>, const char *> kind(T) { return "pointer"; }
template <class T>
const char *kind(T, typename std::enable_if<std::is_class<T>::value>::type * = nullptr) { return "class"; }

// A trait of one's own, by partial specialization on void_t.
template <class T, class = void>
struct has_size : std::false_type {};
template <class T>
struct has_size<T, std::void_t<typename T::size_type>> : std::true_type {};
struct Sized {
	typedef int size_type;
};

// Packs.
template <class... Ts>
int count(Ts...) { return sizeof...(Ts); }
template <class... Ts>
int sum(Ts... xs) { return (xs + ... + 0); }
template <class... Ts>
bool all(Ts... xs) { return (... && xs); }
int add3(int a, int b, int c) { return a + b + c; }
template <class... Ts>
int twice_sum(Ts... xs) { return add3((xs * 2)...); }
template <class T>
void print1(const T &v) {
	if constexpr (std::is_floating_point_v<T>)
		printf("%g ", double(v));
	else if constexpr (std::is_pointer_v<T>)
		printf("%s ", v);
	else
		printf("%d ", int(v));
}
template <class... Ts>
void print(const Ts &...xs) {
	(print1(xs), ...);
	printf("\n");
}
template <class F, class... Args>
auto call(F f, Args &&...args) -> decltype(f(std::forward<Args>(args)...)) {
	return f(std::forward<Args>(args)...);
}
template <class... Ts>
struct Tuple {
	enum { size = sizeof...(Ts) };
};
template <class T, class... Rest>
struct First {
	typedef T type;
};
template <class T>
struct Wrap {
	T v;
	template <class... A>
	Wrap(A &&...a) : v(std::forward<A>(a)...) {}
};
struct Three {
	int a, b, c;
	Three(int a, int b, int c) : a(a), b(b), c(c) {}
};

const char *cat(Owner &) { return "lvalue"; }
const char *cat(const Owner &) { return "const lvalue"; }
const char *cat(Owner &&) { return "rvalue"; }
template <class T>
const char *relay(T &&x) { return cat(std::forward<T>(x)); }

int main() {
	printf("%d %d %d %d\n", std::is_same_v<int, int>, std::is_same<int, long>::value, std::is_same_v<const int, int>,
		std::is_same_v<std::remove_cv_t<const volatile int>, int>);
	printf("%d %d %d %d\n", std::is_base_of_v<Base, Derived>, std::is_base_of_v<Derived, Base>, std::is_base_of<Base, Base>::value,
		std::is_convertible_v<Derived *, Base *>);
	printf("%d %d %d %d\n", std::is_enum_v<Color>, std::is_class_v<Plain>, std::is_polymorphic_v<Derived>, std::is_empty_v<Sized>);
	printf("%d %d %d %d\n", std::is_trivially_copyable_v<Plain>, std::is_trivially_copyable_v<Owner>,
		std::is_trivially_destructible_v<int>, std::is_trivially_destructible_v<Derived>);
	printf("%d %d %d %d\n", std::is_trivially_constructible_v<Plain>, std::is_trivially_constructible_v<Owner>,
		std::is_constructible_v<Three, int, int, int>, std::is_constructible_v<Three, int>);
	printf("%zu %zu %d %d\n", sizeof(std::underlying_type_t<Wide>), sizeof(std::make_unsigned_t<int>),
		std::is_unsigned_v<std::make_unsigned_t<long>>, std::is_signed_v<std::underlying_type_t<Color>>);
	printf("%d %d %d\n", std::is_same_v<std::remove_reference_t<int &&>, int>, std::is_same_v<std::decay_t<const int &>, int>,
		std::is_same_v<std::conditional_t<true, int, char>, int>);
	printf("%d %d %d\n", std::conjunction_v<std::true_type, std::is_integral<int>>, std::disjunction_v<std::false_type, std::is_void<int>>,
		std::negation_v<std::is_void<void>>);
	static_assert(std::is_same_v<std::remove_pointer_t<int *>, int>, "remove_pointer");
	static_assert(!std::is_pointer_v<int>);

	Plain pl = {1};
	printf("%s %s %s %s\n", kind(1), kind(1.5), kind(&pl), kind(pl));
	printf("%d %d\n", has_size<Sized>::value, has_size<Plain>::value);

	printf("%d %d %d %d\n", count(), count(1, 'a', 2.5), sum(1, 2, 3, 4), sum());
	printf("%d %d %d\n", all(true, 1, 2), all(1, 0), twice_sum(1, 2, 3));
	const char *three = "three";
	print(1, 2.5, three, 'c');
	printf("%d %d %d\n", call(add3, 1, 2, 3), int(Tuple<int, char, long>::size), int(sizeof(First<char, int, long>::type)));
	Wrap<Three> w(1, 2, 3);
	Wrap<int> wi(5);
	printf("%d %d\n", w.v.a + w.v.b + w.v.c, wi.v);

	Owner o;
	const Owner co;
	printf("%s %s %s %s\n", relay(o), relay(co), relay(Owner()), relay(std::move(o)));
	Owner a, b;
	Owner c = std::move(a);
	Owner d = b;
	d = std::move(c);
	d = b;
	std::swap(b, d);
	printf("%d %d %d\n", Owner::copies, Owner::moves, a.p == nullptr);
	std::pair<int, double> pr(1, 2.5);
	auto mp = std::make_pair(3, 'x');
	printf("%d %g %d %c\n", pr.first, pr.second, mp.first, mp.second);
	return 0;
}
