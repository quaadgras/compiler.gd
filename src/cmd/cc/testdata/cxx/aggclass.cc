#include <cstdio>
#include <type_traits>
#include <mutex>
struct Str { char b[8]; Str() { b[0] = 0; } Str(const char *s) { int i = 0; for (; s[i] && i < 7; i++) b[i] = s[i]; b[i] = 0; } Str(const Str &o) { for (int i = 0; i < 8; i++) b[i] = o.b[i]; } ~Str() {} };
struct Name { Str s; int id; Name() : id(0) {} Name(const char *n) : s(n), id(1) {} };
struct Key { Name ctx; Str id; int n = 3; };
struct Plain { int a; Key k; double d; };
template <class MutexT>
class Lock {
	mutable std::unique_lock<MutexT> lock;
public:
	explicit Lock(const MutexT &m) : lock(const_cast<MutexT &>(m)) {}
	template <typename T = MutexT>
	inline std::unique_lock<std::mutex> &_get_lock(
			typename std::enable_if<std::is_same<T, std::mutex>::value> * = nullptr) const {
		return lock;
	}
};
struct CV {
	template <typename M> bool owns(const Lock<M> &l) const { return l._get_lock().owns_lock(); }
};
static Key make(const Str &v) { return { Name(), v }; }
int main() {
	const Key k = { Name(), "id" };
	Key k2 = { "ctx", Str("x"), 9 };
	Key k3{};
	Plain p = { 1, { "c", "i" }, 2.5 };
	Key k4 = make("mk");
	printf("%d %s %d|%s %s %d|%d [%s] %d|%d %s %s %d %g|%s\n", k.ctx.id, k.id.b, k.n, k2.ctx.s.b, k2.id.b, k2.n, k3.ctx.id, k3.id.b, k3.n, p.a, p.k.ctx.s.b, p.k.id.b, p.k.n, p.d, k4.id.b);
	std::mutex m; std::recursive_mutex rm;
	Lock<std::mutex> l(m); Lock<std::recursive_mutex> rl(rm);
	CV cv;
	printf("%d\n", cv.owns(l));
}
