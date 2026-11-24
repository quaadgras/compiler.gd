#include <cstdio>
#include <cstdint>
struct S {
	int64_t to_int() const;
	static int64_t to_int(const char *p, int n = -1);
	static int64_t to_int(const wchar_t *p, int n = -1);
	static int64_t to_int(const char32_t *p, int n = -1, bool c = false);
};
int64_t S::to_int() const { return 0; }
int64_t S::to_int(const char *p, int n) { return 1; }
int64_t S::to_int(const wchar_t *p, int n) { return 2; }
int64_t S::to_int(const char32_t *p, int n, bool c) { return 3; }
int main() { S s; printf("%ld %ld %ld %ld\n", s.to_int(), S::to_int("1"), S::to_int(L"1"), S::to_int(U"1")); }
