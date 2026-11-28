#include <stdio.h>
typedef void (*Notify)(const char *);
static Notify hook;
static void set_hook(Notify n) { hook = n; }
struct Iface {
	int marks = 0;
	static Iface *get() { static Iface i; return &i; }
	void mark() { marks++; }
};
template <class T, class M>
struct Bound {
	T *t;
	M m;
	void call() { (t->*m)(); }
};
template <class T, class M>
static Bound<T, M> bind(T *t, M m) { return Bound<T, M>{t, m}; }
struct Node {
	int id = 1;
	static void warn(const char *w) { printf("warn %s\n", w); }

	void setup() {
		set_hook([](const char *w) {
			warn(w);
			bind(Iface::get(), &Iface::mark).call();
		});
	}
};
int main() {
	Node n;
	n.setup();
	hook("x");
	printf("%d\n", Iface::get()->marks);
}

// A conversion function template whose first parameter has a default.
#include <type_traits>
struct Base { int b = 1; };
struct Derived : Base { int d = 2; };
template <class T>
struct Ref {
	T *p = nullptr;
	Ref() {}
	explicit Ref(T *p) : p(p) {}
	void operator=(const Ref &o) { p = o.p; }
};
template <class T>
struct Result {
	T *v;
	template <class U = T, class O, std::enable_if_t<std::is_base_of_v<U, O>, int> = 0>
	operator Ref<O>() const { return Ref<O>(static_cast<O *>(v)); }
};
static Derived the;
static Result<Base> make() { return Result<Base>{&the}; }
static int conv() {
	Ref<Derived> r;
	r = make();
	return printf("%d\n", r.p->d);
}
static int convDone = conv();

// The implicit move of a class whose member cannot be copied; and a
// member of a nested class template defined outside.
struct Lock {
	int *held;
	explicit Lock(int *h) : held(h) { ++*held; }
	Lock(const Lock &) = delete;
	Lock &operator=(const Lock &) = delete;
	Lock(Lock &&o) : held(o.held) { o.held = nullptr; }
	Lock &operator=(Lock &&o) {
		if (held) {
			--*held;
		}
		held = o.held;
		o.held = nullptr;
		return *this;
	}
	~Lock() {
		if (held) {
			--*held;
		}
	}
};
struct Guard {
	Lock lock;
	explicit Guard(int *h) : lock(h) {}
};
struct View {
	template <class T>
	struct Override {
		T value;
		bool on = false;
		bool enabled() const;
		static const T limit;
	};
};
template <class T>
bool View::Override<T>::enabled() const { return on; }
template <class T>
const T View::Override<T>::limit = 9;
static int moves() {
	int a = 0, b = 0;
	{
		Guard g(&a);
		printf("%d %d\n", a, b);
		g = Guard(&b);
		printf("%d %d\n", a, b);
		Guard h(static_cast<Guard &&>(g));
		printf("%d %d %d\n", a, b, g.lock.held == nullptr);
	}
	View::Override<int> o;
	o.on = true;
	return printf("%d %d %d %d\n", a, b, o.enabled(), View::Override<long>::limit == 9);
}
static int movesDone = moves();
