#include <cstdio>
#include <cstdint>
struct W { int v; explicit operator bool() const { return v != 0; } };
static W get(int i) { return W{i}; }
int main() {
	const char16_t s[3] = {0xfeff, 'a', 0};
	int n = 0;
	if (uint16_t(s[0]) == 0xfeff) n += 1;
	if (int(s[1]) != 'a') n += 2;
	while (unsigned(n) < 4u) n += 4;
	if (int k = n * 2) n += k;
	if (W w = get(n)) n += w.v;
	if (auto w = get(0)) n += 1000;
	for (int i = 0; unsigned(i) < 2; i++) n++;
	switch (int(s[1])) { case 'a': n += 100; }
	printf("%d\n", n);
}
