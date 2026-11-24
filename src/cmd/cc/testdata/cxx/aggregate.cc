#include <cstdio>
#include <mutex>
template <typename T> struct Less { bool operator()(const T &a, const T &b) const { return a < b; } };
struct ByMod { int m; bool operator()(int a, int b) const { return a % m < b % m; } };
template <typename T, typename C = Less<T>>
struct Sorter {
	C compare;
	void sort(T *d, int n) {
		for (int i = 1; i < n; i++)
			for (int j = i; j > 0 && compare(d[j], d[j - 1]); j--) { T t = d[j]; d[j] = d[j - 1]; d[j - 1] = t; }
	}
};
template <typename C, typename... Args>
void sort_custom(int *d, int n, Args &&...args) {
	Sorter<int, C> sorter{ args... };
	sorter.sort(d, n);
}
struct Trio { int a, b, c; };
template <typename... A> int sum3(A... a) { Trio t{ a... }; int v[] = { a..., 100 }; return t.a + t.b + t.c + v[sizeof...(a)]; }
struct Counter {
	mutable std::mutex mutex;
	mutable int count = 0;
	void post(int n = 1) const { std::lock_guard lock(mutex); count += n; }
};
int main() {
	int d[6] = {9, 4, 7, 1, 8, 3};
	sort_custom<Less<int>>(d, 6);
	for (int x : d) printf("%d ", x);
	sort_custom<ByMod>(d, 6, 4);
	for (int x : d) printf("%d ", x);
	printf("\n%d %d\n", sum3(1, 2, 3), sum3(5));
	Counter c; c.post(); c.post(4);
	printf("%d\n", c.count);
}
