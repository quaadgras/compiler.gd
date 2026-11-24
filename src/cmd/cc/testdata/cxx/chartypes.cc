#include <cstdio>
#include <cstdint>
struct V {
	uint32_t u;
	operator uint32_t() const { return u; }
	operator char32_t() const { return operator uint32_t() + 1; }
	operator char16_t() const { return 16; }
	operator wchar_t() const { return 9; }
};
template <typename T> struct Info { static const int id = 0; };
template <> struct Info<uint32_t> { static const int id = 1; };
template <> struct Info<char32_t> { static const int id = 2; };
template <> struct Info<char16_t> { static const int id = 3; };
template <> struct Info<wchar_t> { static const int id = 4; };
template <> struct Info<uint16_t> { static const int id = 5; };
static int f(uint32_t) { return 1; }
static int f(char32_t) { return 2; }
static int f(char16_t) { return 3; }
static int f(wchar_t) { return 4; }
static int f(int) { return 5; }
int main() {
	V v{40};
	char32_t c = v; uint32_t u = v; char16_t h = v; wchar_t w = v;
	const char32_t *s = U"hi"; const char16_t *s16 = u"yo"; const wchar_t *ws = L"w";
	printf("%u %u %u %u\n", (unsigned)c, u, (unsigned)h, (unsigned)w);
	printf("%d %d %d %d %d\n", Info<uint32_t>::id, Info<char32_t>::id, Info<char16_t>::id, Info<wchar_t>::id, Info<uint16_t>::id);
	printf("%d %d %d %d %d %d\n", f(u), f(c), f(h), f(w), f('a'), f(U'x'));
	printf("%d %d %d %zu\n", f(s[0]), f(s16[1]), f(ws[0]), sizeof(c) + sizeof(h));
	c = 'a' + 1; c++; u = c; int i = c + h;
	printf("%u %d\n", u, i);
}
