#include <cstdio>
#define AI __attribute__((always_inline)) inline
template <typename T> struct Rf { T *p; Rf(T *q) : p(q) {} };
struct Obj { int id = 4; };
struct DB {
	static Obj *inst(int id) { static Obj o; return id ? &o : nullptr; }
	template <typename T> AI static Rf<T> get_ref(int id);
	template <typename T> AI static int size() { return sizeof(T); }
};
template <typename T> Rf<T> DB::get_ref(int id) { return Rf<T>(static_cast<T *>(inst(id))); }
struct User { Rf<Obj> t{nullptr}; int go() { t = DB::get_ref<Obj>(1); return t.p->id + DB::size<double>(); } };
int main() { User u; printf("%d\n", u.go()); }
