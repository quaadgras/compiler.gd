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
struct Proxy { int i; const char *s; operator char() const { return s[i]; } };
struct Text { const char *s; Proxy operator[](int i) const { return Proxy{i, s}; } };
int kind(const Text &t) {
	switch (t[0]) {
		case 'b': return 1;
		case 'a': return 2;
		default: return 3;
	}
}
static int kinds = kind(Text{"b1"}) * 100 + kind(Text{"a2"}) * 10 + kind(Text{"+"});
struct Report { Report() { printf("%d\n", kinds); } } report;
