#include <cstdio>
#include <cstring>
struct S {
	char b[16];
	S() { b[0] = 0; printf("S()\n"); }
	S(const char *s) { strcpy(b, s); printf("S(%s)\n", b); }
	S(const S &o) { strcpy(b, o.b); printf("copy %s\n", b); }
	~S() { printf("~S %s\n", b); }
	S operator+(const S &o) const { S r; strcpy(r.b, b); strcat(r.b, o.b); return r; }
};
struct P {
	int *p;
	P() : p(nullptr) {}
	P(decltype(nullptr)) : p(nullptr) { printf("P(null)\n"); }
	P(int *q) : p(q) { printf("P(ptr)\n"); }
	~P() { printf("~P %d\n", p ? *p : -1); }
};
enum Mode { M_A, M_B };
enum class K { X, Y };
static void take(Mode m) { printf("mode %d\n", m); }
static void take(int m) { printf("int %d\n", m); }
static void tk(K k) { printf("k %d\n", (int)k); }
static P make(int *q) { return P(q); }
static S name(int i) { return i > 0 ? S("pos") : "neg"; }
int main(int argc, char **argv) {
	bool t = argc > 0, f = argc > 5;
	{ S a = t ? S("x") : S("y"); printf("%s\n", a.b); }
	{ S a = f ? S("x") : S("y"); printf("%s\n", a.b); }
	{ S a("left"); S b = t ? a : "lit"; S c = f ? a : "lit"; printf("%s %s\n", b.b, c.b); }
	{ S a("l"); S b = a + (t ? S("one") : ""); S pre("pre"); S c = pre + (f ? "q" : a); printf("%s %s\n", b.b, c.b); }
	{ int v = 4; P p = t ? make(&v) : nullptr; P q = f ? make(&v) : nullptr; }
	{ S r = name(1); S q = name(-1); printf("%s %s\n", r.b, q.b); }
	take(t ? M_A : M_B);
	take(f ? M_A : M_B);
	tk(f ? K::X : K::Y);
	const S &r = t ? S("ref") : S("other");
	printf("%s\n", r.b);
}
