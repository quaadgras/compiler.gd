#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <new>
struct Str {
	char *p = nullptr;
	Str() {}
	Str(const char *s) { p = (char *)malloc(strlen(s) + 1); strcpy(p, s); }
	Str(const Str &o) { if (o.p) { p = (char *)malloc(strlen(o.p) + 1); strcpy(p, o.p); } }
	~Str() { free(p); }
	const char *c() const { return p ? p : ""; }
};
template <typename K, typename V>
struct KeyValue {
	const K key{};
	V value{};
	KeyValue &operator=(const KeyValue &) = delete;
	KeyValue &operator=(KeyValue &&) = delete;
	constexpr KeyValue(const KeyValue &) = default;
	constexpr KeyValue(KeyValue &&) = default;
	constexpr KeyValue(const K &k, const V &v) : key(k), value(v) {}
};
template <typename K, typename V>
struct Elem {
	Elem *next = nullptr;
	Elem *prev = nullptr;
	KeyValue<K, V> data;
	Elem() {}
	Elem(const K &k, const V &v) : data(k, v) {}
};
struct DefaultAllocator {};
static void *alloc_static(size_t n) {
	unsigned char *m = (unsigned char *)malloc(n + 16);
	unsigned long *s = (unsigned long *)(m + 0);
	*s = n;
	return m + 16;
}
void *operator new(size_t n, DefaultAllocator) { return alloc_static(n); }
template <typename T> T *post(T *p) { return p; }
template <typename T>
struct Alloc {
	template <typename... Args>
	inline T *new_allocation(const Args &&...a) { return post(::new (DefaultAllocator{}) T(a...)); }
};
template <typename K, typename V>
struct Map : private Alloc<Elem<K, V>> {
	Elem<K, V> *head = nullptr;
	V &insert(const K &k, const V &v) {
		Elem<K, V> *e = Alloc<Elem<K, V>>::new_allocation(Elem<K, V>(k, v));
		e->next = head;
		if (head) head->prev = e;
		head = e;
		return e->data.value;
	}
};
int main() {
	Map<Str, int> m;
	m.insert("one", 1) += 10;
	m.insert("two", 2);
	Map<int, Str> r;
	r.insert(3, "three");
	for (Elem<Str, int> *e = m.head; e; e = e->next) printf("%s=%d ", e->data.key.c(), e->data.value);
	printf("%d=%s\n", r.head->data.key, r.head->data.value.c());
}
