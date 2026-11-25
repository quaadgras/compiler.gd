// Templates: classes, functions, specializations, aliases, variables.
#include <cstdio>
#include <cstring>
#include <cstdlib>
#include <new>

template <class T>
T max_of(T a, T b) {
	return a < b ? b : a;
}

template <class T>
void swap_values(T &a, T &b) {
	T t = a;
	a = b;
	b = t;
}

template <class T, int N>
int count_of(T (&)[N]) {
	return N;
}

template <class T, class U = int>
struct Pair {
	T first;
	U second;
	Pair(const T &a, const U &b) : first(a), second(b) {}
	T sum() const { return first + T(second); }
	template <class V>
	V scaled(V k) const { return V(first) * k; }
	static int instances;
};

template <class T, class U>
int Pair<T, U>::instances = 0;

// A growing array, with members defined outside it.
template <class T>
class Vec {
	T *data = nullptr;
	int n = 0, cap = 0;

public:
	Vec() {}
	Vec(const Vec &o) {
		for (int i = 0; i < o.n; i++)
			push(o.data[i]);
	}
	~Vec();
	void push(const T &v);
	int size() const { return n; }
	T &operator[](int i) { return data[i]; }
	const T &operator[](int i) const { return data[i]; }
	T *begin() { return data; }
	T *end() { return data + n; }
	template <class F>
	void each(F f) const {
		for (int i = 0; i < n; i++)
			f(data[i]);
	}
	// Never instantiated: T need not have it.
	void unused() { data[0].no_such_member(); }
};

template <class T>
Vec<T>::~Vec() {
	for (int i = 0; i < n; i++)
		data[i].~T();
	free(data);
}

template <class T>
void Vec<T>::push(const T &v) {
	if (n == cap) {
		cap = cap ? cap * 2 : 4;
		T *nd = (T *)malloc(cap * sizeof(T));
		for (int i = 0; i < n; i++) {
			new (&nd[i]) T(data[i]);
			data[i].~T();
		}
		free(data);
		data = nd;
	}
	new (&data[n++]) T(v);
}

struct Named {
	char name[8];
	Named(const char *s) { strncpy(name, s, 7), name[7] = 0; }
	Named(const Named &o) { memcpy(name, o.name, 8); }
	~Named() { live--; }
	static int live;
};
int Named::live = 0;

// Specializations.
template <class T>
struct Traits {
	static const char *name() { return "other"; }
	enum { is_pointer = 0 };
};
template <>
struct Traits<int> {
	static const char *name() { return "int"; }
	enum { is_pointer = 0 };
};
template <class T>
struct Traits<T *> {
	static const char *name() { return "pointer"; }
	enum { is_pointer = 1 };
	typedef T pointee;
};
template <class T>
struct Traits<const T> {
	static const char *name() { return "const"; }
	enum { is_pointer = Traits<T>::is_pointer };
};

template <class T>
using Ptr = T *;
template <class T>
using PairOf = Pair<T, T>;

template <class T>
constexpr bool is_small = sizeof(T) <= 4;
template <class T>
constexpr int size_of = sizeof(T);

template <int N>
struct Fact {
	static const int value = N * Fact<N - 1>::value;
};
template <>
struct Fact<0> {
	static const int value = 1;
};

template <class T, int N>
struct Array {
	T v[N];
	int size() const { return N; }
	T &operator[](int i) { return v[i]; }
};

// A node that points to its own type, and a template that uses another
// before it is complete.
template <class T>
struct Node {
	T value;
	Node<T> *next;
	Node(const T &v, Node *n) : value(v), next(n) {}
};

template <class T>
struct List {
	Node<T> *head = nullptr;
	void push(const T &v) { head = new Node<T>(v, head); }
	T total() const {
		T s = T();
		for (Node<T> *n = head; n; n = n->next)
			s += n->value;
		return s;
	}
};

struct Printer {
	int n = 0;
	void operator()(int v) {
		printf("[%d]", v);
		n++;
	}
};
void show(int v) { printf("<%d>", v); }

template <class T>
T twice(T x) { return x + x; }
const char *twice(const char *) { return "not a template"; }

template <class To, class From>
To convert(From f) { return To(f); }

namespace ns {
template <class T>
struct Box {
	T v;
	T get() const { return v; }
};
template <class T>
T unbox(const Box<T> &b) { return b.get(); }
}

int main() {
	printf("%d %g %c\n", max_of(3, 7), max_of(2.5, 1.5), max_of('a', 'z'));
	int a = 1, b = 2;
	swap_values(a, b);
	double arr[5];
	printf("%d %d %d\n", a, b, count_of(arr));

	Pair<int> p(3, 4);
	Pair<double, char> q(1.5, 2);
	PairOf<long> r(10, 20);
	printf("%d %g %ld %g\n", p.sum(), q.sum(), r.sum(), p.scaled(2.5));
	Pair<int>::instances = 5;
	Pair<double, char>::instances++;
	printf("%d %d %zu %zu\n", Pair<int, int>::instances, Pair<double, char>::instances, sizeof(p), sizeof(q));

	{
		Vec<int> v;
		for (int i = 0; i < 10; i++)
			v.push(i * i);
		Vec<int> w = v;
		w[0] = 100;
		int sum = 0;
		for (int *it = w.begin(); it != w.end(); ++it)
			sum += *it;
		printf("%d %d %d\n", v.size(), v[9], sum);
		Printer pr;
		v.each(pr);
		v.each(show);
		printf("\n");
		Vec<Named> names;
		names.push("one");
		names.push(Named("two"));
		Named::live = names.size();
		printf("%s %s %d\n", names[0].name, names[1].name, Named::live);
	}
	printf("live %d\n", Named::live);

	printf("%s %s %s %s\n", Traits<int>::name(), Traits<char>::name(), Traits<int *>::name(), Traits<const int>::name());
	printf("%d %d %d\n", Traits<int>::is_pointer, Traits<char **>::is_pointer, Traits<const char *const>::is_pointer);
	Traits<long *>::pointee lp = 5;
	Ptr<int> ip = &a;
	printf("%ld %d %d %d %d\n", lp, *ip, is_small<char>, is_small<double>, size_of<short>);
	printf("%d %d\n", Fact<5>::value, Fact<10>::value);

	Array<int, 3> fixed;
	fixed[0] = 7;
	printf("%d %d %zu\n", fixed.size(), fixed[0], sizeof(Array<char, 9>));

	List<int> list;
	for (int i = 1; i <= 4; i++)
		list.push(i);
	List<double> dl;
	dl.push(1.5);
	dl.push(2.5);
	printf("%d %g\n", list.total(), dl.total());

	printf("%d %s %g\n", twice(21), twice("x"), twice(1.25));
	printf("%d %g %zu\n", convert<int>(3.9), convert<double, int>(2), std::strlen("four"));
	ns::Box<int> box = {42};
	printf("%d %d\n", ns::unbox(box), unbox(box));
	return 0;
}
