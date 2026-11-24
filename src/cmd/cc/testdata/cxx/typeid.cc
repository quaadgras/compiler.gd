// typeid, and built-in operators on enumerations beside overloaded ones.
#include <cstdio>
#include <typeinfo>
struct Foo {};
namespace ns { struct Bar {}; }
enum E { A, B };
struct R { R(int) {} R(double) {} };
bool operator==(const R &, const R &) { return false; }
template <class T> const char *name() { return typeid(T).name(); }
int main() {
	E e = A;
	printf("%d %s %s %s %s %d\n", e == B, typeid(int).name(), name<Foo>(), typeid(ns::Bar).name(), typeid(const char *).name(), typeid(Foo) == typeid(Foo));
	return 0;
}
