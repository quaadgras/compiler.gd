#include <cstdio>
template <typename K, typename D, void (*BeforeEvict)(K &, D &) = nullptr>
struct LRU {
	K k; D d;
	void evict() {
		if constexpr (BeforeEvict != nullptr) {
			BeforeEvict(k, d);
		}
		printf("evict %d\n", (int)k);
	}
};
static void before(int &k, double &d) { printf("before %d %g\n", k, d); }
struct Name { struct Table; int x; };
struct Name::Table {
	constexpr static unsigned BITS = 4;
	constexpr static unsigned LEN = 1 << BITS;
	constexpr static unsigned MASK = LEN - 1;
	int slots[LEN];
};
int main() {
	LRU<int, double> a{1, 2}; a.evict();
	LRU<int, double, before> b{3, 4.5}; b.evict();
	printf("%u %u %zu\n", Name::Table::LEN, Name::Table::MASK, sizeof(Name::Table) / sizeof(int));
}
