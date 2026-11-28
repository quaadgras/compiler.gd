#include <stdio.h>
#include <string.h>
#include <stddef.h>
constexpr size_t operator""_uz(unsigned long long n) { return n; }
constexpr long operator""_k(unsigned long long n) { return (long)n * 1000; }
int main() {
	size_t a = 4_uz, b = 0x10_uz;
	for (size_t i = 0_uz; i < 3_uz; i++)
		a += i;
	int more(); printf("%zu %zu %ld %d %d\n", a, b, 2_k, strcasecmp("ABC", "abc"), more());
}
struct Task { bool ready = true; };
struct SwTask : Task {
	bool ready(bool c) { return c && Task::ready; }
};
int more() {
	SwTask t, *p = &t;
	bool ready = true;
	return p->ready(ready) + t.ready(false);
}
