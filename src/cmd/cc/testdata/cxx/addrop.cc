// A class's own unary operator&, as a proxy for an element has.
#include <stdio.h>

struct Proxy {
	char *base;
	int index;
	operator char() const { return base[index]; }
	const char *operator&() const { return base + index; }
};
struct Str {
	char data[16];
	Proxy operator[](int i) { return Proxy{data, i}; }
};
struct Mask {
	int bits;
	Mask operator&(const Mask &o) const { return Mask{bits & o.bits}; }
};
int main() {
	Str s = {"hello, world"};
	const char *p = &s[7];
	Mask a{6}, b{3};
	Mask *pa = &a;
	printf("%s %c %d %d\n", p, (char)s[1], (a & b).bits, pa->bits);
	return 0;
}
