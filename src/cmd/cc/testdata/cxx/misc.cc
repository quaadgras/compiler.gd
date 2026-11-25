// Scoped enumerations declared again, raw strings, and functions whose
// return type is deduced.
#include <cstdio>
#include <cstring>
#include <cstddef>

enum class Button { A, B, MAX = 5 };
enum class Button;
enum class Later : unsigned char;
enum class Later : unsigned char { X = 200, Y };

struct Pad {
	bool last[(size_t)Button::MAX] = { false };
	auto count() const { return sizeof(last) / sizeof(last[0]); }
};

static auto twice(int x) { return x * 2.5; }
inline auto none() {}
template <typename T> auto half(T x) { return x / 2; }

static const char *raw = R"(a "quoted" \n line
second)";
static const char *delim = R"xy(with )" inside)xy";

int main() {
	Pad p;
	none();
	printf("%zu %d %d\n", p.count(), (int)Button::MAX, (int)Later::Y);
	printf("%g %d %g\n", twice(3), half(9), half(9.0));
	printf("%s|%zu\n%s\n", raw, strlen(raw), delim);
}
